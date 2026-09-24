# El despliegue continuo

Notas de cómo funciona `cd-main.yaml`: qué dispara el despliegue, qué lo frena, dónde despliega, cómo revierte solo, y qué revisar primero cuando algo falla.

Montado el 1 de septiembre de 2026 · issue #18 · [ADR 0008](adr/0008-despliegue-continuo-en-dos-fases.md). Pruebas antes del despliegue desde el issue #53; imágenes publicadas en `ghcr.io` desde el #45.

**Desde el 20 de septiembre de 2026 esto es la fase B** — issue #37, [ADR 0010](adr/0010-fase-b-en-aws-con-k3s-sobre-ec2.md). El despliegue dejó de correr en la Mac de un integrante y pasó al clúster de k3s sobre EC2 que levanta [`terraform/aws/`](../terraform/aws/README.md). La fase A no se borró: el runner self-hosted, [runner-check.yaml](../.github/workflows/runner-check.yaml) y [runner-self-hosted.md](runner-self-hosted.md) siguen siendo el registro de cómo se llegó hasta acá.

## Qué lo dispara

Cualquier `push` a `main` — en la práctica, cada PR que se integra. `paths-ignore` filtra cambios que solo tocan `**/*.md`, `docs/**` o `LICENSE`: un PR que solo actualiza documentación no dispara un despliegue completo de 12 servicios.

```
merge a main → GitHub Actions → pruebas unitarias (ubuntu-24.04)
                                  ❌ → despliegue omitido, el clúster se queda como estaba
                                  ✅ → construir y publicar en ghcr.io (ubuntu-24.04)
                                       ❌ → despliegue omitido
                                       ✅ → deploy (ubuntu-24.04)
                                          → kubeconfig del secreto → ¿el clúster responde?
                                          → skaffold deploy (sin construir) → k3s sobre EC2
                                          → kubectl wait → smoke test → ✅  |  ❌ rollback automático
```

**Nadie aprueba nada y nadie enciende nada.** Los tres jobs corren en máquinas de GitHub, así que un merge despliega solo. Lo único que tiene que estar encendido es la instancia de EC2 — y ahí está el costo que reemplazó al de la fase A.

**Con el laboratorio de AWS cerrado, el despliegue falla.** No espera en cola como pasaba con la Mac apagada: el clúster no existe mientras la instancia está detenida. Por eso el job comprueba que responda **antes** de intentar nada, y falla en veinte segundos con un mensaje que dice qué hacer, en vez de morir diez minutos después dentro del `kubectl wait`. Ver [qué revisar primero](#qué-revisar-primero-cuando-falla).

## Las pruebas van primero

El despliegue arranca **solo si pasan las pruebas unitarias del mismo commit**. Antes del issue #53, `ci-main.yaml` corría las pruebas y `cd-main.yaml` desplegaba al mismo tiempo, sin esperarse: una versión con pruebas rotas se desplegaba igual.

Ahora todo vive en `cd-main.yaml`, como tres jobs encadenados con `needs:`:

| Job | Dónde corre | Qué hace |
|---|---|---|
| `pruebas` | `ubuntu-24.04`, en la nube | Pruebas de Go (`shippingservice`, `productcatalogservice`, `frontend/validator`) y `dotnet test src/cartservice/` |
| `imagenes` | `ubuntu-24.04`, en la nube | Construye los doce artefactos de Skaffold para `linux/amd64` y los publica en `ghcr.io`, etiquetados con el SHA |
| `deploy` | `ubuntu-24.04`, en la nube | Todo lo demás: kubeconfig, despliegue, espera, smoke test y rollback |

**Si algo falla antes, `deploy` sale como omitido (*skipped*) y no llega a tocar el clúster.** La versión que está desplegada sigue en pie y no hay nada que revertir.

- **Los tres jobs corren en la nube.** Desde el issue #37 ninguno depende de una máquina del equipo, así que un merge despliega sin que nadie encienda nada ni apruebe nada.
- **La lista de pruebas es la de `ci-pr.yaml`.** Lo que se exige para integrar un PR y lo que se exige para desplegar es lo mismo. Si se suma un servicio a una lista, hay que sumarlo a la otra.
- **`ci-main.yaml` ya no se dispara con `main`**, solo con `release/*` y a mano. Así hay un solo lugar donde las pruebas deciden si se despliega. No se pierde cobertura: su `paths-ignore` es más amplio que el de `cd-main.yaml`, así que todo merge que lo disparaba también dispara este workflow.
- **`concurrency: cd-main` cubre también las pruebas.** Si un despliegue anterior sigue corriendo, las pruebas del merge siguiente esperan con él. GitHub deja una sola ejecución en espera por grupo: si llega una tercera, la que esperaba se cancela, y el merge más reciente prueba y despliega todo junto. `cancel-in-progress: false` a propósito — dejar el clúster a medio aplicar es peor que esperar.

**Por qué `needs:` y no encadenar dos workflows con `workflow_run`.** `workflow_run` arranca cuando el otro workflow termina, no cuando sale bien: la condición de éxito hay que agregarla a mano, y si falta, el problema sigue igual pero escondido. Además cambia el disparo de un workflow que despliega, y la [regla 1 del ADR 0008](runner-self-hosted.md#reglas-de-seguridad) solo admite `push` a `main` o `workflow_dispatch` — una regla que nació por el runner self-hosted y que se conserva porque ahora protege algo distinto: el secreto del *kubeconfig*. Y como `ci-main.yaml` ignora los cambios de `kustomize/`, `terraform/` y `helm-chart/`, esos merges se quedarían sin desplegar.

## El registro de imágenes: se construye una vez y se despliega eso mismo

Desde el issue #45, cada merge a `main` publica las doce imágenes en `ghcr.io/valentinodepaola`, etiquetadas con el SHA del commit. Lo hace el job `imagenes`, que se autentica con el `GITHUB_TOKEN` que el propio workflow ya recibe: no hay ningún secreto que administrar ni que rotar.

**Construye para `linux/amd64` y nada más**, porque el nodo de k3s es una EC2 `x86_64`. Construir además la variante `arm64` obligaría a emular con QEMU sobre un runner x86, y no la usaría nadie.

Ese job termina escribiendo `build.json` con `--file-output`: la lista de qué imagen le tocó a cada servicio, con su tag completo. Lo sube con `actions/upload-artifact`, y el job `deploy` lo baja y lo aplica:

```
skaffold deploy --build-artifacts=build.json
```

`deploy` es *solo desplegar*; `run` era *construir y desplegar*. **El despliegue ya no construye nada.**

### Por qué antes construía dos veces

Hasta el issue #37 cada merge construía los doce servicios dos veces: una en Ubuntu para publicar, otra en la Mac para desplegar. No era un descuido. Docker Desktop **comparte su almacén de imágenes con el clúster**, y Skaffold reconoce `docker-desktop` como clúster local: construir ahí era lo mismo que poner la imagen dentro del clúster, así que bajarla del registro habría sido dar una vuelta innecesaria.

Un clúster remoto no comparte almacén con nadie. La única forma que tiene de conseguir una imagen es bajarla de un registro — y con eso la construcción doble desapareció sola, sin tener que combatirla.

**Lo que se deja de pasar, y por qué no hace falta.** El `Alcance` del issue #37 pedía `--default-repo=ghcr.io/valentinodepaola` y `skaffold config set --global local-cluster false`. Los dos existen para gobernar una construcción: a qué registro empujar, y si hay que empujar. Sin construcción no gobiernan nada, y los tags de `build.json` ya vienen completos. Comprobado con `skaffold render --offline` antes de escribirlo.

**Los paquetes son públicos**, que es lo que la fase B necesita: un k3s sin credenciales se quedaría en `ImagePullBackOff`. La alternativa era un `imagePullSecret` en el clúster, que son más piezas por mantener.

No hizo falta voltearlos a mano: nacieron públicos al publicarse con el `GITHUB_TOKEN` desde un repositorio que ya lo era. Comprobarlo no requiere credenciales — es la misma ruta que sigue el clúster:

```bash
SHA=$(git rev-parse origin/main)
T=$(curl -s "https://ghcr.io/token?scope=repository:valentinodepaola/frontend:pull&service=ghcr.io" | jq -r .token)
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $T" \
  "https://ghcr.io/v2/valentinodepaola/frontend/manifests/$SHA"
```

`200` es público. `401` o `403` significa que hay que voltearlo en la configuración del paquete.

## La caché de artefactos de Skaffold

El runner de Ubuntu es desechable: GitHub entrega una máquina limpia, corre el job y la destruye. Sin nada que recuerde entre ejecuciones, Skaffold no puede saber qué ya construyó y reconstruye los doce servicios **aunque el merge no haya tocado una línea de ninguno**. Medido antes de la caché:

| Merge | Qué cambió en el código | `imagenes` |
|---|---|---|
| `8c17d84` | los 4 servicios de Go | 9m12s |
| `82d986f` | nada — workflows y documentación | 7m39s |
| `af2941f` | nada — workflows y documentación | 8m38s |

Lo que se guarda con `actions/cache` **no son capas de Docker** —esas no sobreviven al runner desechable— sino el índice de Skaffold en `~/.skaffold/cache`, que asocia un hash de las entradas de cada artefacto con la imagen que produjo. Con ese índice, Skaffold reconoce lo que no cambió y lo reetiqueta en el registro en vez de reconstruirlo.

La clave lleva `github.run_id` porque una clave que ya existe no se sobrescribe: así cada ejecución guarda una entrada nueva, y `restore-keys` recupera la más reciente del prefijo.

El resumen del job (`$GITHUB_STEP_SUMMARY`) registra la duración de cada ejecución, que es la evidencia del cuarto criterio del issue #45.

## El Environment, y por qué ya no pide aprobación

El despliegue corre dentro del Environment **`aws-k3s`**, que cumple tres funciones:

- **Guarda el secreto `KUBECONFIG_K3S`**, el *kubeconfig* del clúster en base64. Un secreto de Environment solo lo alcanza el job que declara ese Environment, a diferencia de uno de repositorio, que cualquier job de cualquier workflow puede pedir. El *kubeconfig* es acceso de administrador al clúster: que lo vea un solo job es la diferencia entre una llave en un cajón y una llave puesta en la puerta.
- **Restringe las ramas** con `protected_branches: true`. Solo desde una rama protegida —hoy `main` y solo `main`— se puede desplegar y, por lo tanto, alcanzar ese secreto.
- **Publica la URL de la tienda** en la pestaña Actions y en la vista de Deployments, sin tener que abrir los logs.

**Lo que ya no tiene es revisor requerido**, y conviene dejar claro por qué, porque es un control que se quitó a propósito.

El Environment de la fase A, `local-cluster`, exigía la aprobación de una persona. Esa compuerta no era un capricho: la [regla 2 del ADR 0008](runner-self-hosted.md#reglas-de-seguridad) la justificaba en que el runner corría **en la máquina de un integrante, sin sandbox, desde un fork público**. Cualquier cosa que se ejecutara en ese job se ejecutaba en una computadora personal, y por eso un humano miraba antes.

Un runner de GitHub es una máquina virtual desechable y aislada que se destruye al terminar el job. **La premisa que justificaba la compuerta dejó de ser cierta**, y un control cuya razón se murió deja de ser seguridad y pasa a ser ceremonia.

Lo que no se perdió es el control humano: para que algo llegue a `main` hace falta un pull request aprobado. La revisión sigue estando, solo que en el lugar donde mira código en vez de en el lugar donde ya no hay nada que mirar.

Con eso, el criterio de aceptación del issue #37 —«un merge despliega sin que nadie toque una terminal»— se cumple al pie de la letra, sin nota al pie.

## El rollback

Si el despliegue falla **después** de haberse aplicado, el workflow revierte automáticamente con `kubectl rollout undo` sobre los servicios que cambian de imagen en cada despliegue.

La condición es `failure() && steps.deploy.outcome != 'skipped'`, y ese `!= 'skipped'` tiene historia. Al principio decía `== 'success'`, razonando que un `skaffold` que falló no llegó a tocar el clúster. **Es falso: `skaffold deploy` aplica primero y espera después.** En el despliegue del merge del PR #62 aplicó once Deployments y recién entonces murió esperando al doceavo — y el rollback no corrió, porque su condición daba por sentado que fallar significaba no haber aplicado.

`!= 'skipped'` distingue los dos casos que sí son distintos: si el preflight falló porque el clúster no responde, el paso queda omitido y no se intenta revertir nada contra una instancia apagada.

**`redis-cart` queda afuera a propósito.** Usa la imagen fija `redis:alpine`, no una etiquetada con el commit, así que nunca genera una revisión nueva — hacerle `rollout undo` la mandaría a una revisión vieja arbitraria o fallaría por falta de historial.

Esto es el nivel 2 de rollback, automático. Para los otros dos niveles (apagar el feature con un kill switch, o volver a la revisión anterior a mano) ver el documento de estrategia de rollback del proyecto.

**La ejecución roja no se borra.** Es la evidencia de la métrica DORA de tiempo de restauración — el resumen del job (`$GITHUB_STEP_SUMMARY`) queda con la duración y si hizo falta rollback o no.

## Qué revisar primero cuando falla

**Si el job muere en `Comprobar que el cluster responde`, el laboratorio está cerrado.** Es el caso más frecuente y el más barato de arreglar. La instancia de EC2 se detiene al cerrar la sesión de AWS Academy, y sin instancia no hay clúster. El paso falla en veinte segundos con una anotación que lo dice. Para volver:

```bash
# 1. Start Lab, y pegar las credenciales nuevas (ver terraform/aws/README.md)
aws sts get-caller-identity
# 2. Si la instancia no existe, recrearla
cd terraform/aws && terraform plan -out=tfplan && terraform apply tfplan
# 3. Volver a extraer el kubeconfig y regenerar el secreto
terraform output -raw kubeconfig_comando | bash
base64 -i ~/.kube/boutique-k3s.yaml | gh secret set KUBECONFIG_K3S \
  -R valentinodepaola/microservices-demo --env aws-k3s
```

El paso 3 no es opcional aunque la IP no haya cambiado: al recrear la instancia, k3s genera **certificados nuevos**, y el *kubeconfig* guardado en el secreto queda apuntando a una cerradura que ya no existe. El síntoma sería un error de TLS, no de conexión.

**Si `deploy` aparece omitido, falló algo antes.** El error está en `pruebas` o en `imagenes`, y el clúster sigue con la versión anterior. No hay nada que revertir.

**Si `loadgenerator` queda en `Pending` con `Insufficient cpu`**, revisar que su Deployment conserve `maxSurge: 0`. Un rolling update normal crea el pod nuevo antes de matar al viejo, y `loadgenerator` pedía entonces 300m — más que los 230m de margen que dejaba la tienda desplegada, así que **no cabía al lado de sí mismo**. Pasó en el merge del PR #62, con los otros once Deployments ya actualizados. Desde #71 pide 100m y ya cabría, pero `maxSurge: 0` se conserva a propósito: en este servicio quedarse un momento sin pod no afecta a nadie, y quitarlo es otro cambio con su propio riesgo. El porqué está comentado en `kubernetes-manifests/loadgenerator.yaml`.

**Si es otro pod el que queda en `Pending` con `Insufficient cpu`**, es el techo de 2 vCPU del laboratorio. La regla es que **el margen libre tiene que ser al menos igual a lo que reserva el servicio más grande**. Cada merge cambia el tag de las doce imágenes, y el rolling update de cada Deployment crea el pod nuevo antes de matar al viejo. Los pods que no caben esperan en `Pending` a que otro termine y libere su reserva, así que no hace falta margen para los doce a la vez, pero sí para el más grande.

Con los valores de upstream, los `requests` de los doce servicios sumaban 1570m y el nodo quedaba en 1770m reservados de 2000m: 230m de margen, que `redis-orders` (70m) y `orderservice` iban a dejar por debajo de los 200m de `adservice` y `cartservice`. Medido el 23/09/2026 con el `loadgenerator` enviando tráfico, el nodo usaba de 110 a 149m: **88% reservado contra 7% usado**. Por eso en #71 se recortaron los tres que más reservaban —`loadgenerator` de 300m a 100m, `adservice` y `cartservice` de 200m a 100m— y ningún servicio reserva ya más de 100m. El nodo queda en 1370m reservados y **630m de margen**, unos 460m cuando lleguen `orderservice` y `redis-orders`. Los `limits` no se tocaron, así que los servicios pesados al arrancar (Java y .NET) siguen teniendo de dónde tomar CPU. Lo que sí cambia es que un `request` menor da menos peso cuando la CPU está contendida, como cuando arrancan los doce a la vez; los reinicios del arranque se comparan contra el párrafo siguiente. Si el margen vuelve a faltar, las palancas son las mismas: recortar más `requests`, midiendo antes con `kubectl top pods`, y como último recurso desplegar sin `loadgenerator` — que rompe los dos smoke tests.

**Si `emailservice` o `recommendationservice` reinician una vez al arrancar, es esperable.** Los doce pods arrancan a la vez sobre 2 vCPU, la CPU queda contendida, y un `timeoutSeconds: 1` en el probe gRPC declara muerto a un proceso que solo estaba lento — `exitCode=137`, o sea SIGKILL del kubelet. Al reiniciar, los demás ya arrancaron y levanta al primer intento. Medido el 20/09/2026: los doce estables en 50 segundos y el smoke test en 190 peticiones con 0 errores. Los probes viven en `kubernetes-manifests/`, que es el entregable compartido, así que **no se ajustan** para acomodar una limitación del laboratorio.

**Si ningún pod arranca y todos dicen `ImagePullBackOff`**, algún paquete de `ghcr.io` dejó de ser público. Ver la comprobación en [la sección del registro](#el-registro-de-imágenes-se-construye-una-vez-y-se-despliega-eso-mismo).

### Lo que ya no puede pasar

Dos fallas que dominaron la fase A y que desaparecieron con el issue #37, anotadas porque van a aparecer en ejecuciones viejas:

**`error getting credentials` o algo sobre el llavero.** Era el *credential helper* de Docker chocando con el LaunchAgent del runner en macOS (issue #57). El job traía su propio `DOCKER_CONFIG` con un helper `noop` para esquivarlo. Ya no existe: el job no construye imágenes y la máquina no es una Mac. El diagnóstico completo sigue en [runner-self-hosted.md](runner-self-hosted.md#el-llavero-y-por-qué-el-job-trae-su-propia-config-de-docker).

**El despliegue esperando en cola.** Con la Mac apagada, el job quedaba encolado hasta que el runner volviera. Ahora corre en la nube y arranca siempre; lo que puede faltar es el clúster, y eso falla rápido en vez de esperar.

[`runner-check.yaml`](../.github/workflows/runner-check.yaml) sigue existiendo y sigue siendo válido, pero diagnostica el runner self-hosted de la fase A, no este pipeline.

### `cartservice` y Rosetta — un hallazgo del pre-vuelo, no del pipeline

Al construir el repo por primera vez desde código (nunca antes se había hecho — siempre se usaron las imágenes ya publicadas), `cartservice` crasheaba con `rosetta error: failed to open elf`. Los demás 11 servicios toleran correr amd64 emulado por Rosetta sin problema — `cartservice` no, porque su Dockerfile usa `PublishSingleFile=true --self-contained true` de .NET, un patrón de auto-extracción en tiempo de ejecución incompatible con Rosetta.

La causa real: `TARGETARCH` no se estaba propagando correctamente desde Docker Desktop/BuildKit al Dockerfile, así que compilaba amd64 aunque el clúster es arm64. El fix vive en `skaffold.yaml`, como un profile que se activa solo por el contexto de `kubectl`:

```yaml
- name: local-arm64
  activation:
  - kubeContext: docker-desktop
  patches:
  - op: add
    path: /build/artifacts/8/docker/buildArgs
    value:
      TARGETARCH: arm64
      BUILDPLATFORM: linux/arm64
```

No toca nada de la fase B — ahí el contexto no es `docker-desktop`, el profile queda inactivo, y `cartservice` usa su default `amd64`, que es correcto en cualquier nube con nodos x86 real. Si en algún momento este profile deja de alcanzar (ej. si otro servicio empieza a fallar igual bajo Rosetta), el índice `8` corresponde a `cartservice` en el array de `build.artifacts` — confirmado con `skaffold diagnose`, no a ojo.

## Dónde despliega: AWS con k3s sobre EC2

El destino se fijó el 10 de septiembre de 2026 —**AWS**, por acceso institucional de AWS Academy— y quedó en pie el 20 de septiembre con el issue #37. No salió de la comparativa de nubes, donde AWS quedaba en último lugar, sino de que es el entorno con el que hay que trabajar. El razonamiento completo y lo que se paga por ello están en el [ADR 0010](adr/0010-fase-b-en-aws-con-k3s-sobre-ec2.md), que reemplaza la fase B del [ADR 0008](adr/0008-despliegue-continuo-en-dos-fases.md).

Dentro de AWS el clúster **no es EKS**: es **k3s sobre una instancia EC2 `x86_64`**, provisionada con Terraform. EKS arrastra un costo de control plane que ningún *free tier* cubre, y en un entorno de AWS Academy la autenticación federada desde GitHub Actions choca con las restricciones de IAM.

| | Fase A — hasta el 20/09 | Fase B — ahora |
|---|---|---|
| `runs-on` de `deploy` | `[self-hosted, order-tracking]` | `ubuntu-24.04` |
| Clúster | `docker-desktop`, en una Mac | k3s sobre EC2 `x86_64` |
| Cómo lo alcanza | `kubectl config use-context docker-desktop` | *Kubeconfig* en un secreto del Environment `aws-k3s` |
| Compuerta | Revisor requerido en el Environment | Ninguna — la razón que la justificaba desapareció |
| Imágenes | `ghcr.io` publica, pero el despliegue reconstruye en local | `ghcr.io`, y el despliegue aplica eso mismo sin construir |
| Infraestructura | Ninguna | Terraform, con estado remoto en S3 |

**Por qué `ghcr.io` y no ECR.** Las credenciales de AWS Academy rotan en cada sesión —clave, secreto y *session token*—, así que un secreto de GitHub Actions apuntando a ECR caduca cada pocas horas. `ghcr.io` se autentica con el token que el propio workflow ya recibe, y no hay nada que rotar.

**Por qué `x86_64` y no Graviton.** Las imágenes de Online Boutique no tienen variante ARM. Las instancias baratas de AWS sí lo son, y elegir una de esas deja los doce pods sin arrancar.

**Lo que no cambió, que es la apuesta del ADR 0008 cobrada.** Los jobs `pruebas` e `imagenes` ya corrían en `ubuntu-24.04` y no se tocaron. Dentro de `deploy`, los pasos de espera, los dos smoke tests y el rollback quedaron **idénticos, carácter por carácter**, pese a que el destino cambió de un Docker Desktop en una Mac a un k3s en otro continente. El cambio entero fueron 121 líneas agregadas —más de la mitad comentarios— y 71 borradas, de las cuales 48 eran el arreglo del llavero de macOS que dejó de hacer falta.

El profile `local-arm64` de `skaffold.yaml` queda inactivo por sí solo, porque se activa por `kubeContext: docker-desktop`. Es lo correcto: el nodo es x86.

### Verificado el 20 de septiembre de 2026

El despliegue se corrió contra el clúster real, con el mismo comando que ejecuta el workflow:

| Qué | Resultado |
|---|---|
| Los doce Deployments | `Running` en 64 segundos, estabilizados en 50s |
| La tienda desde internet | `HTTP 200` en 120 ms |
| Smoke test | 190 peticiones, **0 errores** |
| CPU del nodo con todo arriba | 1770m de 2000m (88%) |

### El entorno, verificado el 10 de septiembre de 2026

Los límites del laboratorio de AWS Academy deciden el dimensionamiento. Se comprobaron contra la API antes de escribir el módulo, y el resultado está registrado en el [issue #44](https://github.com/valentinodepaola/microservices-demo/issues/44):

| Límite | Qué se encontró | Consecuencia |
|---|---|---|
| Tamaño de instancia | Techo de **2 vCPU** (`large`). `xlarge` en adelante, rechazada | El nodo es `m5.large` |
| Región | Solo `us-east-1` y `us-west-2` | El módulo opera en `us-east-1` |
| Roles de IAM | No se pueden crear | Se usa el `LabInstanceProfile` preexistente, que ya trae SSM |
| Fin de sesión | Las instancias se **detienen**, no se terminan | La infraestructura sobrevive entre sesiones |
| IP al reiniciar | Cambia, salvo que haya IP elástica asociada | La IP elástica es obligatoria: sin ella se invalidan el certificado de k3s, el *kubeconfig* y la URL pública |
| Presupuesto | 50 USD; agotarlo desactiva la cuenta | La instancia se detiene a mano al terminar cada jornada |

**El dimensionamiento resultó menos ajustado de lo que se temía.** La estimación de "4 vCPU y 8–16 GB" venía del README de upstream y describe un *node pool* de GKE, no lo que piden los manifiestos: sumando `kustomize/base/`, los `requests` son **1.57 vCPU y 1.34 GiB** y los `limits` 2.83 vCPU y 2.48 GiB. Con 2 vCPU la memoria sobra y la CPU queda justa, así que k3s arranca con `--disable=traefik` —que además libera el puerto 80 para el ServiceLB— y los `requests` fueron la siguiente palanca. Esos 1.57 vCPU son los valores de Google, que `kustomize/base/` conserva; lo desplegado en AWS reserva 1.17 vCPU desde #71, después de medir que el nodo usaba el 7% de lo que tenía reservado. Desplegar sin `loadgenerator` sigue siendo el último recurso, porque los dos smoke tests leen sus registros.

**El descarte de EKS quedó con número.** El laboratorio sí ofrece EKS, con roles ya creados. Pero su control plane cuesta del orden de 0.10 USD por hora de reloj y **no se detiene con la sesión**, al no ser una instancia que el laboratorio pueda apagar: unos 2.40 USD diarios corriendo solo, que agotarían los 50 USD en unas tres semanas sin haber desplegado nada.

### La infraestructura

Descrita en [`terraform/aws/`](../terraform/aws/README.md): VPC, subred pública, internet gateway, tabla de rutas, security group con `6443` y `80` —sin SSH, se entra por SSM—, la instancia con k3s y su IP elástica. El estado vive en S3 con bloqueo nativo.

El módulo **crea la infraestructura y no despliega la aplicación**: eso lo hace `cd-main.yaml`. La separación es deliberada — el módulo heredado de Google mezclaba las dos cosas en un mismo `apply`, y así ni la infraestructura ni el despliegue se distinguían como piezas propias.

**Al terminar la jornada la instancia se detiene, no se destruye.** Detenida cuesta del orden de 0.20 USD por día y conserva el disco, la IP elástica, el *kubeconfig* y la aplicación desplegada; volver es un comando. Destruirla obliga a recrearla, regenerar el secreto `KUBECONFIG_K3S` —porque k3s emite certificados nuevos— y volver a desplegar. El procedimiento completo está en [operar-el-cluster-de-aws.md](operar-el-cluster-de-aws.md).

## Páginas relacionadas

- [operar-el-cluster-de-aws.md](operar-el-cluster-de-aws.md) — **cómo levantar y apagar el clúster**, renovar las llaves del laboratorio y qué hacer cuando la tienda no responde
- [`terraform/aws/README.md`](../terraform/aws/README.md) — el módulo que levanta el clúster y produce el *kubeconfig*
- [ADR 0008](adr/0008-despliegue-continuo-en-dos-fases.md) — el diseño en dos fases sobre un mismo workflow, y las tres reglas de seguridad
- [ADR 0010](adr/0010-fase-b-en-aws-con-k3s-sobre-ec2.md) — el destino de la fase B: AWS con k3s sobre EC2
- [`cd-main.yaml`](../.github/workflows/cd-main.yaml) — el workflow
- [runner-self-hosted.md](runner-self-hosted.md) — la máquina de la fase A. Ya no despliega, pero es el registro de cómo se llegó hasta acá
- [`runner-check.yaml`](../.github/workflows/runner-check.yaml) — diagnóstico de esa máquina, no de este pipeline
