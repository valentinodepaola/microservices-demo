# Ansible: instalar Vault en su máquina

Este playbook deja HashiCorp Vault instalado, configurado y corriendo en la máquina que crea `terraform/aws/vault.tf` (#76), con su interfaz web por HTTPS. Se puede correr las veces que sea: si la máquina ya está bien, no cambia nada, y si está vacía, la deja completa. Las decisiones de diseño están en el [ADR 0011](../docs/adr/0011-secretos-del-pipeline-en-vault.md).

No inicializa Vault: eso genera los pedazos de la llave y el token raíz, y no deben pasar por Ansible ni por su bucket. Ese paso es de #78.

## Qué hace

| Rol | Qué deja en la máquina |
|---|---|
| `base` | La lista de `apt` al día y los paquetes que usan los demás roles |
| `vault_install` | El repositorio oficial de HashiCorp y Vault en la versión fija de `roles/vault_install/defaults/main.yml` (hoy la `2.1.1`) |
| `vault_tls` | La llave y el certificado de Vault en `/opt/vault/tls/`, firmados por nuestra autoridad propia |
| `vault_config` | `/etc/vault.d/vault.hcl` con la UI, `raft` y el listener por HTTPS, y el servicio encendido y habilitado |

Vault solo se reinicia si cambia su configuración o su certificado.

## Requisitos

- **Docker Desktop encendido.** Ansible corre dentro de un contenedor, no directo en la Mac (abajo explico por qué). Solo hace falta mientras corres el playbook.
- **La infraestructura levantada** con Terraform, incluido el bucket de Ansible ([`terraform/aws/README.md`](../terraform/aws/README.md)).
- **Las credenciales de la sesión del lab** en `~/.aws/credentials`. Caducan en cada sesión, así que hay que pegarlas de nuevo cada vez que se inicia el lab.
- **La máquina de Vault encendida.** El inventario solo busca máquinas en estado `running`.

No hace falta instalar Ansible ni el `session-manager-plugin` en la Mac: vienen en la imagen.

## Correrlo

Desde la raíz del repo:

```bash
./ansible/ansible.sh ansible-playbook vault.yml
```

La primera vez sobre una máquina vacía tarda un par de minutos. Para comprobar que es idempotente, córrelo otra vez: el resumen final tiene que decir `changed=0`.

`ansible.sh` hace lo mismo con cualquier comando de Ansible. Por ejemplo, para ver si Ansible llega a la máquina:

```bash
./ansible/ansible.sh ansible vault -m ansible.builtin.ping
```

## Cómo entra a la máquina

Por SSM, sin SSH: la máquina no tiene el puerto 22 abierto. El inventario (`inventory/aws_ec2.yml`) le pregunta a AWS cuál es la máquina con la etiqueta `Name = boutique-vault`, así que si Terraform la recrea y cambia su id, no hay que editar nada. La conexión está en `inventory/group_vars/vault.yml`, y el nombre del bucket lo saca `ansible.sh` de `terraform output`, para no escribir el número de cuenta en el repo.

## Por qué corre en Docker

En mi Mac, Ansible truena con `A worker was found in a dead state` y una alerta de "Python se cerró". Ansible crea sus procesos de trabajo con `fork()`, y en esta versión de macOS, si el proceso principal ya usó la red, el proceso hijo truena en cuanto intenta usarla. Lo reproduje fuera de Ansible con un script de cinco líneas, incluso con el Python que trae macOS. Las variables que suelen recomendarse (`OBJC_DISABLE_INITIALIZE_FORK_SAFETY`, `no_proxy`) no lo arreglan.

En Linux no pasa, así que el `Dockerfile` arma un contenedor con Ansible, sus librerías de AWS, las colecciones de `requirements.yml` y el plugin de SSM, todo con versión fija. `ansible.sh` lo reconstruye si cambió algo y monta tres carpetas:

| En la Mac | En el contenedor | Para qué |
|---|---|---|
| `ansible/` | `/ansible` | El playbook. Lo que edites se ve al instante |
| `~/.aws` | `/root/.aws` (solo lectura) | Las credenciales del lab |
| `~/.vault-boutique` | `/root/.vault-boutique` | La llave de la autoridad propia |

## La autoridad propia

El HTTPS de Vault usa una autoridad certificadora nuestra (ADR 0011, pieza 5). La primera corrida la crea:

- **`~/.vault-boutique/ca.key`**: la llave de la autoridad. Se queda en mi Mac, fuera del repo. Conviene guardar una copia en el gestor de contraseñas.
- **`ansible/vault-ca.crt`**: el certificado raíz. Es público y va en el repo.

La llave de Vault nace en su propia máquina y nunca sale de ahí. A la Mac solo viaja la solicitud de certificado, y de regreso el certificado firmado. Ninguno de los dos es secreto, así que por el bucket de SSM no pasa ninguna llave.

Si se recrea la máquina, se genera una llave de Vault nueva y se firma otra vez con la misma autoridad. `vault-ca.crt` no cambia.

**Si se pierde `ca.key`**, la siguiente corrida crea una autoridad nueva y cambia `vault-ca.crt`. Hay que subir el archivo nuevo al repo, volver a confiar en él en el llavero y cambiarlo en el pipeline (#80).

El certificado de Vault dura un año. Para renovarlo basta con correr el playbook de nuevo antes de que venza.

## Comprobar que quedó bien

```bash
curl --cacert ansible/vault-ca.crt https://$(terraform -chdir=terraform/aws output -raw vault_public_ip):8200/v1/sys/health
```

Mientras no esté inicializado, Vault responde `501` con `"initialized":false`, que es lo esperado. Sin `--cacert`, `curl` rechaza el certificado: así se ve que lo firma nuestra autoridad y no una pública.

Para abrir la interfaz en el navegador con candado, primero hay que confiar en la autoridad. En la Mac se agrega al llavero de inicio de sesión (pide tu contraseña):

```bash
security add-trusted-cert -r trustRoot -k ~/Library/Keychains/login.keychain-db ansible/vault-ca.crt
```

Y luego se abre `terraform -chdir=terraform/aws output -raw vault_url` seguido de `/ui`. Firefox usa su propio almacén de certificados, así que ahí hay que importarlo aparte.

**Si la red corta el 8200.** En algunas redes (me pasó en la de la escuela), la conexión al 8200 se abre pero se corta en cuanto empieza el HTTPS, y lo mismo con el 6443 del clúster. Vault está bien, es la red. Se puede entrar por un túnel de SSM, que va por el 443:

```bash
aws ssm start-session --target $(terraform -chdir=terraform/aws output -raw vault_instance_id) \
  --document-name AWS-StartPortForwardingSession \
  --parameters '{"portNumber":["8200"],"localPortNumber":["8200"]}'
```

Con el túnel abierto, Vault queda en `https://localhost:8200`. El certificado incluye `localhost`, así que el candado sigue saliendo.

## Recrear la máquina desde cero

```bash
terraform -chdir=terraform/aws apply -replace=aws_instance.vault
```

Cuando la máquina nueva aparezca en SSM (un par de minutos), se corre el playbook igual que siempre. La IP elástica no cambia, así que el certificado nuevo lleva la misma IP. Lo probé el 6 de octubre de 2026: la máquina vacía quedó con Vault corriendo en una sola corrida (`changed=8`) y la segunda dio `changed=0`.

**Ojo:** una vez inicializado Vault (#78), recrear la máquina borra sus datos. Antes hay que sacar una foto con `vault operator raft snapshot save` (ADR 0011, pieza 7). Y cualquier corrida que lo reinicie, por cambiar la configuración o renovar el certificado, lo deja sellado: hay que volver a abrirlo.
