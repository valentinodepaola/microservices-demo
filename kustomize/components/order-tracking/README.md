# Componente `order-tracking`

Despliega la funcionalidad de seguimiento de pedidos, que es opcional ([ADR 0007](../../../docs/adr/0007-componente-opcional-con-kill-switch.md)). Sin este componente, el despliegue por defecto no levanta nada de seguimiento.

Contiene dos piezas:

- **`redis-orders`**: la instancia de Redis dedicada a los pedidos (#24). Pide contraseña (#79).
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

## La contraseña de `redis-orders`

`redis-orders` pide contraseña y `orderservice` la manda al conectarse (#79). Los dos la leen del Secret `redis-orders`, de la llave `password`. El valor no está en el repo: vive en Vault, en `boutique/redis-orders` ([`vault/README.md`](../../../vault/README.md)).

El Secret tiene que existir antes de encender el componente, sea con Kustomize, Skaffold o Helm. Si no está, los pods de `redis-orders` y `orderservice` se quedan en `CreateContainerConfigError`. Es a propósito: prefiero que no arranque a tener la base de pedidos abierta sin darme cuenta.

Para crearlo o actualizarlo con lo que hay en Vault (con el lab levantado y Vault abierto):

```bash
vault kv get -field=password boutique/redis-orders | tr -d '\n' \
  | kubectl create secret generic redis-orders --from-file=password=/dev/stdin --dry-run=client -o yaml \
  | kubectl apply -f -
```

La contraseña pasa por la tubería y nunca sale en la terminal. Sirve igual si el Secret ya existe. Para revisar que quedó completa sin imprimirla:

```bash
kubectl get secret redis-orders -o jsonpath='{.data.password}' | base64 -d | wc -c
```

Tiene que dar `48`. Si da `49`, se coló un salto de línea y Redis no va a arrancar bien.

Redis y `orderservice` leen la contraseña al arrancar, así que si la cambias no se enteran solos. Hay que reiniciarlos:

```bash
kubectl rollout restart deploy/redis-orders deploy/orderservice
```

En el clúster de AWS no hace falta crearlo a mano: desde #80, `cd-main.yaml` lo crea o lo actualiza en cada despliegue con lo que lee de Vault. Pero si cambias la contraseña en Vault, el despliegue actualiza el Secret y no reinicia a nadie: después del merge todavía tienes que correr el `rollout restart` de arriba. El comando a mano queda para un clúster local.

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

## Dónde viven los pedidos

`redis-orders` guarda los pedidos en un disco propio, el `PersistentVolumeClaim` `redis-orders-data` de 1 Gi, igual que `redis-cart` con `redis-cart-data`. Hasta #90 era un `emptyDir`, y como cada despliegue recrea el pod, cualquier merge borraba todos los pedidos.

El disco sobrevive a los despliegues, a que el pod se caiga y a apagar el lab. **Solo se pierde si se destruye la instancia** o si alguien borra el PVC. Corre con `--appendonly yes`, así que un apagado brusco pierde a lo mucho un segundo de pedidos, y con estrategia `Recreate`, para que el Redis viejo y el nuevo nunca escriban a la vez en la misma carpeta. El detalle está en [`docs/despliegue-continuo.md`](../../../docs/despliegue-continuo.md#los-redis-guardan-sus-datos-en-un-disco).

Las llaves no tienen tiempo de expiración, así que el almacén crece con cada pedido, y ahora los pedidos se acumulan entre despliegues. El 23/10/2026 cada pedido ocupaba unos 800 bytes en memoria (`MEMORY USAGE`), y el `loadgenerator` hace del orden de miles de compras por cada día de lab prendido: unos cuantos MB por día. **El primer límite que se alcanza no es el disco, es la memoria:** el contenedor tiene `limits.memory: 256Mi`, y si Redis la llena Kubernetes lo reinicia por OOM. Con ese ritmo faltan semanas de lab prendido, pero si alguna vez pasa, la salida es borrar los pedidos viejos o ponerles expiración.

## Comprobaciones

```
kubectl get pods                                      # redis-orders y orderservice en Running y 1/1
kubectl exec deploy/redis-orders -- sh -c 'REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli PING'    # PONG
kubectl exec deploy/redis-orders -- sh -c 'REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli DBSIZE'  # cuántos pedidos hay
kubectl exec deploy/redis-orders -- redis-cli DBSIZE  # sin contraseña: NOAUTH
```

`redis-cli` se autentica dentro del pod con la variable que ya tiene el contenedor. Las comillas simples hacen que `$REDIS_PASSWORD` la lea el pod y no tu terminal, así que la contraseña no sale de ahí.

El `1/1` de `orderservice` ya quiere decir que su readiness respondió `SERVING`, o sea que llega a Redis. Para preguntarle directo (la imagen es distroless y no trae shell, así que se hace desde afuera):

```
kubectl port-forward deploy/orderservice 50051:50051
grpcurl -plaintext -d '{"service":"hipstershop.OrderService"}' localhost:50051 grpc.health.v1.Health/Check
```
