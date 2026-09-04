# El despliegue continuo

Notas de cómo funciona `cd-main.yaml`: qué dispara el despliegue, qué hace la aprobación del Environment, cómo revierte solo, y qué revisar primero cuando algo falla.

Montado el 1 de septiembre de 2026 · issue #18 · [ADR 0008](adr/0008-despliegue-continuo-en-dos-fases.md) · corre sobre la infraestructura de [runner-self-hosted.md](runner-self-hosted.md).

## Qué lo dispara

Cualquier `push` a `main` — en la práctica, cada PR que se integra. `paths-ignore` filtra cambios que solo tocan `**/*.md`, `docs/**` o `LICENSE`: un PR que solo actualiza documentación no dispara un despliegue completo de 12 servicios.

```
merge a main → GitHub Actions → runner self-hosted (mac-valentino)
             → skaffold run → clúster docker-desktop
             → kubectl wait → smoke test → ✅  |  ❌ rollback automático
```

**Con la Mac apagada, el workflow no falla: espera en cola** hasta que el runner vuelva a estar en línea. Es el costo aceptado de la fase A — ver [runner-self-hosted.md](runner-self-hosted.md#con-qué-hay-que-contar).

## Por qué no hay registro de imágenes

Docker Desktop comparte su almacén de imágenes con el clúster de kubeadm, y Skaffold reconoce `docker-desktop` como clúster local: construye con el Docker local y se salta el `push`. Es lo contrario de lo que hace `ci-main.yaml`, que fuerza `skaffold config set --global local-cluster false` precisamente para empujar a GKE.

## La aprobación del Environment no contradice "sin intervención"

El criterio de aceptación del issue dice que un merge despliega solo. El Environment `local-cluster` exige un revisor. No son contradictorios: la aprobación es una **compuerta de seguridad obligatoria** (ADR 0008 — el runner corre en una máquina del equipo, sin sandbox, y el fork es público), no un paso operativo. Nadie toca una terminal ni corre un comando — solo aprueba el despliegue que ya está listo para correr.

Para aprobar: **Actions → la ejecución en cola → Review deployments → Approve and deploy**. El revisor configurado es `valentinodepaola`, con `prevent_self_review: false` a propósito — si no, quien integra el PR no podría aprobar su propio despliegue y el pipeline se trabaría.

## El rollback

Si `kubectl wait` o el smoke test fallan **después** de que `skaffold run` se aplicó, el workflow revierte automáticamente con `kubectl rollout undo` sobre los servicios que se reconstruyen en cada despliegue.

**`redis-cart` queda afuera a propósito.** Usa la imagen fija `redis:alpine`, no `--tag=$GITHUB_SHA`, así que nunca genera una revisión nueva — hacerle `rollout undo` la mandaría a una revisión vieja arbitraria o fallaría por falta de historial.

Esto es el nivel 2 de rollback, automático. Para los otros dos niveles (apagar el feature con un kill switch, o volver a la revisión anterior a mano) ver `estrategia-de-rollback.md` en el vault del proyecto.

**La ejecución roja no se borra.** Es la evidencia de la métrica DORA de tiempo de restauración — el resumen del job (`$GITHUB_STEP_SUMMARY`) queda con la duración y si hizo falta rollback o no.

## Qué revisar primero cuando falla

**Antes de sospechar del pipeline, correr [`runner-check.yaml`](../.github/workflows/runner-check.yaml):**

```bash
gh workflow run runner-check.yaml --repo valentinodepaola/microservices-demo --ref main
```

Revisa que la máquina tenga `docker`, `kubectl`, `skaffold`, y que el contexto sea `docker-desktop`. No despliega nada. Separa en menos de un minuto "la máquina está mal" de "el pipeline está mal".

### `cartservice` y Rosetta — un hallazgo del pre-vuelo, no del pipeline

Al construir el repo por primera vez desde código (nunca antes se había hecho — siempre se usaron las imágenes ya publicadas, ver `como-levantar-online-boutique.md` en el vault del proyecto), `cartservice` crasheaba con `rosetta error: failed to open elf`. Los demás 11 servicios toleran correr amd64 emulado por Rosetta sin problema — `cartservice` no, porque su Dockerfile usa `PublishSingleFile=true --self-contained true` de .NET, un patrón de auto-extracción en tiempo de ejecución incompatible con Rosetta.

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

## La fase B todavía no está decidida

Todo lo de arriba es la fase A, ya funcionando. La fase B (destino en la nube, para cuando se acerque la demo) sigue **sin resolver** — depende de una pregunta al profesor, ver `pendientes.md` en el vault del proyecto. GKE es la opción hoy mejor posicionada porque hereda casi entera la pipeline de `ci-main.yaml`, pero no está cerrada: AWS y Azure también están en la comparativa (`opciones-de-despliegue-en-la-nube.md`).

Lo que **sí** está decidido por ADR 0008, sea cual sea el proveedor: la fase B se construye **sobre el mismo `cd-main.yaml`**, no un workflow aparte. Cambian el `runs-on`, la autenticación al proveedor, el contexto de clúster y el registro de imágenes. Los pasos de espera, smoke test y rollback no cambian.

## Páginas relacionadas

- [runner-self-hosted.md](runner-self-hosted.md) — la máquina y el Environment sobre los que corre este workflow
- [ADR 0008](adr/0008-despliegue-continuo-en-dos-fases.md) — el diseño en dos fases (la fase B, sin proveedor decidido todavía)
- [`cd-main.yaml`](../.github/workflows/cd-main.yaml) — el workflow
- [`runner-check.yaml`](../.github/workflows/runner-check.yaml) — diagnóstico de la máquina, correr primero ante cualquier falla
