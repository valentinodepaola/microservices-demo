# El despliegue continuo

Notas de cómo funciona `cd-main.yaml`: qué dispara el despliegue, qué lo frena, qué hace la aprobación del Environment, cómo revierte solo, y qué revisar primero cuando algo falla.

Montado el 1 de septiembre de 2026 · issue #18 · [ADR 0008](adr/0008-despliegue-continuo-en-dos-fases.md) · corre sobre la infraestructura de [runner-self-hosted.md](runner-self-hosted.md). Pruebas antes del despliegue desde el issue #53; imágenes publicadas en `ghcr.io` desde el #45.

## Qué lo dispara

Cualquier `push` a `main` — en la práctica, cada PR que se integra. `paths-ignore` filtra cambios que solo tocan `**/*.md`, `docs/**` o `LICENSE`: un PR que solo actualiza documentación no dispara un despliegue completo de 12 servicios.

```
merge a main → GitHub Actions → pruebas unitarias (ubuntu-24.04)
                                  ❌ → despliegue omitido, el clúster se queda como estaba
                                  ✅ → construir y publicar en ghcr.io (ubuntu-24.04)
                                       ❌ → despliegue omitido
                                       ✅ → aprobación del Environment → runner self-hosted (mac-valentino)
                                          → skaffold run → clúster docker-desktop
                                          → kubectl wait → smoke test → ✅  |  ❌ rollback automático
```

**Con la Mac apagada, el workflow no falla:** las pruebas corren en la nube y el despliegue **espera en cola** hasta que el runner vuelva a estar en línea. Es el costo aceptado de la fase A — ver [runner-self-hosted.md](runner-self-hosted.md#con-qué-hay-que-contar).

## Las pruebas van primero

El despliegue arranca **solo si pasan las pruebas unitarias del mismo commit**. Antes del issue #53, `ci-main.yaml` corría las pruebas y `cd-main.yaml` desplegaba al mismo tiempo, sin esperarse: una versión con pruebas rotas se desplegaba igual.

Ahora todo vive en `cd-main.yaml`, como tres jobs encadenados con `needs:`:

| Job | Dónde corre | Qué hace |
|---|---|---|
| `pruebas` | `ubuntu-24.04`, en la nube | Pruebas de Go (`shippingservice`, `productcatalogservice`, `frontend/validator`) y `dotnet test src/cartservice/` |
| `imagenes` | `ubuntu-24.04`, en la nube | Construye los doce artefactos de Skaffold para `linux/amd64` y los publica en `ghcr.io`, etiquetados con el SHA |
| `deploy` | `[self-hosted, order-tracking]`, la Mac | Todo lo demás: despliegue, espera, smoke test y rollback |

**Si algo falla antes, `deploy` sale como omitido (*skipped*) y la aprobación del Environment no se llega a pedir.** GitHub evalúa la protección del Environment justo cuando el job va a arrancar, y un job omitido nunca arranca. Nadie tiene que acordarse de revisar las pruebas antes de aprobar.

- **Las pruebas no corren en el runner.** No necesitan el clúster, y así no suman tiempo ni ejecutan código en la máquina del equipo.
- **La lista de pruebas es la de `ci-pr.yaml`.** Lo que se exige para integrar un PR y lo que se exige para desplegar es lo mismo. Si se suma un servicio a una lista, hay que sumarlo a la otra.
- **`ci-main.yaml` ya no se dispara con `main`**, solo con `release/*` y a mano. Así hay un solo lugar donde las pruebas deciden si se despliega. No se pierde cobertura: su `paths-ignore` es más amplio que el de `cd-main.yaml`, así que todo merge que lo disparaba también dispara este workflow.
- **`concurrency: cd-main` cubre también las pruebas.** Si un despliegue anterior está esperando aprobación o a la Mac, las pruebas del merge siguiente esperan con él. GitHub deja una sola ejecución en espera por grupo: si llega una tercera, la que esperaba se cancela, y el merge más reciente prueba y despliega todo junto.

**Por qué `needs:` y no encadenar dos workflows con `workflow_run`.** `workflow_run` arranca cuando el otro workflow termina, no cuando sale bien: la condición de éxito hay que agregarla a mano, y si falta, el problema sigue igual pero escondido. Además cambia el disparo de un workflow que usa el runner, y la [regla 1](runner-self-hosted.md#reglas-de-seguridad) solo admite `push` a `main` o `workflow_dispatch`. Y como `ci-main.yaml` ignora los cambios de `kustomize/`, `terraform/` y `helm-chart/`, esos merges se quedarían sin desplegar.

## El registro de imágenes, y por qué el despliegue igual construye en local

Desde el issue #45, cada merge a `main` publica las doce imágenes en `ghcr.io/valentinodepaola`, etiquetadas con el SHA del commit. Lo hace el job `imagenes`, que se autentica con el `GITHUB_TOKEN` que el propio workflow ya recibe: no hay ningún secreto que administrar ni que rotar.

**Construye para `linux/amd64` y nada más.** La Mac del runner es Apple Silicon (`arm64`) y el clúster de la fase B es una EC2 `x86_64` — una imagen construida en la Mac no arrancaría ahí. Construir además la variante `arm64` obligaría a emular con QEMU sobre un runner x86, y nadie usaría esa mitad. Por eso el job corre en `ubuntu-24.04` y no en el runner, y por eso pasa `--platform=linux/amd64` en vez de dejar que Skaffold use las dos plataformas que declara `skaffold.yaml`.

**Y aun así el despliegue vuelve a construir.** Docker Desktop comparte su almacén de imágenes con el clúster, y Skaffold reconoce `docker-desktop` como clúster local: `skaffold run` construye con el Docker local, en `arm64`, y se salta el `push`. No baja nada del registro porque no lo necesita.

Así que **cada merge construye dos veces**: una en Ubuntu para publicar, otra en la Mac para desplegar. Es deliberado, no un descuido — la fase A no necesita registro, y la fase B no puede prescindir de él. Termina cuando el issue #37 reapunte `cd-main.yaml` al clúster de k3s y el despliegue pase a bajar las imágenes en lugar de fabricarlas.

**Los paquetes son públicos**, que es lo que la fase B necesita: un k3s sin credenciales se quedaría en `ImagePullBackOff`. La alternativa era un `imagePullSecret` en el clúster, que son más piezas por mantener.

No hizo falta voltearlos a mano: nacieron públicos al publicarse con el `GITHUB_TOKEN` desde un repositorio que ya lo era. Comprobarlo no requiere credenciales — es la misma ruta que seguiría el clúster:

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

## La aprobación del Environment no contradice "sin intervención"

El criterio de aceptación del issue dice que un merge despliega solo. El Environment `local-cluster` exige un revisor. No son contradictorios: la aprobación es una **compuerta de seguridad obligatoria** (ADR 0008 — el runner corre en una máquina del equipo, sin sandbox, y el fork es público), no un paso operativo. Nadie toca una terminal ni corre un comando — solo aprueba el despliegue que ya está listo para correr.

Para aprobar: **Actions → la ejecución en cola → Review deployments → Approve and deploy**. El revisor configurado es `valentinodepaola`, con `prevent_self_review: false` a propósito — si no, quien integra el PR no podría aprobar su propio despliegue y el pipeline se trabaría.

## El rollback

Si `kubectl wait` o el smoke test fallan **después** de que `skaffold run` se aplicó, el workflow revierte automáticamente con `kubectl rollout undo` sobre los servicios que se reconstruyen en cada despliegue.

**`redis-cart` queda afuera a propósito.** Usa la imagen fija `redis:alpine`, no `--tag=$GITHUB_SHA`, así que nunca genera una revisión nueva — hacerle `rollout undo` la mandaría a una revisión vieja arbitraria o fallaría por falta de historial.

Esto es el nivel 2 de rollback, automático. Para los otros dos niveles (apagar el feature con un kill switch, o volver a la revisión anterior a mano) ver el documento de estrategia de rollback del proyecto.

**La ejecución roja no se borra.** Es la evidencia de la métrica DORA de tiempo de restauración — el resumen del job (`$GITHUB_STEP_SUMMARY`) queda con la duración y si hizo falta rollback o no.

## Qué revisar primero cuando falla

**Si `deploy` aparece omitido, no es la máquina: falló algo antes.** El error está en `pruebas` o en `imagenes`, y el clúster sigue con la versión anterior. No hay nada que revertir.

**Si `deploy` falla construyendo, con `error getting credentials` o algo sobre el llavero**, es el *credential helper* de Docker chocando con el LaunchAgent del runner. Está explicado en [runner-self-hosted.md](runner-self-hosted.md#el-llavero-y-por-qué-el-job-trae-su-propia-config-de-docker); el job ya trae su propio `DOCKER_CONFIG` para evitarlo, así que si vuelve a salir es que alguien lo quitó.

**Antes de sospechar del pipeline, correr [`runner-check.yaml`](../.github/workflows/runner-check.yaml):**

```bash
gh workflow run runner-check.yaml --repo valentinodepaola/microservices-demo --ref main
```

Revisa que la máquina tenga `docker`, `kubectl`, `skaffold`, y que el contexto sea `docker-desktop`. No despliega nada. Separa en menos de un minuto "la máquina está mal" de "el pipeline está mal".

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

## La fase B — AWS con k3s sobre EC2

Todo lo de arriba es la fase A, ya funcionando. La **fase B** tiene destino desde el 10 de septiembre de 2026: **AWS**, por acceso institucional de AWS Academy. No salió de la comparativa de nubes —ahí AWS quedaba en último lugar— sino de que es el entorno con el que hay que trabajar. El razonamiento completo y lo que se paga por ello están en el [ADR 0010](adr/0010-fase-b-en-aws-con-k3s-sobre-ec2.md), que reemplaza la fase B del [ADR 0008](adr/0008-despliegue-continuo-en-dos-fases.md).

Dentro de AWS el clúster **no es EKS**: es **k3s sobre una instancia EC2 `x86_64`**, provisionada con Terraform. EKS arrastra un costo de control plane que ningún *free tier* cubre, y en un entorno de AWS Academy la autenticación federada desde GitHub Actions choca con las restricciones de IAM.

| | Fase A — ya | Fase B — AWS |
|---|---|---|
| `runs-on` | `[self-hosted, order-tracking]` | `ubuntu-24.04` |
| Clúster | `docker-desktop` | k3s sobre EC2 `x86_64` |
| Contexto | `kubectl config use-context docker-desktop` | *kubeconfig* del k3s, desde los secretos del repositorio |
| Registro | `ghcr.io` — se publica, pero el despliegue no baja de ahí | El mismo `ghcr.io`, y el despliegue sí baja de ahí |
| Infraestructura | Ninguna | Terraform, con estado remoto en S3 |

**Por qué `ghcr.io` y no ECR.** Las credenciales de AWS Academy rotan en cada sesión —clave, secreto y *session token*—, así que un secreto de GitHub Actions apuntando a ECR caduca cada pocas horas. `ghcr.io` se autentica con el token que el propio workflow ya recibe, y no hay nada que rotar.

**Por qué `x86_64` y no Graviton.** Las imágenes de Online Boutique no tienen variante ARM. Las instancias baratas de AWS sí lo son, y elegir una de esas deja los doce pods sin arrancar.

Lo que **no** cambia, igual que predijo el ADR 0008: los jobs `pruebas` e `imagenes` —ya corren en `ubuntu-24.04`—, los pasos de espera, los smoke tests y el rollback. El profile `local-arm64` de `skaffold.yaml` queda inactivo por sí solo, porque se activa por `kubeContext: docker-desktop`.

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

**El dimensionamiento resultó menos ajustado de lo que se temía.** La estimación de "4 vCPU y 8–16 GB" venía del README de upstream y describe un *node pool* de GKE, no lo que piden los manifiestos: sumando `kustomize/base/`, los `requests` son **1.57 vCPU y 1.34 GiB** y los `limits` 2.83 vCPU y 2.48 GiB. Con 2 vCPU la memoria sobra y la CPU queda justa, así que k3s arranca con `--disable=traefik` —que además libera el puerto 80 para el ServiceLB— y quedan los `requests` como siguiente palanca. Desplegar sin `loadgenerator` sigue siendo el último recurso, porque los dos smoke tests leen sus registros.

**El descarte de EKS quedó con número.** El laboratorio sí ofrece EKS, con roles ya creados. Pero su control plane cuesta del orden de 0.10 USD por hora de reloj y **no se detiene con la sesión**, al no ser una instancia que el laboratorio pueda apagar: unos 2.40 USD diarios corriendo solo, que agotarían los 50 USD en unas tres semanas sin haber desplegado nada.

### La infraestructura

Descrita en [`terraform/aws/`](../terraform/aws/README.md): VPC, subred pública, internet gateway, tabla de rutas, security group con `6443` y `80` —sin SSH, se entra por SSM—, la instancia con k3s y su IP elástica. El estado vive en S3 con bloqueo nativo.

El módulo **crea la infraestructura y no despliega la aplicación**: eso queda para `cd-main.yaml`, que es el reapuntamiento pendiente del issue #37.

## Páginas relacionadas

- [runner-self-hosted.md](runner-self-hosted.md) — la máquina y el Environment sobre los que corre este workflow
- [ADR 0008](adr/0008-despliegue-continuo-en-dos-fases.md) — el diseño en dos fases sobre un mismo workflow
- [ADR 0010](adr/0010-fase-b-en-aws-con-k3s-sobre-ec2.md) — el destino de la fase B: AWS con k3s sobre EC2
- [`cd-main.yaml`](../.github/workflows/cd-main.yaml) — el workflow
- [`runner-check.yaml`](../.github/workflows/runner-check.yaml) — diagnóstico de la máquina, correr primero ante cualquier falla
