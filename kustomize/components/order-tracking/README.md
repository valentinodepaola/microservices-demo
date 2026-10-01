# Componente `order-tracking`

Despliega la funcionalidad de seguimiento de pedidos, que es opcional ([ADR 0007](../../../docs/adr/0007-componente-opcional-con-kill-switch.md)). Sin este componente, el despliegue por defecto no levanta nada de seguimiento.

Contiene dos piezas:

- **`redis-orders`**: la instancia de Redis dedicada a los pedidos (#24).
- **`orderservice`**: el servicio de pedidos, con su Deployment, su Service `ClusterIP` en el 50051 y su ServiceAccount (#26). Solo lo llaman `checkoutservice` y `frontend` desde dentro del clúster, así que no se expone hacia afuera.

Además, el componente **parcha** dos Deployments y ajusta un tercero (#28):

- **`frontend`** y **`checkoutservice`**: les inyecta `ENABLE_ORDER_TRACKING=true` y `ORDER_SERVICE_ADDR=orderservice:50051`. Sin el componente no tienen esas variables y se comportan como siempre.
- **`orderservice`**: le pone las duraciones de demo (`30s`, `60s`, `90s`, `120s`; el ciclo completo dura 5 minutos). Sin este parche usaría los defaults de modo revisión, que suman casi un día ([modelo del pedido §5](../../../docs/modelo-del-pedido.md)).

## Dónde se enciende

| Ruta | Apagado (default) | Encendido |
|---|---|---|
| Kustomize | `kubectl apply -k kustomize/` | descomentar `components/order-tracking` en `kustomize/kustomization.yaml` |
| Skaffold | `skaffold run` | `skaffold run -p order-tracking` |
| Helm | `helm install …` | `--set orderTracking.enabled=true` |
| CD (`cd-main.yaml`) | — | siempre encendido: despliega con `-p order-tracking` |

El perfil de Skaffold no apunta a este componente directo: renderiza `kustomize/overlays/order-tracking/`, que es `kubernetes-manifests/` con el componente encima. Los parches necesitan a `frontend` y `checkoutservice` en la misma pasada de kustomize, y `skaffold.yaml` construye `kubernetes-manifests/`, no `kustomize/`.

## Cómo sabe Kubernetes si `orderservice` está sano

Las dos sondas le preguntan al mismo health check de gRPC, pero de forma distinta:

| Sonda | Pregunta por | Si Redis se cae |
|---|---|---|
| readiness | `hipstershop.OrderService` | Responde `NOT_SERVING`: el pod deja de recibir tráfico, pero sigue vivo |
| liveness | nada (el proceso) | Sigue en `SERVING`: el pod no se reinicia, porque reiniciarlo no arreglaría a Redis |

Cuando Redis vuelve, el servicio lo detecta en unos 5 segundos y vuelve a `SERVING` solo.

## Uso

En el `kustomization.yaml` de la raíz:

```yaml
components:
- components/order-tracking
```

Y luego:

```
kubectl apply -k .
```

## Por qué un Redis aparte de `redis-cart`

Los carritos se vacían al comprar y los pedidos tienen que sobrevivir a eso. Compartir instancia también haría que un `FLUSHALL` de depuración de carritos se llevara los pedidos por delante.

## ⚠️ Los pedidos no sobreviven al pod

`redis-orders` usa `emptyDir`, igual que `redis-cart`. **Los pedidos viven mientras viva el pod:** si se elimina o se recrea, se pierden todos, y un número de rastreo que antes funcionaba empieza a responder `NotFound`.

Es aceptable porque la demostración ocurre dentro de una sola sesión, pero esto **no es almacenamiento duradero**. Pasar a un `PersistentVolumeClaim` o a un Redis gestionado es evolución posterior, fuera del alcance de la Fase 2.

Las llaves tampoco tienen tiempo de expiración: un pedido registrado se puede consultar durante toda la vida del pod. El almacén crece mientras el pod viva, lo cual es acotado justamente porque el pod no es permanente.

## Comprobaciones

```
kubectl get pods                                      # redis-orders y orderservice en Running y 1/1
kubectl exec deploy/redis-orders -- redis-cli PING    # PONG
kubectl exec deploy/redis-orders -- redis-cli DBSIZE  # cuántos pedidos hay
```

El `1/1` de `orderservice` ya quiere decir que su readiness respondió `SERVING`, o sea que llega a Redis. Para preguntarle directo (la imagen es distroless y no trae shell, así que se hace desde afuera):

```
kubectl port-forward deploy/orderservice 50051:50051
grpcurl -plaintext -d '{"service":"hipstershop.OrderService"}' localhost:50051 grpc.health.v1.Health/Check
```
