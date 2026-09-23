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
go test .
```
