# Operar el clúster de AWS

Cómo levantar el clúster, cómo saber si está encendido, qué hacer cuando no responde y cómo apagarlo al terminar. Escrito el 20 de septiembre de 2026, con el issue #37.

Aquí solo están los pasos. Por qué se decidió cada cosa está en el [ADR 0010](adr/0010-fase-b-en-aws-con-k3s-sobre-ec2.md), cómo funciona el pipeline en [despliegue-continuo.md](despliegue-continuo.md) y qué crea Terraform en [`terraform/aws/README.md`](../terraform/aws/README.md).

---

## Git y AWS son cosas separadas

Esto es lo primero que hay que tener claro, porque de aquí salen casi todas las dudas.

| | Git y GitHub | AWS |
|---|---|---|
| Qué es | Ramas, commits, pull requests | Una máquina encendida en Virginia |
| Cuándo lo tocas | Todo el tiempo | Al empezar y al terminar el día |
| Crear una rama | sí | no pasa nada |
| Abrir un PR | sí | no pasa nada |
| Hacer merge a `main` | sí | aquí sí: el pipeline despliega |

Puedes crear ramas, hacer commits y abrir PRs con el lab cerrado. No hace falta correr Terraform ni encender nada. Lo único que necesita el clúster encendido es el merge a `main`, porque ahí corre `cd-main.yaml`.

---

## Cómo funciona el despliegue

```
1. Rama nueva, commits, PR                      → AWS no se entera
2. Alguien aprueba el PR y hace merge a main    → arranca cd-main.yaml
3. Job "pruebas"    (máquina de GitHub)         → Go y C#
4. Job "imagenes"   (máquina de GitHub)         → construye las 12 y las sube a ghcr.io
                                                   + sube build.json como artefacto
5. Job "deploy"     (máquina de GitHub)
   ├─ monta el kubeconfig desde el secreto
   ├─ ¿el clúster responde?  ──── NO ──→ falla en unos segundos: "el laboratorio está detenido"
   ├─ skaffold deploy (no construye: baja de ghcr.io)
   ├─ espera a los 12 Deployments
   ├─ smoke test: 50 peticiones, 0 errores
   └─ ✅  |  ❌ → rollback automático a la versión anterior
```

Nadie tiene que aprobar el despliegue ni correr comandos. Lo único que se necesita es que la instancia de EC2 esté encendida.

### Lo que tiene que saber el equipo

Los merges de cualquiera se despliegan solos, también los de Alessia. Al pipeline no le importa quién hizo el merge.

Pero el clúster solo lo puedo encender yo, porque el lab de AWS Academy está en mi cuenta de Tecmilenio. Si alguien hace merge con el lab cerrado:

- El job `deploy` falla en unos segundos con un mensaje que dice que la instancia está detenida.
- No se rompe nada. El clúster no está encendido, así que no queda nada a medias.
- Las imágenes sí se publican en `ghcr.io`, eso lo hace el job anterior.
- Para arreglarlo, enciendo el lab y le doy **Re-run failed jobs** a esa ejecución. No hace falta un commit nuevo.

O sea: el despliegue es automático, pero que el clúster esté encendido depende de mí.

---

## Empezar el día

### 1. Levantar el lab y cambiar las llaves

Las llaves de AWS Academy duran lo que dura la sesión. Traen un `aws_session_token` que deja de servir cuando la sesión se cierra. Cada vez que le das a *Start Lab* te da llaves nuevas, pero se quedan en la página, no se guardan solas en tu computadora. Por eso hay que copiarlas cada vez.

1. En el lab, dale a *Start Lab* y espera a que el círculo se ponga verde.
2. Ve a *AWS Details* → *AWS CLI* → *Show* y copia todo el bloque (⌘C).
3. En la terminal:

```bash
pbpaste > ~/.aws/credentials && chmod 600 ~/.aws/credentials
```

`pbpaste` pega lo que copiaste directo en el archivo y borra lo que había antes, que es lo que queremos.

> **Cuidado:** si pegas las llaves nuevas debajo de las viejas, el archivo queda con dos bloques `[default]` y el CLI lee el primero, que es el viejo. Vas a seguir viendo `InvalidClientTokenId` aunque ya pegaste las nuevas. Por eso el comando usa `>` y no `>>`.

Si prefieres hacerlo a mano, abre el archivo con `open -a TextEdit ~/.aws/credentials`, borra todo, pega las llaves nuevas y guarda con ⌘S.

Para revisar que funcionen:

```bash
aws sts get-caller-identity
```

Si te regresa un JSON con tu `Arn`, ya quedaron. Si dice `InvalidClientTokenId`, no se pegaron bien.

### 2. Levantar el clúster

Depende de cómo lo dejaste la última vez. Para saberlo:

```bash
aws ec2 describe-instances --filters "Name=tag:Name,Values=boutique-k3s" \
  --query 'Reservations[].Instances[].State.Name' --output text
```

| Si dice | Qué hacer |
|---|---|
| `running` | Nada, ya está encendida. Normalmente el lab la enciende solo cuando abres la sesión |
| `stopped` | [Encenderla](#si-la-instancia-está-detenida) |
| nada, o `terminated` | [Crearla otra vez con Terraform](#si-la-instancia-no-existe) |

---

### Si la instancia está detenida

```bash
cd terraform/aws
aws ec2 start-instances --instance-ids $(terraform output -raw instance_id)
```

Espera un minuto y revisa:

```bash
kubectl --kubeconfig ~/.kube/boutique-k3s.yaml get nodes
```

Y ya, no hay que hacer nada más. k3s arranca solo con la máquina, la IP elástica sigue siendo la misma, el kubeconfig sigue sirviendo y la tienda sigue desplegada. Los doce pods vuelven a levantarse con las imágenes que ya estaban en el disco.

### Si la instancia no existe

```bash
cd terraform/aws
terraform plan -out=tfplan
terraform apply tfplan
```

El plan tiene que decir `12 to add, 0 to change, 0 to destroy`. Si sale otro número, no apliques y revisa primero qué está pasando.

Tarda como dos minutos.

> Que diga `Apply complete` no quiere decir que el clúster ya esté listo. Terraform solo asegura que la máquina se creó y arrancó. k3s se instala adentro con el `user_data` y tarda como un minuto más. Si intentas conectarte antes, te va a salir `connection refused` aunque todo esté bien.

Luego saca el kubeconfig:

```bash
terraform output -raw kubeconfig_comando | bash
kubectl --kubeconfig ~/.kube/boutique-k3s.yaml get nodes -o wide
```

Tiene que salir un nodo `Ready` con `ARCH` en `amd64`.

**Este paso es fácil de olvidar:** cuando se crea una instancia nueva, k3s genera certificados nuevos. El kubeconfig que está guardado en el secreto de GitHub ya no sirve, porque es de la instancia anterior. Hay que actualizarlo:

```bash
base64 -i ~/.kube/boutique-k3s.yaml | gh secret set KUBECONFIG_K3S \
  -R valentinodepaola/microservices-demo --env aws-k3s
```

> Si no lo actualizas, el job `deploy` pasa la revisión del clúster (el clúster sí responde) pero después falla con un error de TLS. Si ves un error de TLS, casi seguro es esto.

Por último, la tienda no está desplegada porque la instancia es nueva y está vacía. Se despliega sola con el siguiente merge a `main`, o la puedes desplegar a mano:

```bash
export KUBECONFIG=~/.kube/boutique-k3s.yaml
SHA=$(git rev-parse origin/main)
# build.json con las doce imágenes publicadas para ese commit
python3 - "$SHA" <<'PY' > /tmp/build.json
import json, sys
n = ['emailservice','productcatalogservice','recommendationservice','shoppingassistantservice',
     'shippingservice','checkoutservice','paymentservice','currencyservice','cartservice',
     'frontend','adservice','loadgenerator']
print(json.dumps({'builds': [{'imageName': x, 'tag': f'ghcr.io/valentinodepaola/{x}:{sys.argv[1]}'} for x in n]}))
PY
skaffold deploy --build-artifacts=/tmp/build.json
```

Es el mismo comando que corre el workflow.

---

## Terminar el día

Hay que detener la instancia, no destruirla.

```bash
cd terraform/aws
aws ec2 stop-instances --instance-ids $(terraform output -raw instance_id)
```

### Por qué detenerla y no destruirla

| | Costo por día | Qué se queda |
|---|---|---|
| Encendida todo el día | ~2.40 USD | todo |
| Detenida | ~0.20 USD | disco, IP elástica, kubeconfig y la tienda desplegada |
| Destruida | 0 | nada |

*(Son precios públicos aproximados: `m5.large` ~0.096 USD/h, disco gp3 de 30 GB ~0.08 USD/día, IP pública ~0.12 USD/día. No los pude revisar contra AWS porque el lab bloquea la API de precios.)*

Detenida, el crédito de 50 USD alcanza para meses. Y para volver solo necesitas un comando y esperar un minuto, sin actualizar el secreto ni volver a desplegar.

Si la destruyes, cada vez que vuelvas tienes que correr `terraform apply`, actualizar `KUBECONFIG_K3S` y volver a desplegar la tienda. Por 20 centavos al día no vale la pena.

> **Ojo:** el lab enciende solo las instancias detenidas cuando abres la sesión. Si entras al lab para otra cosa, la instancia se enciende y empieza a gastar. Si no vas a usar el clúster, detenla otra vez.

### Cuándo sí destruirla

- Cuando termine el proyecto, después de la entrega y la demo.
- Si el crédito se está acabando. Si se acaba, la cuenta se desactiva y se borra todo, también el bucket de S3 donde está el estado de Terraform.

```bash
cd terraform/aws && terraform destroy
```

Cuando la destruyas, déjalo anotado en el issue con la fecha. El historial de versiones del estado en S3 sirve como evidencia de la infraestructura como código.

> **Nunca le des al botón *Reset* del lab.** Borra toda la cuenta, también el bucket con el estado de Terraform.

---

## Revisar si la instancia está encendida

Aquí hay un problema: para preguntarle a AWS tienes que abrir el lab, y al abrir el lab la instancia se enciende sola. O sea, al revisar ya la encendiste. Me pasó el 20/09: corrí `start-instances` y me respondió `PreviousState: running`, porque el lab ya la había encendido al abrir la sesión.

Por eso es mejor preguntarle directo a la máquina con `curl`, usando la IP elástica. No necesitas llaves y no enciendes nada:

```bash
curl -s -o /dev/null -w '%{http_code}\n' --max-time 10 http://3.94.38.103/
```

| Si responde | Quiere decir |
|---|---|
| `200` | Está encendida y la tienda funciona. Está gastando |
| `000` | No respondió nada en 10 segundos. Está apagada |
| otro número | Está encendida pero la tienda tiene un problema. Revisa los [síntomas](#síntomas-y-qué-significan) |

`000` no es un código de HTTP. Es `curl` diciendo que nadie contestó. Si la máquina estuviera encendida contestaría algo, aunque fuera un error.

Si la IP elástica cambió (solo pasa si destruiste la infraestructura), la nueva la sacas con `terraform output public_ip`.

### Revisarlo con AWS, si el lab ya está abierto

Si ya abriste la sesión y tus llaves funcionan, AWS te dice exactamente cómo está:

```bash
aws ec2 describe-instances --filters "Name=tag:Name,Values=boutique-k3s" \
  --query 'Reservations[].Instances[].State.Name' --output text
```

Te va a decir `running`, `stopped` o `stopping`. Si abriste el lab solo para revisar, acuérdate de que ya la encendiste y [detenla](#terminar-el-día).

### Qué pasa cuando se acaba la sesión del lab

El lab tiene un tiempo límite que sigue corriendo aunque cierres la pestaña de Chrome. Cuando se acaba, AWS Academy hace dos cosas:

1. **Detiene la instancia.** No la borra: el disco, la IP elástica y la tienda se quedan.
2. **Bloquea las llaves.** No las borra, les pone encima una política que niega todo, `voc-cancel-cred`. Tu archivo `~/.aws/credentials` se queda igual, pero cualquier cosa que pidas te la va a rechazar.

Lo comprobé el 20/09/2026: el despliegue del merge del PR #63 corrió a las 22:40 UTC con la instancia detenida, y la máquina volvió a arrancar a las 22:42, cuando volví a abrir el lab. El workflow detectó en 40 segundos que el clúster no respondía, y lo arreglé con *Re-run failed jobs*.

## Revisar que todo funcione

```bash
export KUBECONFIG=~/.kube/boutique-k3s.yaml

kubectl get nodes                    # un nodo Ready
kubectl get pods                     # doce Running
curl -I http://$(kubectl config view --minify \
  -o jsonpath='{.clusters[0].cluster.server}' | sed 's|https://||; s|:.*||')
```

El `curl` tiene que regresar `HTTP/1.1 200 OK`. La IP la saco del kubeconfig y no de `kubectl get svc`, porque ese regresa la IP privada del nodo. La instancia no sabe que tiene una IP elástica, esa traducción la hace AWS por fuera.

### Síntomas y qué significan

| Qué ves | Qué pasa |
|---|---|
| `InvalidClientTokenId` | Las llaves ya no sirven. [Cámbialas](#1-levantar-el-lab-y-cambiar-las-llaves) |
| `RequestExpired` | Se acabó la sesión del lab. Seguramente la instancia está detenida. [Cambia las llaves](#1-levantar-el-lab-y-cambiar-las-llaves) |
| `explicit deny` … `policy/voc-cancel-cred` | Lo mismo: AWS Academy bloqueó las llaves al cerrar la sesión. [Cámbialas](#1-levantar-el-lab-y-cambiar-las-llaves) |
| `curl` regresa `000` | La instancia está apagada. Ve [cómo revisarlo](#revisar-si-la-instancia-está-encendida) |
| El job falla en `Comprobar que el cluster responde` | La instancia está detenida. Enciéndela y dale **Re-run failed jobs** |
| Error de TLS al conectar | El secreto `KUBECONFIG_K3S` es de una instancia anterior. Actualízalo |
| Un pod en `Pending` con `Insufficient cpu` | El límite de 2 vCPU del lab. Ve [despliegue-continuo.md](despliegue-continuo.md#qué-revisar-primero-cuando-falla) |
| `emailservice` o `recommendationservice` con 1 reinicio | Es normal cuando arrancan los doce al mismo tiempo. Se arregla solo |
| Todos en `ImagePullBackOff` | Alguna imagen de `ghcr.io` dejó de ser pública |

---

## Comandos rápidos

```bash
# ¿Funcionan las llaves?
aws sts get-caller-identity

# ¿Está encendida? Sin llaves y sin encenderla (200 = sí, 000 = no)
curl -s -o /dev/null -w '%{http_code}\n' --max-time 10 http://3.94.38.103/

# ¿Cómo está la instancia? (necesita el lab abierto)
aws ec2 describe-instances --filters "Name=tag:Name,Values=boutique-k3s" \
  --query 'Reservations[].Instances[].State.Name' --output text

# Encender / detener
cd terraform/aws
aws ec2 start-instances --instance-ids $(terraform output -raw instance_id)
aws ec2 stop-instances  --instance-ids $(terraform output -raw instance_id)

# ¿Dónde está la tienda?
cd terraform/aws && terraform output tienda_url

# Entrar a la máquina (sin SSH)
aws ssm start-session --target $(terraform output -raw instance_id)

# Actualizar el secreto (solo si creaste la instancia de nuevo)
base64 -i ~/.kube/boutique-k3s.yaml | gh secret set KUBECONFIG_K3S \
  -R valentinodepaola/microservices-demo --env aws-k3s
```

## Otras páginas

- [despliegue-continuo.md](despliegue-continuo.md): cómo funciona `cd-main.yaml` y qué revisar cuando falla
- [`terraform/aws/README.md`](../terraform/aws/README.md): qué crea el módulo y por qué
- [ADR 0010](adr/0010-fase-b-en-aws-con-k3s-sobre-ec2.md): por qué AWS, por qué k3s y no EKS, y lo que cuesta esa decisión
