# El runner self-hosted

Notas de cómo quedó montado el runner de GitHub Actions donde corre el despliegue automático: qué hay que respetar para no abrir un hueco de seguridad, y con qué hay que contar cuando la máquina no está disponible.

Montado el 1 de septiembre de 2026 · issue #17 · [ADR 0008](adr/0008-despliegue-continuo-en-dos-fases.md).

## Por qué hace falta

GitHub Actions corre en máquinas de GitHub, en la nube. Nuestro clúster vive en el `localhost` de una máquina del equipo, y una computadora remota no puede llegar a ese `localhost`. No hay configuración que lo arregle.

El runner le da la vuelta al problema. Es un programa que se instala en la máquina del equipo y le va preguntando a GitHub si hay trabajo pendiente. La conexión sale de la Mac hacia GitHub, nunca al revés, así que no hay que abrir puertos ni exponer nada: el trabajo baja hasta donde ya está el clúster, en vez de intentar que el clúster suba.

## La máquina

| | |
|---|---|
| Equipo | MacBook Pro de Valentino — macOS 26.6, Apple Silicon (`arm64`) |
| Runner | `mac-valentino`, versión `osx-arm64` v2.337.0 |
| Etiquetas | `self-hosted`, `macOS`, `ARM64`, **`order-tracking`** |
| Clúster destino | Docker Desktop, contexto `docker-desktop` |
| Directorio | `~/actions-runner`, fuera del repositorio |
| Servicio | LaunchAgent, arranca al iniciar sesión |

Es la misma máquina donde ya está el clúster local verificado. La etiqueta `order-tracking` es la que usan los workflows para mandar trabajos aquí:

```yaml
runs-on: [self-hosted, order-tracking]
```

## Reglas de seguridad

El fork es público, y el job no corre en una máquina desechable: corre en la computadora de alguien del equipo, con su usuario y sin aislamiento. Lo que se ejecute ahí puede leer lo mismo que esa persona — sus llaves SSH, sus credenciales, sus otros proyectos.

Por eso GitHub desaconseja los runners self-hosted en repositorios públicos. Cualquiera puede abrir un Pull Request, y si un workflow de PR corriera en el runner, código de un desconocido se ejecutaría en esa máquina.

Cualquier workflow que use `runs-on: [self-hosted, order-tracking]` tiene que cumplir las tres:

| # | Regla | Qué evita |
|---|---|---|
| 1 | Se dispara solo con `push` a `main` o `workflow_dispatch`. Nunca con `pull_request` ni `pull_request_target`. | Que llegue código de terceros a la máquina. Para entrar a `main` hay que pasar por un PR aprobado. |
| 2 | Declara `environment: local-cluster`. | Que algo se ejecute sin que alguien lo apruebe antes. El Environment además solo permite la rama `main`. |
| 3 | Declara `permissions: contents: read`. | Que un job comprometido escriba en el repositorio. |

Aparte de eso, en Settings → Actions → General la aprobación de workflows de forks está en *Require approval for all external contributors*.

> Lo que no hay que hacer: agregarle un trigger de `pull_request` a un workflow del runner para probar más rápido. Eso tumba la regla 1, que es la que sostiene a las otras dos.

Los workflows que sí corren con Pull Requests (`ci-pr.yaml`, `ci-main.yaml`) siguen en `ubuntu-24.04`, en la nube. No tocan esta máquina.

## Con qué hay que contar

Las tres primeras se resumen en lo mismo: si esa Mac no está lista, no hay despliegue.

1. **Arranca al iniciar sesión, no al encender.** En macOS `svc.sh install` deja un LaunchAgent, no un LaunchDaemon, y un Agent pertenece a la sesión de usuario. Después de reiniciar hay que iniciar sesión para que el runner vuelva a estar en línea.
2. **Si el servicio se cae, no se levanta solo.** El `.plist` que genera GitHub no trae `KeepAlive`. Hay un supervisor interno que mantiene vivo al listener entre trabajos y cuando el runner se autoactualiza, pero si el servicio muere hay que arrancarlo a mano.
3. **Docker Desktop tiene que estar corriendo.** El runner puede aparecer en línea y aun así fallar todo despliegue si el daemon está apagado.
4. **Con la Mac apagada, un merge se queda encolado.** El workflow no falla: espera a que el runner vuelva. Es el costo de la fase A, y es justo lo que la fase B (GKE) elimina.

## Operación

```bash
cd ~/actions-runner

./svc.sh status    # PID, ultimo codigo de salida (0 = sano) y etiqueta del servicio
./svc.sh stop      # apagarlo sin desinstalarlo, deja de aceptar trabajos
./svc.sh start     # encenderlo otra vez
```

Puede estar corriendo en la Mac y aun así salir *offline* en GitHub si perdió la conexión, así que conviene revisar los dos lados:

```bash
gh api repos/valentinodepaola/microservices-demo/actions/runners \
  --jq '.runners[] | {name, status, busy, labels: [.labels[].name]}'
```

También aparece en Settings → Actions → Runners.

Los logs están en dos lugares: `~/Library/Logs/actions.runner.*/` es la salida del servicio, y `~/actions-runner/_diag/` el diagnóstico. Ahí los `Runner_*.log` son el agente escuchando y los `Worker_*.log` la ejecución de cada job — cuando un despliegue falla raro, lo que sirve es el `Worker_*`.

Cuando los builds llenen el disco:

```bash
docker system prune -a   # borra todas las imagenes sin contenedor activo,
                         # el siguiente despliegue reconstruye desde cero
```

### Verificar que la máquina está lista

[`runner-check.yaml`](../.github/workflows/runner-check.yaml) revisa que el runner encuentre `docker`, `kubectl` y `skaffold`, y que esté apuntando a `docker-desktop`. No despliega nada. Se dispara a mano desde `main`:

```bash
gh workflow run runner-check.yaml --repo valentinodepaola/microservices-demo --ref main
```

Se va a quedar detenido esperando aprobación del Environment. Eso es la regla 2 funcionando, no un error. Cuando un despliegue falle, correr esto primero separa "la máquina está mal" de "el pipeline está mal".

## Montarlo en otra máquina

```bash
# 1. Herramientas
brew install skaffold
# + Docker Desktop -> Settings -> General -> "Start Docker Desktop when you sign in"

# 2. Descargar el runner (osx-arm64 en Apple Silicon, osx-x64 en Intel)
mkdir -p ~/actions-runner && cd ~/actions-runner
curl -o actions-runner.tar.gz -L \
  https://github.com/actions/runner/releases/download/v2.337.0/actions-runner-osx-arm64-2.337.0.tar.gz
tar xzf ./actions-runner.tar.gz

# 3. Registrar. El token se pide y se usa en el mismo comando para que no quede
#    en el portapapeles ni en el historial. Dura una hora.
./config.sh \
  --url https://github.com/valentinodepaola/microservices-demo \
  --token "$(gh api -X POST repos/valentinodepaola/microservices-demo/actions/runners/registration-token --jq .token)" \
  --name <nombre-de-la-maquina> \
  --labels order-tracking \
  --work _work --unattended --replace

# 4. Instalarlo como servicio
./svc.sh install && ./svc.sh start && ./svc.sh status
```

Hace falta `gh` autenticado con una cuenta que sea admin del repositorio.

**Sobre el PATH.** Un servicio de launchd no hereda el PATH de la terminal, así que `skaffold` sería invisible para el runner. `config.sh` lo resuelve guardando una copia del PATH de la shell en `~/actions-runner/.path`, que el servicio exporta al arrancar. Si algún día el runner no encuentra una herramienta recién instalada, el archivo a revisar es ese, no el `.zshrc`. Después de editarlo hay que reiniciar el servicio.

## Quitarlo

Nada de esto es permanente:

```bash
cd ~/actions-runner
./svc.sh stop && ./svc.sh uninstall
./config.sh remove --token "$(gh api -X POST repos/valentinodepaola/microservices-demo/actions/runners/remove-token --jq .token)"
cd ~ && rm -rf ~/actions-runner
```

Eso quita el LaunchAgent, lo desregistra de GitHub y borra los archivos. Solo queda la carpeta de logs en `~/Library/Logs/`. Ojo que el token para borrar es `remove-token`, no `registration-token`.
