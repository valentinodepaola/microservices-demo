# Infraestructura de la fase B — k3s sobre EC2 en AWS

Módulo de Terraform que crea el clúster de Kubernetes donde se despliega Online Boutique en la fase B del despliegue continuo, según el [ADR 0010](../../docs/adr/0010-fase-b-en-aws-con-k3s-sobre-ec2.md).

Desde la Fase 3 crea también **la máquina de Vault**, aparte del clúster, según el [ADR 0011](../../docs/adr/0011-secretos-del-pipeline-en-vault.md). Todo lo de Vault vive en [`vault.tf`](vault.tf). Terraform solo crea la máquina vacía; quien instala Vault adentro es Ansible.

> **Para el día a día —encender, apagar, renovar las llaves— usar [`docs/operar-el-cluster-de-aws.md`](../../docs/operar-el-cluster-de-aws.md).** Esta página explica qué crea el módulo y por qué; aquella dice qué comando correr y cuándo.

**Este módulo crea la infraestructura; no despliega la aplicación.** Terraform levanta el clúster vacío y produce el *kubeconfig*; quien despliega la tienda dentro es [`cd-main.yaml`](../../.github/workflows/cd-main.yaml). Esa separación es deliberada: el módulo heredado de Google, conservado en [`../gcp-heredado/`](../gcp-heredado/), mezclaba las dos cosas en un mismo `apply`, y con ello ni la infraestructura ni el despliegue se distinguían como piezas propias.

## Qué crea

| Recurso | Para qué |
|---|---|
| `aws_vpc` | La red privada del proyecto, `10.0.0.0/16` |
| `aws_subnet` | Subred pública `10.0.1.0/24`, con IP pública automática |
| `aws_internet_gateway` | La salida a internet de la VPC |
| `aws_route_table` + asociación | La ruta `0.0.0.0/0` hacia el gateway. Sin la asociación, la subred se queda colgando de la tabla principal y no tiene salida |
| `aws_security_group` | El firewall del nodo |
| 3 reglas de firewall | Entrada `6443` (API de Kubernetes) y `80` (la tienda); salida sin restricción |
| `aws_instance` | El nodo, `m5.large` con Ubuntu 24.04 `x86_64`, que instala k3s al arrancar |
| `aws_eip` + asociación | Dirección pública fija |

Y para Vault, en `vault.tf`:

| Recurso | Para qué |
|---|---|
| `aws_instance` | La máquina de Vault, `t3.small` con Ubuntu 24.04 y disco de 10 GB. Arranca limpia, sin `user_data` |
| `aws_eip` + asociación | Su dirección pública fija. Va dentro del certificado de Vault, así que no puede cambiar |
| `aws_security_group` | Su propio firewall, separado del del nodo |
| 2 reglas de firewall | Entrada `8200` (API e interfaz web de Vault); salida sin restricción. Sin 22 y sin 80 |
| `aws_s3_bucket_public_access_block` y `aws_s3_bucket_lifecycle_configuration` | Bloquean el acceso público al bucket de Ansible y borran lo que quede en él después de un día. El bucket en sí se crea a mano (ver abajo) |

La AMI no está fijada por identificador: se resuelve con un `data source` filtrando por Canonical y por `amd64`, porque el identificador cambia según la región y con cada publicación de imagen. Por eso las dos instancias tienen `ignore_changes = [ami]`: sin eso, cada imagen nueva de Canonical haría que el `plan` quisiera destruirlas y crearlas otra vez.

## Requisitos

- **Terraform ≥ 1.10** — el backend usa `use_lockfile`, que no existe antes de esa versión
- **AWS CLI**
- Credenciales vigentes del laboratorio de AWS Academy en `~/.aws/credentials`

> Las credenciales del laboratorio son temporales y **caducan al cerrar la sesión**. Cuando un comando responda `ExpiredToken`, hay que pulsar *Start Lab* y volver a pegarlas desde *AWS Details → AWS CLI*, reemplazando el archivo completo: si quedan dos bloques `[default]`, el CLI lee el primero, que es el caducado.

## Revisar que el lab este levantado

```bash
aws sts get-caller-identity
```

Si te dice que no se identifica entonces levanta el lab, vete a la parte de AWS Details y pega la llave con este comando 

```bash
open -a TextEdit ~/.aws/credentials
```
Este comando abrira la ventana con la llave, borra y pega la nueva y guardala con command S

## Preparación, por única vez: el bucket del estado

El estado de Terraform vive en S3. Ese bucket **se crea a mano**, una sola vez:

```bash
aws s3api create-bucket --bucket tfstate-boutique-614858348004 --region us-east-1
aws s3api put-bucket-versioning --bucket tfstate-boutique-614858348004 --versioning-configuration Status=Enabled
```

**Por qué es un paso manual y no un recurso más del módulo:** el bloque `backend` se lee durante el `terraform init`, antes de que Terraform pueda crear nada. Un módulo no puede construir el bucket donde guarda su propio estado. Las alternativas —un segundo módulo solo para el bucket, o crearlo con estado local y migrarlo después— mueven el problema en lugar de resolverlo.

El *versioning* no es opcional: conserva cada versión anterior del estado, que es lo que permite recuperarse si un `apply` lo corrompe.

## Preparación, por única vez: el bucket de Ansible

Ansible entra a la máquina de Vault por SSM, y su plugin necesita un bucket para pasarle archivos. Ese bucket **también se crea a mano**, antes del primer `apply`:

```bash
aws s3api create-bucket --bucket boutique-ansible-ssm-614858348004 --region us-east-1
```

**Por qué a mano:** el provider de AWS, cada vez que revisa un `aws_s3_bucket`, le pregunta por su configuración de *object lock*, y el lab niega ese permiso (`s3:GetBucketObjectLockConfiguration`) con una política de la organización. Terraform alcanza a crear el bucket, pero falla al leerlo, y en el siguiente `apply` intenta borrarlo y crearlo otra vez. Lo comprobé el 05/10: de las lecturas que hace el provider sobre un bucket, esa es la única bloqueada. Por eso Terraform solo maneja el bloqueo público y la regla de un día, que sí puede leer.

**Este va sin *versioning*, al revés que el del estado.** Los archivos que pasa Ansible pueden llevar secretos en texto plano, y con *versioning* las copias borradas se quedan para siempre. Por lo mismo no se reusa el bucket del estado.

`terraform destroy` tampoco borra este bucket. Con la regla de un día no se queda nada adentro.

Lo que Ansible instala en la máquina de Vault, y cómo se corre, está en [`ansible/README.md`](../../ansible/README.md).

## Levantar la infraestructura

```bash
cd terraform/aws
terraform init
terraform plan -out=tfplan
terraform apply tfplan
```

Guardar el plan y aplicar ese archivo garantiza que se ejecuta exactamente lo que se revisó. Con un plan guardado, `apply` no vuelve a pedir confirmación.

> **`Apply complete` no significa que el clúster esté listo.** Terraform garantiza que la máquina existe y arrancó; k3s se instala dentro, por `user_data`, y tarda alrededor de un minuto más. Comprobarlo con:
>
> ```bash
> aws ssm start-session --target $(terraform output -raw instance_id)
> # ya dentro:
> k3s kubectl get nodes
> ```

## Obtener el kubeconfig

El módulo publica el procedimiento completo como salida:

```bash
terraform output kubeconfig_comando
```

Extrae `/etc/rancher/k3s/k3s.yaml` de la instancia por SSM y sustituye la dirección interna `127.0.0.1` por la IP elástica. Ese reemplazo es necesario porque k3s escribe el archivo pensando en quien esté dentro del servidor; el certificado del API ya es válido para la dirección pública gracias a `--tls-san`.

Comprobación:

```bash
kubectl --kubeconfig ~/.kube/boutique-k3s.yaml get nodes -o wide
```

> **El kubeconfig es una credencial.** Contiene el certificado de cliente que autoriza como administrador del clúster. No se versiona ni se comparte; vive fuera del repositorio, en `~/.kube/`, con permisos `600`. Para el despliegue automático va a un *secret* del repositorio en base64, nunca a un archivo versionado.
>
> Esto es también lo que hace aceptable tener el puerto `6443` abierto a internet: los runners de GitHub Actions no tienen IP fija, así que no hay rango que restringir, pero el puerto abierto expone la cerradura, no la casa — sin este archivo no se pasa de la autenticación.

## Acceso a la máquina

**No hay acceso por SSH y el puerto 22 no está abierto.** Se entra por AWS Systems Manager, que el rol `LabRole` del laboratorio ya permite:

```bash
aws ssm start-session --target $(terraform output -raw instance_id)
```

A la máquina de Vault se entra igual:

```bash
aws ssm start-session --target $(terraform output -raw vault_instance_id)
```

No hay llave privada que custodiar ni puerto expuesto que atacar.

> **`start-session` necesita el Session Manager plugin en la Mac.** Sin él, el CLI responde `SessionManagerPlugin is not found`, aunque la máquina esté bien registrada. Se instala con `brew install --cask session-manager-plugin`. Ansible también lo necesita para entrar a Vault.

## Operación diaria y presupuesto

El presupuesto del laboratorio es de **50 USD**, y agotarlo **desactiva la cuenta y borra todos los recursos**. El dato que muestra el laboratorio proviene de AWS Budgets y se actualiza cada 8–12 horas, así que no refleja el consumo más reciente.

| Recurso | Cuándo cobra | Aproximado |
|---|---|---|
| Instancia `m5.large` | Solo encendida | ~0.10 USD/hora |
| Disco de 30 GB | Siempre, aunque esté detenida | ~2.40 USD/mes |
| IP elástica | Siempre, mientras esté reservada | ~3.60 USD/mes |
| Instancia `t3.small` de Vault | Solo encendida | ~0.02 USD/hora |
| Disco de 10 GB de Vault | Siempre, aunque esté detenida | ~0.80 USD/mes |
| IP elástica de Vault | Siempre, mientras esté reservada | ~3.60 USD/mes |

> **El laboratorio enciende solas las dos instancias** al iniciar cada sesión, aunque se hayan detenido antes. Conviene revisar al abrir:
>
> ```bash
> aws ec2 describe-instances --filters Name=instance-state-name,Values=running \
>   --query 'Reservations[].Instances[].[InstanceId,InstanceType,State.Name]' --output table
> ```

Detener al terminar la jornada:

```bash
aws ec2 stop-instances --instance-ids $(terraform output -raw instance_id) $(terraform output -raw vault_instance_id)
```

Detener conserva el disco, la IP elástica y, por tanto, la validez del *kubeconfig*: k3s vuelve a levantar solo al encender y la dirección no cambia.

## Destruir

```bash
terraform destroy
```

> **Capturar la evidencia antes.** El clúster respondiendo, los pods corriendo y la tienda accesible solo existen mientras la infraestructura está en pie; reconstruirlos para fotografiarlos cuesta tiempo y presupuesto.

`destroy` **no borra el bucket del estado ni el de Ansible**, porque no los creó este módulo. Eso es intencional: los buckets sobreviven a los ciclos de creación y destrucción de la infraestructura.

## Restricciones del entorno

El laboratorio de AWS Academy impone límites que condicionan el diseño. Verificados el 10 de septiembre de 2026 (ver el paso 0 del issue #44):

| Restricción | Consecuencia en este módulo |
|---|---|
| Máximo **2 vCPU** por instancia (tamaño `large`) | `m5.large` es el techo. `t3.xlarge` y `m5.xlarge` son rechazadas |
| Solo `us-east-1` y `us-west-2` | Otras regiones responden `UnauthorizedOperation` |
| No se pueden crear roles de IAM | Se usa el `LabInstanceProfile` preexistente |
| Las instancias se **detienen**, no se terminan, al cerrar sesión | La infraestructura sobrevive entre sesiones |
| Una instancia reiniciada **cambia de IP pública** | La IP elástica es obligatoria, no una comodidad |
| Imágenes de Online Boutique solo para `amd64` | La instancia es `x86_64`; una ARM dejaría los doce pods sin arrancar |

### Por qué `m5.large` y no `t3.large`

Ambas ofrecen 2 vCPU y 8 GiB. La familia `t3` es *burstable*: entrega una línea base —el 30 % en `t3.large`— y consume créditos por encima de ella. El `loadgenerator` de Online Boutique genera tráfico de forma continua por diseño, que es lo que alimenta los *smoke tests* de `cd-main.yaml`. Con ese perfil los créditos se agotan y el nodo queda estrangulado a unos 0.6 vCPU, con 1.57 vCPU de `requests` encima. `m5.large` entrega los 2 vCPU de forma sostenida.

### Dimensionamiento

La suma de `resources.requests` de `kustomize/base/` es de **1.57 vCPU y 1.34 GiB**; los `limits` suman 2.83 vCPU y 2.48 GiB. Con el techo de 2 vCPU la memoria sobra y la CPU queda ajustada, sobre todo al sumar el componente de observabilidad y, más adelante, `orderservice` y `redis-orders`.

Palancas disponibles, en orden de preferencia: k3s ya arranca con `--disable=traefik` (Traefik ocupaba el puerto 80 que necesita el ServiceLB para publicar la tienda), y si hiciera falta más margen, recortar los `requests` de los servicios menos exigentes. Ese recorte ya se aplicó en #71 sobre `kubernetes-manifests/`: los servicios desplegados reservan 1.17 vCPU, y el detalle está en `docs/despliegue-continuo.md`. **Retirar el `loadgenerator` es el último recurso**, porque los dos *smoke tests* de `cd-main.yaml` leen sus registros para contar peticiones y errores.

## Validación automática

`terraform-validate-ci.yaml` corre en cada Pull Request que toque `terraform/**`, con un job por módulo. Sobre este módulo ejecuta `terraform validate` y `terraform fmt -check`; sobre `gcp-heredado` solo `validate`, por tratarse de código heredado que no se reformatea.

El CI usa `terraform init -backend=false`: no toca el estado real ni necesita credenciales.
