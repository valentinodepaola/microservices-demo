# Vault: configuración, políticas y tokens

Aquí está lo que deja a Vault listo para guardar los secretos del pipeline, una vez que Ansible lo instaló ([`ansible/`](../ansible/README.md), #77) y alguien lo inicializó (#78). Las decisiones de diseño están en el [ADR 0011](../docs/adr/0011-secretos-del-pipeline-en-vault.md).

| Archivo | Qué es |
|---|---|
| `configurar.sh` | Enciende el motor de secretos y la bitácora, carga las políticas y los dos secretos. Se puede correr las veces que sea |
| `policies/pipeline.hcl` | Lo único que puede hacer el pipeline: leer sus dos secretos |
| `policies/respaldo.hcl` | Lo único que puede hacer el token de respaldo: sacar fotos de Vault |

**En el repo no hay ningún secreto.** Los pedazos de la llave, el token raíz y los tokens viven en el gestor de contraseñas y en GitHub, nunca en un archivo, un issue o un chat.

## Antes de cualquier comando

El CLI de Vault en la Mac (`brew install hashicorp/tap/vault`) y, desde la raíz del repo:

```bash
export VAULT_ADDR=https://98.94.123.92:8200
export VAULT_CACERT=$PWD/ansible/vault-ca.crt
```

Si estás en una red que corta el 8200, como la de la escuela, entra por el túnel de SSM que explica [`ansible/README.md`](../ansible/README.md) y usa `VAULT_ADDR=https://localhost:8200`.

## Lo que hay en Vault

- **El motor `boutique/`**, de tipo KV versión 2, con dos secretos:
  - `boutique/kubeconfig`: el kubeconfig del clúster, en el campo `kubeconfig`.
  - `boutique/redis-orders`: la contraseña de `redis-orders` (#79), en el campo `password`. La genera `configurar.sh` la primera vez y no la vuelve a cambiar.
- **La bitácora de auditoría** en `/var/log/vault/audit.log`, dentro de la máquina de Vault. La carpeta la crea Ansible, porque Vault corre con su propio usuario y no puede escribir directo en `/var/log`.
- **Dos políticas**, `pipeline` y `respaldo`.
- **Dos tokens**, los dos huérfanos (abajo explico por qué).

La llave está partida en 3 pedazos y hacen falta 2 para abrir Vault: yo tengo 2 y Alessia 1. **El token raíz se revocó** después de configurar todo, el 11 de octubre de 2026.

## Abrir Vault

Vault despierta sellado cada vez que se enciende su máquina, o sea, cada vez que se inicia el lab. Mientras esté sellado no responde a nadie, y el pipeline falla en el paso de Vault (#80).

```bash
vault status
```

Si dice `Sealed true`, ábrelo con 2 pedazos:

```bash
vault operator unseal
```

Pide el pedazo oculto, así que no queda en el historial. Se corre dos veces, con un pedazo distinto cada vez. Cuando `vault status` diga `Sealed false`, ya está.

También se puede desde la interfaz web: al entrar a `https://98.94.123.92:8200/ui` con Vault sellado, la página pide los pedazos. Así es como lo abre Alessia, que no necesita el CLI.

## Las políticas

Una política es una lista de rutas y de lo que se puede hacer en cada una. Todo lo que no está en la lista está prohibido.

- **`pipeline`** solo deja leer (`read`) `boutique/data/kubeconfig` y `boutique/data/redis-orders`. No puede escribir, borrar, listar ni ver otros secretos. Ojo con el `data/`: KV versión 2 guarda los valores bajo `boutique/data/` y el historial bajo `boutique/metadata/`. Por fuera se escribe `vault kv get boutique/redis-orders`, pero la política tiene que nombrar la ruta real.
- **`respaldo`** solo deja leer `sys/storage/raft/snapshot`, que es sacar una foto de Vault. No puede leer secretos ni restaurar una foto.

## Los tokens

| Token | Política | Dura | Dónde vive |
|---|---|---|---|
| Del pipeline | `pipeline`, sin `default` | 7 días | Secreto `VAULT_TOKEN` del Environment `aws-k3s` en GitHub |
| De respaldo | `respaldo` y `default` | 32 días | El gestor de contraseñas |

**Los dos son huérfanos** (`-orphan`). En Vault, un token creado por otro es su "hijo", y al revocar al padre se revocan todos sus hijos. Si se hubieran creado como hijos del token raíz, revocar el raíz se los habría llevado.

**El del pipeline no lleva la política `default`**, que da permisos sobre el propio token, como consultarse o renovarse. El pipeline no los necesita.

**Para crear o cambiar un token hace falta un token raíz**, que ya no existe. Hay que generar uno nuevo con los pedazos (abajo), hacer el cambio y volverlo a revocar. Es el precio de no dejar un raíz vivo, y de no usar el método JWT (ADR 0011, pieza 3).

### Renovar el token del pipeline

El primero se creó el 11 de octubre de 2026 y vence el 18. Cuando venza, el pipeline va a fallar en el paso de Vault. Con un token raíz nuevo y la sesión abierta (`vault login`):

```bash
vault token create -orphan -no-default-policy -policy=pipeline -ttl=168h \
  -display-name=pipeline -field=token \
  | gh secret set VAULT_TOKEN --env aws-k3s -R valentinodepaola/microservices-demo
```

El token va directo de Vault a GitHub sin imprimirse. Si el anterior todavía no vencía, revócalo por su *accessor*, un identificador público que sirve para revocar sin conocer el token:

```bash
vault list -format=json auth/token/accessors | jq -r '.[]' | while read a; do
  vault token lookup -format=json -accessor "$a" \
    | jq -r 'select(.data.display_name=="token-pipeline") | "\(.data.accessor) \(.data.creation_time)"'
done
```

Muestra los tokens del pipeline con su fecha de creación. Revoca el más viejo con `vault token revoke -accessor <accessor>`.

### Sacar una foto de Vault

Antes de recrear la máquina de Vault (ADR 0011, pieza 7). Copia el token de respaldo desde el gestor de contraseñas y, sin pegarlo en la terminal:

```bash
VAULT_TOKEN=$(pbpaste) vault operator raft snapshot save vault.snap
```

`pbpaste` lo toma del portapapeles, así que el token no queda en el historial.

La foto sale cifrada con la llave de Vault: sin 2 pedazos no se puede leer nada de adentro. Aun así, se guarda en la Mac, nunca en el repo.

## Generar un token raíz nuevo

Hace falta para correr `configurar.sh`, crear tokens o cambiar un secreto. Se necesitan 2 pedazos de la llave.

Desde Vault 2.0 esto también pide un token válido (CVE-2026-5807), y como el raíz está revocado no tendríamos ninguno que sirva. Por eso `vault.hcl` trae `enable_unauthenticated_access = ["generate-root"]`, que lo deja funcionar solo con los pedazos (#95). Lo que se cede es que alguien en internet podría empezar intentos falsos y estorbar el nuestro; si pasa, se cancela y se vuelve a empezar. Lo que no se cede es lo importante: sin 2 pedazos nadie genera nada.

```bash
vault operator generate-root -init
```

Imprime un `Nonce` y un `OTP`. Copia el `OTP` a un lugar temporal: se usa al final para descifrar el token. Luego, dos veces, con un pedazo distinto cada vez (lo pide oculto):

```bash
vault operator generate-root
```

La segunda vez imprime un `Encoded Token`. Descífralo con el `OTP`:

```bash
vault operator generate-root -decode=<Encoded Token> -otp=<OTP>
```

Eso imprime el token raíz. Inicia sesión con él (`vault login`, que también lo pide oculto), haz lo que necesites y **revócalo al terminar**:

```bash
vault token revoke -self
rm ~/.vault-token
```

Si alguien empezó un `generate-root` y no lo terminó, se cancela con `vault operator generate-root -cancel`. También funciona sin token.

Si `generate-root -init` contesta `permission denied`, Vault no tiene la opción encendida: corre el playbook de Ansible (`./ansible/ansible.sh ansible-playbook vault.yml`) y vuelve a abrir Vault, porque el playbook lo reinicia.

## Configurar Vault: `configurar.sh`

Se corre después de inicializar Vault en una máquina nueva, o cuando cambia el kubeconfig (por ejemplo, si se recreó el nodo de k3s). Necesita la sesión con un token raíz:

```bash
vault login
./vault/configurar.sh
```

Cada parte revisa primero si ya está hecha:

- **El motor `boutique/` y la bitácora** solo se encienden si no existen.
- **Las políticas** se escriben siempre: escribir una política reemplaza la anterior, así que repetirlo no duplica nada y aplica cualquier cambio a los `.hcl`.
- **El kubeconfig** solo se carga si cambió, para no sumar versiones iguales. Lo lee de `~/.kube/boutique-k3s.yaml`, o de la ruta que pongas en `KUBECONFIG_ARCHIVO`.
- **La contraseña de `redis-orders`** solo se genera si no existe. Si se generara en cada corrida, Redis y Vault quedarían con contraseñas distintas. Va de `openssl rand -hex 24` directo a Vault por la entrada estándar, sin pasar por la pantalla.

La segunda corrida tiene que decir "ya existe" o "ya está al día" en todo, salvo en las dos políticas.

## La bitácora

Cada petición a Vault queda anotada: quién la hizo, a qué ruta y si se le negó. Los valores de los secretos no aparecen en claro: Vault los guarda cifrados (HMAC). Para ver cuántas veces se leyeron los secretos y la última petición:

```bash
./ansible/ansible.sh ansible vault -b -m ansible.builtin.shell -a "grep -c boutique/data /var/log/vault/audit.log; tail -n 1 /var/log/vault/audit.log | jq '{ruta: .request.path, token: .auth.display_name, error: .error}'"
```

Los intentos rechazados también quedan: el 11 de octubre, el token del pipeline intentó listar los motores (`sys/mounts`) y la bitácora lo registró con `permission denied`.

## Inicializar Vault en una máquina nueva

Solo si se recrea la máquina de Vault y no hay foto que restaurar (ADR 0011, pieza 7). Después de correr el playbook de Ansible:

1. `vault operator init -key-shares=3 -key-threshold=2`. Imprime los 3 pedazos y el token raíz **una sola vez**: guárdalos en ese momento en el gestor de contraseñas, y limpia la terminal con Cmd+K.
2. `vault operator unseal` dos veces.
3. `vault login` con el token raíz y `./vault/configurar.sh`. La contraseña de `redis-orders` va a ser nueva: actualiza el Secret del clúster y reinicia Redis y `orderservice`, como explica el [README del componente](../kustomize/components/order-tracking/README.md#la-contraseña-de-redis-orders) (#79).
4. Crear los dos tokens (arriba) y mandar el del pipeline a GitHub.
5. Revocar el token raíz.
