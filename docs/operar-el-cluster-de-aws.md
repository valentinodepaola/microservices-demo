# Operar el clúster de AWS

Qué hacer para que la tienda esté arriba, qué hacer cuando no lo está, y qué apagar al terminar. Escrito el 20 de septiembre de 2026 · issue #37.

Esta página es **operativa**. El porqué de cada decisión está en el [ADR 0010](adr/0010-fase-b-en-aws-con-k3s-sobre-ec2.md), el pipeline en [despliegue-continuo.md](despliegue-continuo.md) y la infraestructura en [`terraform/aws/README.md`](../terraform/aws/README.md).

---

## Lo primero: dos mundos que no se tocan

Confundirlos es la fuente de casi todas las dudas.

| | Git y GitHub | AWS |
|---|---|---|
| Qué es | Ramas, commits, pull requests | Una máquina encendida en Virginia |
| Cuándo se toca | Todo el tiempo | Solo al empezar y terminar la jornada |
| Crear una rama | sí | **no pasa nada en AWS** |
| Abrir un PR | sí | **no pasa nada en AWS** |
| Integrar a `main` | sí | **acá sí**: el pipeline despliega |

**Crear ramas, commitear y abrir PRs no requiere tocar Terraform ni encender nada.** Se pueden abrir cincuenta ramas con el laboratorio cerrado. Lo único que necesita el clúster vivo es el **merge a `main`**, porque ahí corre `cd-main.yaml`.

---

## El flujo completo, de punta a punta

```
1. Rama nueva, commits, PR                      → AWS no se entera
2. Alguien aprueba el PR y lo integra a main    → arranca cd-main.yaml
3. Job "pruebas"    (máquina de GitHub)         → Go y C#
4. Job "imagenes"   (máquina de GitHub)         → construye las 12 y las sube a ghcr.io
                                                   + sube build.json como artefacto
5. Job "deploy"     (máquina de GitHub)
   ├─ monta el kubeconfig desde el secreto
   ├─ ¿el clúster responde?  ──── NO ──→ falla en 20 s: "el laboratorio está detenido"
   ├─ skaffold deploy (no construye: baja de ghcr.io)
   ├─ espera a los 12 Deployments
   ├─ smoke test: 50 peticiones, 0 errores
   └─ ✅  |  ❌ → rollback automático a la versión anterior
```

**Nadie aprueba nada y nadie corre un comando.** La única condición es que la instancia de EC2 esté encendida.

### Qué significa para el equipo

**Los merges de Alessia despliegan solos, igual que los tuyos** — el pipeline no distingue quién integró.

**Pero solo vos podés encender el clúster**, porque el laboratorio de AWS Academy está atado a tu cuenta institucional. Si alguien integra un PR con el laboratorio cerrado:

- El job `deploy` falla en veinte segundos con un mensaje que lo explica.
- **No se rompe nada**: el clúster no existe, así que no hay nada a medio aplicar.
- Las imágenes **sí quedaron publicadas** en `ghcr.io` por el job anterior.
- Para recuperarlo: encendés el laboratorio y le das **Re-run jobs** a esa ejecución. No hace falta un commit nuevo.

Conviene que el equipo lo sepa: el despliegue es automático, la disponibilidad del clúster no.

---

## Empezar la jornada

### 1. Encender el laboratorio y renovar las llaves

Las credenciales de AWS Academy son **temporales**: incluyen un `aws_session_token` que caduca al cerrar la sesión. Pulsar *Start Lab* genera un juego nuevo **en la página**, no en tu disco — por eso hay que copiarlas a mano cada vez.

1. En el laboratorio, *Start Lab*, y esperar el punto verde.
2. *AWS Details* → *AWS CLI* → *Show*. Copiar el bloque entero (⌘C).
3. En la terminal:

```bash
pbpaste > ~/.aws/credentials && chmod 600 ~/.aws/credentials
```

`pbpaste` vuelca el portapapeles al archivo **pisando lo que había**, que es justamente lo que hace falta.

> **El error más caro de esta página:** pegar las llaves nuevas *debajo* de las viejas. Si el archivo queda con dos bloques `[default]`, el CLI lee **el primero** —el caducado— y vas a seguir viendo `InvalidClientTokenId` con las credenciales nuevas ya pegadas. Por eso `>` y no `>>`.

Comprobar:

```bash
aws sts get-caller-identity
```

Si devuelve un JSON con tu `Arn`, las llaves andan. Si dice `InvalidClientTokenId`, no se pegaron bien.

### 2. Poner el clúster en pie

Depende de en qué estado lo dejaste. Averiguarlo:

```bash
aws ec2 describe-instances --filters "Name=tag:Name,Values=boutique-k3s" \
  --query 'Reservations[].Instances[].State.Name' --output text
```

| Respuesta | Qué hacer |
|---|---|
| `running` | Nada. Ya está arriba — el laboratorio suele reencenderla sola al abrir sesión |
| `stopped` | [Arrancarla](#caso-a-la-instancia-existe-y-está-detenida) |
| vacío, o `terminated` | [Recrearla con Terraform](#caso-b-la-instancia-no-existe) |

---

### Caso A: la instancia existe y está detenida

```bash
cd terraform/aws
aws ec2 start-instances --instance-ids $(terraform output -raw instance_id)
```

Esperar un minuto y comprobar:

```bash
kubectl --kubeconfig ~/.kube/boutique-k3s.yaml get nodes
```

**No hay que hacer nada más.** k3s arranca solo con la máquina, la IP elástica sigue asociada, el *kubeconfig* guardado sigue siendo válido y **la aplicación sigue desplegada** — los doce pods vuelven a levantar con las imágenes que ya están en el disco.

### Caso B: la instancia no existe

```bash
cd terraform/aws
terraform plan -out=tfplan
terraform apply tfplan
```

Tiene que decir **`12 to add, 0 to change, 0 to destroy`**. Si el número es otro, parar y revisar antes de aplicar.

Tarda unos dos minutos. Después:

> **`Apply complete` no significa que el clúster esté listo.** Terraform garantiza que la máquina existe y arrancó; k3s se instala adentro por `user_data` y tarda **alrededor de un minuto más**. Si le hablás antes, vas a ver `connection refused` y vas a creer que algo salió mal.

Sacar el *kubeconfig*:

```bash
terraform output -raw kubeconfig_comando | bash
kubectl --kubeconfig ~/.kube/boutique-k3s.yaml get nodes -o wide
```

Tiene que salir un nodo `Ready` con `ARCH` en **amd64**.

**Y acá viene el paso que es fácil olvidar.** Al recrear la instancia, k3s genera **certificados nuevos**: el *kubeconfig* que está guardado en el secreto de GitHub quedó apuntando a una cerradura que ya no existe. Hay que regenerarlo:

```bash
base64 -i ~/.kube/boutique-k3s.yaml | gh secret set KUBECONFIG_K3S \
  -R valentinodepaola/microservices-demo --env aws-k3s
```

> Si se salta este paso, el job `deploy` no falla en el preflight —el clúster responde— sino más adelante, con un error de **TLS**. Es el síntoma que delata un secreto viejo.

Por último, la aplicación **no está desplegada**: la instancia es nueva y está vacía. Vuelve con el siguiente merge a `main`, o a mano:

```bash
export KUBECONFIG=~/.kube/boutique-k3s.yaml
SHA=$(git rev-parse origin/main)
# build.json con los doce artefactos publicados para ese commit
python3 - "$SHA" <<'PY' > /tmp/build.json
import json, sys
n = ['emailservice','productcatalogservice','recommendationservice','shoppingassistantservice',
     'shippingservice','checkoutservice','paymentservice','currencyservice','cartservice',
     'frontend','adservice','loadgenerator']
print(json.dumps({'builds': [{'imageName': x, 'tag': f'ghcr.io/valentinodepaola/{x}:{sys.argv[1]}'} for x in n]}))
PY
skaffold deploy --build-artifacts=/tmp/build.json
```

Es el mismo comando exacto que corre el workflow.

---

## Terminar la jornada

**Detener la instancia, no destruirla.**

```bash
cd terraform/aws
aws ec2 stop-instances --instance-ids $(terraform output -raw instance_id)
```

### Por qué detener y no destruir

| | Costo por día | Qué conserva |
|---|---|---|
| Corriendo 24 h | ~2.40 USD | todo |
| **Detenida** | **~0.20 USD** | disco, IP elástica, *kubeconfig*, la app desplegada |
| Destruida | 0 | nada |

*(Tarifas públicas aproximadas: `m5.large` ~0.096 USD/h, EBS gp3 de 30 GB ~0.08 USD/día, IPv4 pública ~0.12 USD/día. La API de precios está bloqueada en el laboratorio, así que no se pudieron verificar contra AWS.)*

Detenida, el presupuesto de 50 USD aguanta meses. Y sobre todo: **volver es un comando y un minuto**, sin regenerar el secreto ni volver a desplegar.

Destruir obliga, cada vez, a `terraform apply` + regenerar `KUBECONFIG_K3S` + volver a desplegar la aplicación. Por veinte centavos al día, no compensa.

> **Ojo con lo que el laboratorio hace por su cuenta:** reenciende las instancias detenidas al abrir sesión. Si entrás al laboratorio por cualquier otro motivo, la instancia arranca y empieza a gastar. Si no vas a trabajar con el clúster, detenela otra vez.

### Cuándo sí destruir

- **Cuando el proyecto termine**, después de la entrega y la demostración.
- Si el crédito se acerca al límite. Agotarlo **desactiva la cuenta y borra todo**, incluido el bucket de S3 con el estado de Terraform.

```bash
cd terraform/aws && terraform destroy
```

Dejar constancia en el issue de cuándo se destruyó: el versionado del estado en S3 es evidencia del criterio de infraestructura como código.

> **No tocar nunca el botón *Reset* del laboratorio.** Borra la cuenta entera, incluido el bucket del estado de Terraform.

---

## Comprobar que todo está bien

```bash
export KUBECONFIG=~/.kube/boutique-k3s.yaml

kubectl get nodes                    # un nodo Ready
kubectl get pods                     # doce Running
curl -I http://$(kubectl config view --minify \
  -o jsonpath='{.clusters[0].cluster.server}' | sed 's|https://||; s|:.*||')
```

El `curl` tiene que devolver `HTTP/1.1 200 OK`. La dirección sale del *kubeconfig* y no de `kubectl get svc`: ese devuelve la IP **privada** del nodo, porque la instancia no sabe que tiene una IP elástica — la traducción la hace AWS en el borde.

### Síntomas y causas

| Qué ves | Qué es |
|---|---|
| `InvalidClientTokenId` | Las llaves caducaron. [Renovarlas](#1-encender-el-laboratorio-y-renovar-las-llaves) |
| El job muere en `Comprobar que el cluster responde` | La instancia está detenida. Arrancarla y **Re-run jobs** |
| Error de **TLS** al conectar | El secreto `KUBECONFIG_K3S` es de una instancia anterior. Regenerarlo |
| Un pod en `Pending` con `Insufficient cpu` | El techo de 2 vCPU del laboratorio. Ver [despliegue-continuo.md](despliegue-continuo.md#qué-revisar-primero-cuando-falla) |
| `emailservice` o `recommendationservice` con 1 reinicio | Esperable al arrancar los doce a la vez. Se estabiliza solo |
| Todos en `ImagePullBackOff` | Algún paquete de `ghcr.io` dejó de ser público |

---

## Referencia rápida

```bash
# ¿Las llaves andan?
aws sts get-caller-identity

# ¿En qué estado está la instancia?
aws ec2 describe-instances --filters "Name=tag:Name,Values=boutique-k3s" \
  --query 'Reservations[].Instances[].State.Name' --output text

# Arrancar / detener
cd terraform/aws
aws ec2 start-instances --instance-ids $(terraform output -raw instance_id)
aws ec2 stop-instances  --instance-ids $(terraform output -raw instance_id)

# ¿Dónde está la tienda?
cd terraform/aws && terraform output tienda_url

# Entrar a la máquina (sin SSH)
aws ssm start-session --target $(terraform output -raw instance_id)

# Regenerar el secreto (solo tras recrear la instancia)
base64 -i ~/.kube/boutique-k3s.yaml | gh secret set KUBECONFIG_K3S \
  -R valentinodepaola/microservices-demo --env aws-k3s
```

## Páginas relacionadas

- [despliegue-continuo.md](despliegue-continuo.md) — cómo funciona `cd-main.yaml` y qué revisar cuando falla
- [`terraform/aws/README.md`](../terraform/aws/README.md) — qué crea el módulo y por qué
- [ADR 0010](adr/0010-fase-b-en-aws-con-k3s-sobre-ec2.md) — por qué AWS, por qué k3s y no EKS, y qué se paga por ello
