# Seguimiento de pedidos — análisis del feature

Feature asignado al equipo sobre [Online Boutique](01-analisis-microservicios.md). Este documento reúne la **introspección**: qué hace hoy la aplicación, qué le falta, y qué implica construirlo.

## La frase que resume el feature

> **Online Boutique le entrega al cliente un número de rastreo que no rastrea nada. El feature de seguimiento de pedidos consiste en volverlo real.**

## El hueco raíz: la app no guarda los pedidos

Leyendo `PlaceOrder` en `src/checkoutservice/main.go`, esto es todo lo que pasa cuando alguien compra:

1. Genera un `orderID` con UUID
2. Junta los items del carrito y cotiza el envío
3. Cobra la tarjeta (`paymentservice`)
4. Manda a enviar (`shippingservice`)
5. Vacía el carrito
6. Manda el correo de confirmación (`emailservice`)
7. **Devuelve el resultado y lo olvida**

No existe base de datos de órdenes, ni historial, ni estados. El `orderID` muere en cuanto termina la petición HTTP. El único servicio con estado duradero en toda la aplicación es el carrito.

## Las cuatro evidencias

Lo interesante del caso es que **la aplicación ya finge tener seguimiento**. Cuatro hallazgos concretos, todos verificables en el código:

### 1. El número de rastreo es aleatorio

En `src/shippingservice/tracker.go`, la función `CreateTrackingId` arma el número con `math/rand`:

```
dos letras al azar + longitud de un string + 3 dígitos al azar + ... + 7 dígitos al azar
```

No se guarda en ninguna parte y no está asociado al pedido. Es un string decorativo.

### 2. El contrato gRPC no contempla consultarlo

En `protos/demo.proto`, `ShippingService` expone únicamente dos operaciones:

- `GetQuote(GetQuoteRequest) → GetQuoteResponse`
- `ShipOrder(ShipOrderRequest) → ShipOrderResponse`

Y `ShipOrderResponse` contiene un solo campo: `tracking_id`. Sin estado, sin fechas, sin historial. **No existe ningún RPC de consulta de rastreo.**

### 3. Pero la interfaz sí se lo promete al usuario

En `src/frontend/templates/order.html`, la pantalla de confirmación muestra un campo **"Tracking #"** con ese número aleatorio.

### 4. Y ahí se acaba el camino

Revisando las rutas registradas en `src/frontend/main.go`: existen `/`, `/product/{id}`, `/cart`, `/cart/checkout`, `/setCurrency`, `/assistant`, `/logout`... y **ninguna ruta de pedidos ni de rastreo**. No hay `/orders`, no hay `/track/{id}`.

La página de confirmación es un callejón sin salida: el usuario recibe un número de rastreo y no tiene absolutamente dónde escribirlo.

## Qué implica construirlo

Piezas que el feature obliga a resolver:

- **Persistencia de pedidos** — sería el primer servicio con estado duradero real de la app
- **Un modelo de estados** — creado → pagado → preparando → en tránsito → entregado. No existe hoy; hay que diseñarlo desde cero
- **`tracking_id` deja de ser aleatorio** y pasa a ser una llave consultable ligada al pedido
- **Consulta por dos vías** — por número de rastreo (anónima) y por usuario ("mis pedidos"); son casos de uso distintos
- **`checkoutservice` debe registrar el pedido** al completarlo, en lugar de olvidarlo
- **Contrato gRPC nuevo** — extender `protos/demo.proto` con el servicio y sus mensajes
- **`frontend` necesita rutas y vistas nuevas** — pantalla de rastreo y probablemente historial
- **Todo el ciclo DevOps** — Dockerfile, componente de Kustomize, `skaffold.yaml`, Helm chart, documentación (ver el checklist de 8 pasos en [online-boutique-arquitectura](01-analisis-microservicios.md))

## Decisión de diseño abierta

**¿Quién hace avanzar los estados del pedido?**

Nadie envía nada de verdad en esta app: `shippingservice` no habla con ninguna paquetería. Las opciones son simular la progresión por tiempo transcurrido desde la compra, o tener un proceso que actualice los estados periódicamente.

Vale la pena justificar la elección por escrito en la propuesta — es exactamente el tipo de decisión que una fase de introspección debe dejar documentada.

## Restricción a respetar

El feature debe ser **opt-in** (componente de Kustomize), por los requisitos de producto del repo. Ver [online-boutique-arquitectura](01-analisis-microservicios.md).

## Páginas relacionadas

- [online-boutique-arquitectura](01-analisis-microservicios.md) — el sistema sobre el que se construye
- [propuesta-de-proyecto](03-propuesta-de-proyecto.md) — el alcance y las decisiones de diseño derivadas de este análisis
- Fase 1 - Introspeccion — entregables de la fase actual
