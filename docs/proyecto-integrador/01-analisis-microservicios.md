# Online Boutique — arquitectura del repositorio base

Repositorio base del proyecto: fork de [`GoogleCloudPlatform/microservices-demo`](https://github.com/GoogleCloudPlatform/microservices-demo). Fork propio: [`valentinodepaola/microservices-demo`](https://github.com/valentinodepaola/microservices-demo).

## Qué es

**Online Boutique** es la aplicación demo oficial de Google Cloud para enseñar microservicios. Es una tienda en línea deliberadamente simple — ver productos, agregar al carrito, hacer checkout — construida con **11 microservicios en 6 lenguajes**, que se comunican por **gRPC** y se despliegan en **Kubernetes**.

Lo importante: **no es un producto real, es material didáctico**. El propio repo lo declara en `docs/purpose.md` — existe para demostrar GKE, Kubernetes y el herramental que se usa alrededor. La tienda es la excusa; lo que se enseña es la infraestructura.

## Por qué sirve para una materia de DevOps

El repo no trae solo código de microservicios: trae **todo el aparato DevOps ya montado**.

| Ruta en el repo | Para qué sirve |
|---|---|
| `src/*/Dockerfile` | Containerización de cada servicio |
| `protos/demo.proto` | El contrato gRPC compartido entre servicios |
| `kubernetes-manifests/`, `kustomize/`, `helm-chart/` | Tres formas distintas de declarar el despliegue |
| `skaffold.yaml` | Build + deploy local iterativo |
| `cloudbuild.yaml` | Pipeline de CI |
| `istio-manifests/` | Service mesh: tráfico y observabilidad |
| `terraform/` | Infraestructura como código |
| `src/loadgenerator/` | Pruebas de carga con Locust |

Un repo normal deja practicar *codificar*. Este obliga a pasar por el **ciclo DevOps completo**: planear → codificar → construir imagen → probar → versionar → desplegar en un clúster → observar. Agregarle un feature no termina cuando el código compila, sino cuando el servicio corre en Kubernetes junto a los otros diez.

## Los servicios

| Servicio | Lenguaje | Responsabilidad |
|---|---|---|
| `frontend` | Go | Interfaz web. **El único expuesto al exterior** |
| `productcatalogservice` | Go | Catálogo de productos |
| `cartservice` | C# | Carrito. **Único servicio con estado** (Redis) |
| `recommendationservice` | Python | Recomendaciones de productos |
| `currencyservice` | Node.js | Conversión de divisas |
| `shippingservice` | Go | Cotiza el envío y "envía" |
| `paymentservice` | Node.js | Cobra la tarjeta |
| `emailservice` | Python | Correo de confirmación |
| `checkoutservice` | Go | **Orquestador** del pedido |
| `adservice` | Java | Banners contextuales |
| `loadgenerator` | Python / Locust | Genera tráfico sintético |
| `shoppingassistantservice` | Python | Opt-in. Sirve de **plantilla** de servicio agregado después |

## Cómo se hablan

```
Usuario / loadgenerator
        │ HTTP
        ▼
   ┌─────────┐
   │frontend │ (Go) ── el único con interfaz web
   └────┬────┘
        │ gRPC
        ├──► productcatalogservice     catálogo
        ├──► cartservice ──► Redis     carrito (único con estado)
        ├──► recommendationservice     "también te puede gustar"
        ├──► currencyservice           divisas
        ├──► shippingservice           cotiza y "envía"
        ├──► adservice                 banners
        └──► checkoutservice ── orquestador del pedido
                   │
                   ├──► cart (lee y vacía)
                   ├──► productcatalog (precios)
                   ├──► currency (convierte)
                   ├──► shipping (cotiza + envía)
                   ├──► payment (cobra)
                   └──► email (confirma)
```

## Tres hechos de diseño que importan

1. **Solo `frontend` está expuesto.** Todo lo demás vive dentro del clúster y solo habla gRPC.
2. **`checkoutservice` es un orquestador puro.** No hace trabajo propio: coordina a otros seis servicios. Es el rol que probablemente tenga cualquier servicio nuevo que se agregue.
3. **Casi nada tiene estado.** El único servicio que persiste algo es el carrito, y lo hace en Redis. Esto es la raíz del hueco que ataca [seguimiento-de-pedidos-analisis](02-definicion-problema-objetivo.md).

## Restricciones del repo

`docs/product-requirements.md` exige que todo cambio nuevo:

- Preserve el despliegue por defecto sobre un clúster *kind* (gratis, sin nube)
- No complique el recorrido de demo (ver producto → carrito → checkout)
- No agregue pasos obligatorios al quickstart

Consecuencia práctica: **cualquier feature nuevo debe ser opt-in**, idealmente como componente de Kustomize bajo `kustomize/components/`. Debe poder encenderse y apagarse.

## Checklist para agregar un microservicio

`docs/adding-new-microservice.md` trae el instructivo oficial de 8 pasos, y usa `shoppingassistantservice` como ejemplo a copiar:

1. Crear directorio en `src/`
2. Código fuente siguiendo la convención de los servicios existentes
3. `Dockerfile`
4. Manifiestos de Kubernetes en `kustomize/components/` (Deployment + Service)
5. Registrar el componente en el `kustomization.yaml` raíz
6. Registrar el servicio en `skaffold.yaml`
7. Agregarlo al Helm chart (templates + values)
8. Actualizar documentación y diagramas

## Observabilidad

Los servicios están instrumentados con **OpenTelemetry**. En `src/frontend/go.mod` se ven las dependencias `otelgrpc`, `otelhttp` y el exportador `otlptracegrpc` — es decir, hay trazas distribuidas cruzando las llamadas gRPC entre servicios.

Ver opentelemetry para el concepto general.

## Páginas relacionadas

- como-levantar-online-boutique — cómo correr todo esto en local
- [seguimiento-de-pedidos-analisis](02-definicion-problema-objetivo.md) — el feature a desarrollar y el hueco que resuelve
- [propuesta-de-proyecto](03-propuesta-de-proyecto.md) — qué se construye sobre esta arquitectura y cómo
- Fase 1 - Introspeccion — entregables de la fase actual
- opentelemetry — estándar de observabilidad usado por estos servicios
- Arquitectura — patrones de arquitectura en el vault
