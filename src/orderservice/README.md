# Order Service

Guarda los pedidos en Redis y responde la consulta de seguimiento por número de rastreo. El estado del pedido no se guarda: se calcula al consultar (ver [ADR 0005](../../docs/adr/0005-estado-derivado-del-tiempo-no-almacenado.md) y [el modelo del pedido](../../docs/modelo-del-pedido.md)).

Es un componente opcional ([ADR 0007](../../docs/adr/0007-componente-opcional-con-kill-switch.md)): solo se despliega cuando el seguimiento de pedidos está activo.

## Configuración

| Variable | Obligatoria | Default | Para qué |
|----------|-------------|---------|----------|
| `REDIS_ADDR` | Sí | — | Dirección de `redis-orders`, p. ej. `redis-orders:6379`. Sin ella el servicio no arranca. |
| `PORT` | No | `50051` | Puerto del servidor gRPC. |
| `ORDER_CREATED_DURATION` | No | `5m` | Cuánto dura la etapa Creado. |
| `ORDER_PAID_DURATION` | No | `30m` | Cuánto dura la etapa Pagado. |
| `ORDER_PREPARING_DURATION` | No | `3h` | Cuánto dura la etapa En preparación. |
| `ORDER_IN_TRANSIT_DURATION` | No | `20h` | Cuánto dura la etapa En tránsito. |

Las duraciones usan el formato de Go (`30s`, `5m`, `3h`), tienen que ser de al menos un segundo y en segundos cerrados. Si una viene con un valor inválido, el servicio no arranca. Los defaults son el modo revisión: el ciclo completo dura casi un día. Para la demostración se sobrescriben desde Kustomize (#28).

Como el estado se calcula, cambiar una duración cambia el estado de **todos** los pedidos ya guardados. Es esperado: no hay nada guardado que migrar.

`ENABLE_ORDER_TRACKING` no aplica aquí: si el componente está apagado, este servicio simplemente no se despliega.

## Health check

El servicio registra el health check estándar de gRPC con dos nombres:

- `""` (vacío): **liveness**. Responde `SERVING` mientras el proceso esté vivo.
- `hipstershop.OrderService`: **readiness**. Responde `SERVING` solo cuando Redis contesta; se revisa cada 5 segundos.

Si Redis se cae, el servicio deja de estar listo pero no se reinicia, y vuelve solo en cuanto Redis regresa.

## Persistencia

Cada pedido se guarda como un solo valor en `redis-orders`, bajo la llave de su número de rastreo y en JSON generado desde el proto ([modelo del pedido](../../docs/modelo-del-pedido.md)). La escritura usa `SET NX`, así que registrar dos veces el mismo número no duplica ni sobrescribe nada.

El estado del pedido no se guarda: se calcula al consultar, a partir de la fecha de compra y de las duraciones de abajo. La respuesta incluye además la línea de tiempo con el momento en que empieza cada etapa.

> ⚠️ **Los pedidos no sobreviven al pod.** `redis-orders` usa `emptyDir`, así que si el pod se recrea se pierden todos y un número de rastreo que antes funcionaba empieza a responder `NotFound`. Es aceptable para una demostración de una sesión, pero no es almacenamiento duradero. Las llaves tampoco expiran: ver el [README del componente](../../kustomize/components/order-tracking/README.md).

## Local

Con un Redis corriendo en `localhost:6379`:

```
REDIS_ADDR=localhost:6379 go run .
```

Para probarlo:

```
grpcurl -plaintext localhost:50051 grpc.health.v1.Health/Check
grpcurl -plaintext -d '{"service":"hipstershop.OrderService"}' localhost:50051 grpc.health.v1.Health/Check
grpcurl -plaintext localhost:50051 list
```

## Recorrido de punta a punta

Comprueba el camino completo en el clúster local: una compra en la tienda queda guardada en `redis-orders` y se puede consultar por su número de rastreo. Mientras no exista la pantalla de seguimiento (#32), la consulta se hace con `grpcurl` a través de `kubectl port-forward`. Necesita [#12](https://github.com/valentinodepaola/microservices-demo/issues/12) (checkout registra el pedido) y [#28](https://github.com/valentinodepaola/microservices-demo/issues/28) (el perfil que enciende el componente).

**1. Levantar la tienda con el seguimiento encendido.** Desde la raíz del repo:

```
skaffold run -p order-tracking
kubectl wait --for=condition=available --timeout=300s deploy/orderservice deploy/redis-orders deploy/frontend deploy/checkoutservice
```

**2. Hacer una compra.** Abrir la tienda (`kubectl port-forward svc/frontend 8080:80` y entrar a http://localhost:8080), agregar un producto al carrito y pagar con los datos de prueba que ya trae el formulario. En la confirmación, copiar el **Tracking #**.

**3. Confirmar que el pedido llegó a Redis.** La llave es el número de rastreo:

```
kubectl exec deploy/redis-orders -- redis-cli EXISTS <TRACKING_ID>
```

Responde `1` si el pedido está guardado.

**4. Consultar el pedido.** En otra terminal:

```
kubectl port-forward svc/orderservice 50051:50051
```

Y luego:

```
grpcurl -plaintext -d '{"tracking_id":"<TRACKING_ID>"}' \
  localhost:50051 hipstershop.OrderService/GetOrderByTrackingId
```

La respuesta trae `shippingTrackingId`, `status`, los `items` que se compraron, `total` y la `timeline` con las cinco etapas. Con las duraciones de demo (30s / 60s / 90s / 120s), si se repite la consulta unos minutos después, `status` avanza de `ORDER_STATUS_CREATED` hasta `ORDER_STATUS_DELIVERED` en unos 5 minutos.

**5. Caso de error.** Un número que no existe responde `NotFound`:

```
grpcurl -plaintext -d '{"tracking_id":"NO-EXISTE"}' \
  localhost:50051 hipstershop.OrderService/GetOrderByTrackingId
```

> Si el pod de `redis-orders` se reinicia a mitad del recorrido, el pedido se pierde (`emptyDir`, ver *Persistencia*) y la consulta responde `NotFound`. No es un bug: hay que hacer otra compra.

### Resultado

| Paso | Esperado | Obtenido |
|------|----------|----------|
| 1. Despliegue con `-p order-tracking` | `orderservice` y `redis-orders` disponibles | |
| 2. Compra | La confirmación muestra el Tracking # | |
| 3. `EXISTS` en `redis-orders` | `1` | |
| 4. `GetOrderByTrackingId` | Estado, productos y total de la compra | |
| 4. Repetir a los ~5 min | `ORDER_STATUS_DELIVERED` | |
| 5. Número inexistente | `NotFound` | |

Probado el AAAA-MM-DD sobre el commit `<SHA>`.

## Regenerar los stubs

Desde `src/orderservice`:

```
./genproto.sh
```

## Build

Desde `src/orderservice`:

```
docker build ./
```

## Test

```
go test ./...
```

No necesitan un Redis real. Los manifiestos se prueban en CI: `kustomize/tests/order-tracking` (Kustomize con el componente) y el paso *helm template order tracking* de `helm-chart-ci.yaml` (Helm con `orderTracking.enabled=true`).
