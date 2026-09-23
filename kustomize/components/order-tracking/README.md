# Componente `order-tracking`

Despliega la funcionalidad de seguimiento de pedidos, que es opcional ([ADR 0007](../../../docs/adr/0007-componente-opcional-con-kill-switch.md)). Sin este componente, el despliegue por defecto no levanta nada de seguimiento.

Hoy contiene `redis-orders`, la instancia de Redis dedicada a los pedidos (#24). El `orderservice`, el perfil de Skaffold y los parches al `frontend` y al `checkoutservice` los agrega #28.

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
kubectl get pods                                      # redis-orders en Running y 1/1
kubectl exec deploy/redis-orders -- redis-cli PING    # PONG
kubectl exec deploy/redis-orders -- redis-cli DBSIZE  # cuántos pedidos hay
```
