# Order Service

Guarda los pedidos en Redis y responde la consulta de seguimiento por número de rastreo. El estado del pedido no se guarda: se calcula al consultar (ver [ADR 0005](../../docs/adr/0005-estado-derivado-del-tiempo-no-almacenado.md) y [el modelo del pedido](../../docs/modelo-del-pedido.md)).

Es un componente opcional ([ADR 0007](../../docs/adr/0007-componente-opcional-con-kill-switch.md)): solo se despliega cuando el seguimiento de pedidos está activo.

## Configuración

| Variable | Obligatoria | Default | Para qué |
|----------|-------------|---------|----------|
| `REDIS_ADDR` | Sí | — | Dirección de `redis-orders`, p. ej. `redis-orders:6379`. Sin ella el servicio no arranca. |
| `PORT` | No | `50051` | Puerto del servidor gRPC. |

`ENABLE_ORDER_TRACKING` no aplica aquí: si el componente está apagado, este servicio simplemente no se despliega.

## Health check

El servicio registra el health check estándar de gRPC con dos nombres:

- `""` (vacío): **liveness**. Responde `SERVING` mientras el proceso esté vivo.
- `hipstershop.OrderService`: **readiness**. Responde `SERVING` solo cuando Redis contesta; se revisa cada 5 segundos.

Si Redis se cae, el servicio deja de estar listo pero no se reinicia, y vuelve solo en cuanto Redis regresa.

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
