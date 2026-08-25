# Propuesta de proyecto — Seguimiento de pedidos en Online Boutique

Documento principal de la Fase 1. Define **qué vamos a construir** y **cómo lo vamos a construir**, recorriendo el ciclo DevOps completo. Reemplaza a propuesta-comercial-devops.

| | |
|---|---|
| **Equipo** | Alessia, Valentino |
| **Aplicación base** | Online Boutique — `GoogleCloudPlatform/microservices-demo` |
| **Repositorio de trabajo** | [`valentinodepaola/microservices-demo`](https://github.com/valentinodepaola/microservices-demo) |
| **Funcionalidad a desarrollar** | Seguimiento de pedidos |
| **Metodología** | DevOps |
| **Estrategia de ramificación** | GitHub Flow — ver [estrategia-de-ramificacion](04-estrategia-de-ramificacion.md) |
| **Ambiente de despliegue** | Clúster local *kind* sobre Docker Desktop |

---

## Resumen ejecutivo

Online Boutique le entrega hoy al cliente un número de rastreo que no rastrea nada: se genera al azar, no se guarda en ninguna parte y la aplicación no ofrece ninguna pantalla donde consultarlo.

Este proyecto construye **`orderservice`**, un microservicio nuevo que persiste los pedidos y permite consultarlos por número de rastreo, más las pantallas del frontend que hoy no existen. La funcionalidad se entrega como **componente opcional de Kustomize**, de modo que se enciende y se apaga sin alterar el despliegue por defecto de la tienda.

El objetivo de la materia no es la funcionalidad en sí, sino **el proceso que la lleva desde un commit hasta un clúster corriendo**: planear, codificar, construir, probar, desplegar y observar, cada etapa con su herramienta y su evidencia.

---

## 1. Qué vamos a hacer

### 1.1 El problema, verificado en el código

La aplicación **finge tener seguimiento de pedidos**. Cuatro hallazgos, todos comprobables en el repositorio:

| #   | Hallazgo                                                                                | Dónde se verifica                   |
| --- | --------------------------------------------------------------------------------------- | ----------------------------------- |
| 1   | El número de rastreo se arma con `math/rand` y no se guarda en ningún lado              | `src/shippingservice/tracker.go`    |
| 2   | El contrato gRPC solo expone `GetQuote` y `ShipOrder`; no existe ningún RPC de consulta | `protos/demo.proto:107-110`         |
| 3   | Aun así, la pantalla de confirmación le muestra al cliente un campo *"Tracking #"*      | `src/frontend/templates/order.html` |
| 4   | No existe ninguna ruta de pedidos ni de rastreo en el frontend                          | `src/frontend/main.go:150-163`      |

La causa raíz está una capa más abajo: **la aplicación no guarda los pedidos**. En `PlaceOrder` (`src/checkoutservice/main) el orquestador arma un `OrderResult`, lo manda por correo, lo devuelve en la respuesta HTTP **y lo olvida**. No hay base de datos de pedidos, ni historial, ni estados. El único servicio con estado duradero en toda la plataforma es el carrito.

El resultado para el cliente es una promesa rota: recibe un número de rastreo y no tiene absolutamente dónde escribirlo.

> Análisis extendido en [seguimiento-de-pedidos-analisis](02-definicion-problema-objetivo.md).

### 1.2 La solución

Un microservicio nuevo, **`orderservice`**, escrito en Go, con estado persistente, que cumple dos responsabilidades:

- **Registrar** cada pedido en el momento en que `checkoutservice` lo completa.
- **Consultar** un pedido por su número de rastreo y devolver su estado actual.

Más las piezas que lo hacen visible y desplegable: los RPC nuevos en el contrato compartido, las rutas y vistas del frontend, y el componente de Kustomize que lo activa.

### 1.3 Alcance

**Dentro del alcance**

- Microservicio `orderservice` en Go, siguiendo la convención de los servicios existentes.
- Persistencia de pedidos en Redis, con el mismo patrón que usa `cartservice`.
- Modelo de estados del pedido y la lógica que lo hace avanzar.
- Extensión de `protos/demo.proto` con el servicio `OrderService` y sus mensajes.
- Registro del pedido desde `checkoutservice` al completar la compra.
- Ruta y vista de rastreo en el frontend, consultable por número de rastreo.
- Enlace desde la pantalla de confirmación hacia la pantalla de rastreo — cerrar el callejón sin salida del hallazgo #3.
- `Dockerfile`, componente de Kustomize, entrada en `skaffold.yaml`, plantillas de Helm y documentación: los 8 pasos de `docs/adding-new-microservice.md`.
- Pruebas unitarias del servicio nuevo, integradas al CI existente.
- Instrumentación con OpenTelemetry, igual que el resto de los servicios.

**Fuera del alcance** — declarado explícitamente para que no se discuta después

- **Autenticación de usuarios.** La consulta es anónima, por número de rastreo. La app no tiene login real: usa una *cookie* de sesión anónima.
- **Historial "mis pedidos" por usuario.** Depende de identidad de usuario; queda como extensión posterior.
- **Integración con paqueterías reales.** Nadie envía nada de verdad en esta aplicación; los estados se simulan.
- **Cancelación, devolución o modificación de pedidos.**
- **Notificaciones de cambio de estado por correo.**
- **Despliegue en la nube.** El proyecto se despliega en un clúster local; ver sección 3.5.

### 1.4 Criterios de aceptación

La funcionalidad se considera terminada cuando:

1. Al completar una compra, el pedido queda registrado y es recuperable por su número de rastreo.
2. La pantalla de confirmación enlaza a la pantalla de rastreo del pedido recién hecho.
3. Consultar un número de rastreo válido devuelve el estado del pedido, sus artículos y la fecha de compra.
4. Consultar un número inexistente devuelve un mensaje claro, no un error de servidor.
5. El estado del pedido avanza de forma observable con el paso del tiempo.
6. `kubectl apply -k .` sin el componente activo despliega la tienda **exactamente igual que hoy**, sin `orderservice`.
7. Con el componente activo, la tienda despliega en un clúster *kind* limpio y la funcionalidad opera de extremo a extremo.
8. El CI pasa en verde sobre el Pull Request: pruebas unitarias, `kustomize build` y lint del Helm chart.

---

## 2. Cómo lo vamos a construir

Cada decisión de esta sección está tomada y justificada. No quedan puntos abiertos.

### 2.1 Servicio nuevo, no extensión de uno existente

La alternativa era meter la persistencia y la consulta dentro de `shippingservice`, que es quien ya genera el número de rastreo. Se descarta por tres razones:

- **Responsabilidad distinta.** `shippingservice` cotiza y despacha; guardar el historial de pedidos es otra cosa. Mezclarlas produce exactamente el acoplamiento que una arquitectura de microservicios existe para evitar.
- **Estado.** `shippingservice` hoy no tiene estado. Convertirlo en un servicio con estado cambia su naturaleza operativa: pasa a necesitar respaldo, arranque ordenado y una dependencia externa.
- **Valor para la materia.** Un servicio nuevo obliga a recorrer los 8 pasos de `docs/adding-new-microservice.md` — imagen propia, manifiestos, `skaffold`, Helm, documentación. Es decir, ejercita el ciclo DevOps completo, que es lo que el proyecto evalúa. Una extensión no construye ninguna pieza de despliegue nueva.

El repositorio ya trae el molde a copiar: `shoppingassistantservice` es un servicio agregado después del diseño original y entregado como componente opcional. Se sigue ese patrón al pie de la letra.

### 2.2 El número de rastreo se conserva aleatorio — lo que cambia es que se guarda

Una precisión que simplifica el trabajo: **el defecto nunca fue que el número fuera aleatorio, sino que no se persistía**. Un identificador aleatorio persistido es perfectamente válido como llave de consulta.

Por lo tanto **no se modifica `ShipOrder` ni `ShipOrderResponse`**. `shippingservice` sigue generando el número igual que hoy; `checkoutservice` lo recibe y se lo entrega a `orderservice` junto con el resto del pedido. El contrato existente queda intacto y el radio de impacto del cambio se reduce a lo mínimo.

### 2.3 Persistencia en Redis, con el patrón que ya existe

Se despliega una instancia de Redis dedicada (`redis-orders`), replicando lo que `kustomize/base/cartservice.yaml` hace con `redis-cart`: imagen `redis:alpine`, volumen `emptyDir`, servicio interno del clúster.

Por qué Redis y no una base relacional:

- **Ya está en el repositorio.** No introduce tecnología nueva ni pasos nuevos al *quickstart*.
- **El acceso es por llave.** La consulta principal es "dame el pedido con este número de rastreo" — un acceso por llave directa, que es justamente para lo que Redis sirve.
- **Corre gratis en local.** No requiere Cloud SQL, Spanner ni AlloyDB, y respeta la restricción de que el despliegue por defecto funcione en un clúster *kind* sin nube.

Se documenta explícitamente que `emptyDir` **no sobrevive al reinicio del pod**, igual que el carrito hoy. Es aceptable en una aplicación de demostración y es una limitación consciente, no un descuido.

### 2.4 Modelo de estados y quién los hace avanzar

Estados del pedido:

```
CREADO → PAGADO → EN_PREPARACIÓN → EN_TRÁNSITO → ENTREGADO
```

La pregunta real de diseño es **quién los hace avanzar**, porque en esta aplicación nadie envía nada de verdad. Las opciones eran un proceso en segundo plano que actualice los registros periódicamente, o derivar el estado del tiempo transcurrido.

**Decisión: el estado se deriva del tiempo transcurrido desde la compra, calculado en el momento de la consulta.**

- No agrega un proceso más que desplegar, vigilar y depurar.
- Es una **función pura** de dos entradas — fecha de compra y momento actual — así que se prueba unitariamente sin levantar Redis ni el clúster.
- No hay escrituras en segundo plano, así que no hay condiciones de carrera sobre el registro del pedido.
- La duración de cada estado se configura por variable de entorno, de modo que en una demostración el pedido pueda recorrer el ciclo completo en minutos en lugar de días.

### 2.5 El contrato gRPC nuevo

Se agrega a `protos/demo.proto` un servicio propio, sin tocar los existentes:

```
service OrderService {
    rpc RecordOrder(RecordOrderRequest) returns (RecordOrderResponse) {}
    rpc GetOrderByTrackingId(GetOrderByTrackingIdRequest) returns (Order) {}
}
```

`Order` reutiliza los tipos que el contrato ya define — `OrderItem`, `Address`, `Money` — y agrega estado y fecha de compra. Reutilizar en vez de duplicar mantiene un solo lenguaje de dominio en el contrato compartido.

### 2.6 El punto de integración en checkoutservice

Una sola llamada nueva en `PlaceOrder`, justo después de armar el `OrderResult` y antes de responder (`src/checkoutservice/main.go:265`).

**El registro no puede tumbar la compra.** Si `orderservice` no responde, se registra la advertencia en el log y la compra se completa igual — exactamente el mismo criterio que el repositorio ya aplica al envío del correo de confirmación, que también falla sin abortar el pedido. Cuando el componente está apagado, `checkoutservice` simplemente no tiene a quién llamar y se comporta como hoy.

### 2.7 El frontend

Dos cambios, ambos condicionados a que la funcionalidad esté activa:

| Cambio | Detalle |
|---|---|
| Ruta nueva | `/orders/track/{trackingId}` — pantalla de rastreo con estado, artículos y fecha |
| Cambio en vista existente | En `order.html`, el campo *"Tracking #"* deja de ser texto muerto y pasa a ser enlace a la pantalla de rastreo |

El interruptor sigue el patrón exacto de `ENABLE_ASSISTANT` (`src/frontend/handlers.go:48`): una variable de entorno que el componente de Kustomize inyecta al Deployment del frontend. Sin la variable, las rutas no se registran y la interfaz se ve idéntica a la actual.

### 2.8 Entrega como componente opcional

`docs/product-requirements.md` exige que todo cambio preserve el despliegue por defecto sobre *kind*, no complique el recorrido de demostración y no agregue pasos obligatorios al *quickstart*.

Por eso la funcionalidad completa vive en `kustomize/components/order-tracking/`, siguiendo la estructura de `kustomize/components/shopping-assistant/`: un `Component` que aporta los recursos nuevos — `orderservice` y `redis-orders` — y **parcha** el Deployment del frontend para encender la variable de entorno. Nada se agrega a `kustomize/base/`.

---

## 3. Cómo vamos a trabajar

El proyecto recorre el ciclo DevOps completo. Cada etapa tiene herramienta asignada y **produce una evidencia verificable**, que es lo que alimenta portafolio-de-evidencias.

| Etapa | Qué se hace | Herramienta | Evidencia que genera |
|---|---|---|---|
| Planear | Descomponer el trabajo en tarjetas con criterios de aceptación | Tablero Kanban | Tablero con el flujo de tarjetas |
| Codificar | Ramas de vida corta, Pull Request revisado | Git + GitHub Flow | Historial de ramas y PR revisados |
| Construir | Imagen de contenedor por servicio | Docker + Skaffold | Imágenes construidas, log de build |
| Probar | Pruebas unitarias y validación de manifiestos | GitHub Actions | Checks en verde sobre cada PR |
| Desplegar | Aplicar al clúster local | kind + Kustomize + Skaffold | Tienda corriendo con la funcionalidad |
| Observar | Trazas distribuidas de la petición entre servicios | OpenTelemetry | Traza que cruza frontend → orderservice |

### 3.1 Planear

Cada elemento del alcance de la sección 1.3 se convierte en una tarjeta del tablero, con criterios de aceptación tomados de la sección 1.4. Ninguna tarjeta entra a *en progreso* sin criterio de aceptación escrito: es lo que hace que "terminado" signifique lo mismo para los dos integrantes.

### 3.2 Codificar

**GitHub Flow**, ya definido en [estrategia-de-ramificacion](04-estrategia-de-ramificacion.md): `main` siempre desplegable, ramas de vida corta, nada entra sin Pull Request revisado por el otro integrante.

La regla se hace cumplir por la plataforma, no por disciplina: sobre `main` se activa protección de rama exigiendo Pull Request y al menos una aprobación.

### 3.3 Construir

`skaffold.yaml` construye las imágenes de los servicios. Agregar `orderservice` a la lista de artefactos es el paso 6 del checklist oficial, y a partir de ahí `skaffold dev` reconstruye y redespliega el servicio ante cada cambio de código.

### 3.4 Probar

Tres niveles, todos automatizados sobre el Pull Request:

- **Unitarias.** El workflow `ci-pr.yaml` ya corre `go test` sobre `shippingservice`, `productcatalogservice` y `frontend/validator`. Se agrega `orderservice` a esa lista. La máquina de estados de la sección 2.4, al ser función pura, se prueba aquí de forma directa.
- **Manifiestos.** `kustomize-build-ci.yaml` valida que el componente nuevo construya sin errores, y `helm-chart-ci.yaml` valida las plantillas de Helm.
- **Extremo a extremo, manual.** Recorrido de compra en el clúster local: producto → carrito → checkout → rastreo.

### 3.5 Desplegar — y por qué no heredamos el pipeline

**Hallazgo importante del repositorio base:** los workflows de despliegue que vienen en el fork **no pueden funcionar para nosotros**. `deploy-pr.yaml` y `ci-main.yaml` están cableados a la infraestructura de Google: apuntan a un proveedor de identidad de un proyecto de GCP ajeno y a un clúster llamado `prs-gke-cluster` que no controlamos. Sin esas credenciales, esos workflows fallan siempre.

Lo que **sí** se hereda y funciona: las pruebas unitarias, la validación de Kustomize, el lint de Helm y la validación de Terraform. Ese es el CI real del proyecto.

En consecuencia, el despliegue es **local sobre un clúster kind en Docker Desktop**, con Skaffold. La decisión no es una limitación asumida sino la lectura correcta del terreno: un clúster gestionado en la nube exige cuenta con créditos y credenciales federadas, y el propio repositorio exige que el despliegue por defecto funcione sin nube. El procedimiento ya está verificado y documentado en como-levantar-online-boutique.

Los workflows de despliegue heredados se desactivan explícitamente en el fork, y esa desactivación se documenta como decisión. Un pipeline rojo de forma permanente es peor que no tener pipeline: enseña al equipo a ignorar el CI.

*Extensión posible en fase posterior:* levantar el clúster *kind* dentro del propio GitHub Actions para desplegar la tienda completa en cada Pull Request, sin depender de ninguna nube.

### 3.6 Observar

Los servicios ya están instrumentados con OpenTelemetry, y `orderservice` se instrumenta igual. La evidencia buscada es una **traza distribuida** que cruce del frontend al servicio nuevo, demostrando que la funcionalidad no es una caja opaca sino una pieza observable del sistema. Ver opentelemetry.

---

## 4. Por qué DevOps, para este proyecto en concreto

Online Boutique tiene once servicios en seis lenguajes que se comunican por un contrato compartido. Esa arquitectura se eligió para que la plataforma pudiera evolucionar por partes. Pero **la arquitectura sola no entrega ese beneficio**: sin un proceso automatizado, la plataforma paga el costo de la complejidad distribuida sin cobrar la ventaja que la justifica.

Este proyecto lo demuestra con un caso concreto en lugar de un argumento abstracto. La funcionalidad de seguimiento toca un contrato compartido, un orquestador, la interfaz y el despliegue. Sin DevOps, ese cambio es una coordinación manual de alto riesgo. Con DevOps:

| Práctica | Qué resuelve **en este cambio específico** |
|---|---|
| Integración continua | Tocar `demo.proto` afecta a servicios que nadie editó. El CI lo detecta en minutos, no en la semana de entrega |
| Entrega continua | El servicio nuevo se despliega con el mismo comando que los otros once; agregar una pieza deja de ser un proyecto de coordinación |
| Configuración como código | El componente de Kustomize hace que la funcionalidad se encienda y se apague de forma reproducible y reversible |
| Observabilidad | Cuando el rastreo falle, la traza dice si fue el frontend, `orderservice` o Redis — con datos, no con suposiciones |
| Ramificación disciplinada | Dos personas trabajando sobre el mismo repositorio sin pisarse, con `main` siempre desplegable |

El argumento de negocio se sostiene solo: el seguimiento de pedidos reduce consultas a atención a clientes y sube la confianza en la compra, y DevOps es lo que permite entregarlo de forma incremental y reversible en lugar de en una liberación grande y riesgosa.

---

## 5. Plan de trabajo

| Etapa | Trabajo | Entregable |
|---|---|---|
| 1 — Planeación | Tablero Kanban, propuesta, estrategia de ramificación, protección de `main` | Esta propuesta y el repositorio configurado |
| 2 — Contrato y servicio | `demo.proto` extendido, `orderservice` con persistencia y máquina de estados, pruebas unitarias | Servicio construido y probado en CI |
| 3 — Integración y despliegue | Registro desde `checkoutservice`, rutas del frontend, componente de Kustomize, Helm, `skaffold` | Funcionalidad corriendo de extremo a extremo en kind |
| 4 — Observabilidad y cierre | Instrumentación, traza de evidencia, documentación, portafolio | Portafolio de evidencias completo |

**Reparto de responsabilidades**

| Área | Responsable |
|---|---|
| Tablero Kanban y seguimiento de tarjetas | Alessia |
| Repositorio, ramificación y configuración de CI | Valentino |
| Desarrollo de `orderservice` y contrato gRPC | Valentino |
| Frontend y componente de Kustomize | Por definir con el equipo |
| Documentación y portafolio de evidencias | Compartido |

Toda integración a `main` requiere revisión del otro integrante, sin importar quién escribió el código.

---

## 6. Cómo se mide el éxito

**De la funcionalidad:** los ocho criterios de aceptación de la sección 1.4, verificables uno por uno.

**Del proceso** — los cuatro indicadores estándar de la industria, aterrizados a este proyecto:

| Indicador | Cómo se mide aquí |
|---|---|
| Frecuencia de despliegue | Pull Requests integrados a `main` por semana |
| Tiempo de entrega de un cambio | Horas entre el primer commit de una rama y su integración |
| Tasa de fallo de cambios | Porcentaje de Pull Requests que rompen el CI o el despliegue local |
| Tiempo de restauración | Minutos entre detectar `main` roto y tenerlo desplegable otra vez |

Los cuatro se leen directamente del historial de GitHub, sin instrumentación adicional.

---

## 7. Riesgos

| Riesgo | Probabilidad | Mitigación |
|---|---|---|
| El pipeline de despliegue heredado no funciona en el fork | **Ya ocurrió** | Confirmado en la sección 3.5. Se desactivan esos workflows y se despliega en local |
| Recursos de la máquina insuficientes para doce servicios en kind | Media | Desplegar con el componente `without-loadgenerator` durante el desarrollo |
| Un cambio en `demo.proto` rompe servicios que nadie tocó | Media | El contrato se extiende con un servicio nuevo, sin modificar los mensajes existentes (sección 2.2) |
| El registro de pedidos hace fallar el checkout | Baja | El registro no aborta la compra; falla con advertencia en el log (sección 2.6) |
| La funcionalidad desestabiliza el despliegue por defecto | Baja | Todo vive en un componente opcional; `base` no se toca (sección 2.8) |
| Trabajo bloqueado entre los dos integrantes | Media | Tarjetas con criterio de aceptación y ramas de vida corta; nada se integra sin revisión |

---

## Páginas relacionadas

- instrucciones-fase-1 — la actividad y los criterios de evaluación
- Fase 1 - Introspeccion — estado de los entregables
- [estrategia-de-ramificacion](04-estrategia-de-ramificacion.md) — GitHub Flow y las reglas del equipo
- [online-boutique-arquitectura](01-analisis-microservicios.md) — el sistema sobre el que se construye
- [seguimiento-de-pedidos-analisis](02-definicion-problema-objetivo.md) — análisis técnico del hueco a resolver
- como-levantar-online-boutique — procedimiento verificado de despliegue local
- portafolio-de-evidencias — recopilación del proceso
- propuesta-comercial-devops — versión anterior, reemplazada por este documento
