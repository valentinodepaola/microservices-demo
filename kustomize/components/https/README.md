# Componente `https`

Sirve la tienda por `https://` con un certificado de Let's Encrypt (#68). Antes se abría en `http://3.94.38.103` y el navegador la marcaba como "No seguro".

Contiene tres piezas:

- **Traefik**, como `HelmChart`. No lo instala Skaffold ni `helm`: el helm-controller que trae k3s ve el `HelmChart` y corre un Job que instala el chart en el namespace `traefik`. Traefik recibe el 80 y el 443 del nodo, redirige http a https y pide y renueva el certificado.
- **El Ingress `frontend`**, para el host `3-94-38-103.sslip.io`. Le manda el tráfico al Service interno `frontend:80`. El frontend sigue hablando http dentro del clúster: el cifrado se hace en Traefik.
- **Un parche al `frontend`** con `ENV_PLATFORM=aws`, para que el banner de la izquierda diga "AWS" y no "local".

No se usa solo: va en el overlay [`kustomize/overlays/aws/`](../../overlays/aws/) junto con `order-tracking` y `non-public-frontend`, que quita `frontend-external` para liberar el puerto 80. El perfil `aws` de Skaffold renderiza ese overlay, y es el que usa el CD.

## Por qué así

- **El ACME de Traefik y no cert-manager.** cert-manager es lo más común, pero necesita que sus CRDs existan antes de aplicar el `ClusterIssuer`, y eso obligaba a partir el despliegue en dos pasos. Con Traefik todo entra en un solo `kubectl apply`, y es un pod en vez de cuatro en un nodo que anda justo de CPU. El costo es que el certificado vive en un archivo de Traefik (`/data/acme.json`) y no en un Secret.
- **sslip.io y no un dominio propio.** Let's Encrypt necesita un nombre, no una IP. sslip.io convierte la IP en uno sin registrar nada. Si se consigue un dominio, solo cambia el host del Ingress y el output `tienda_url`.
- **Un disco para el certificado**, como los Redis (#106). Sin él, cada reinicio del pod pediría otro certificado y gastaría el límite de Let's Encrypt.
- **Dos resolvers**: `letsencrypt-staging` para probar sin gastar el límite (sus certificados no sirven en el navegador) y `letsencrypt` para el de verdad. El Ingress elige con su anotación.

## Lo que hay que saber

- **sslip.io comparte el límite de Let's Encrypt con todo el mundo.** Si se agota, el certificado no se emite hasta que baje el uso. Con un dominio propio no pasa.
- **`frontend-external` no se borra solo.** Skaffold no borra lo que ya no está en los manifiestos. En el clúster que ya existía se borró a mano una vez con `kubectl delete svc frontend-external`.
- **Si la IP elástica cambia**, hay que cambiar el host del Ingress.
- **Pasar de staging a producción no es solo cambiar la anotación.** Si Traefik ya tiene un certificado para el nombre, aunque sea el de staging, no pide otro. Hay que borrar el de staging y reiniciar:

  ```bash
  kubectl -n traefik exec deploy/traefik -- rm /data/acme-staging.json
  kubectl -n traefik rollout restart deploy/traefik
  ```

- **Traefik corre con `Recreate`, nunca dos pods a la vez.** En la primera prueba, un rolling update dejó al pod viejo y al nuevo juntos unos segundos. Let's Encrypt le preguntó por el reto al viejo, que no lo conocía, y recibió un `404`.
- **Si el certificado falla al instalar Traefik por primera vez**, puede ser que Let's Encrypt llegó antes de que k3s abriera el puerto 80 (`Connection refused`). Me pasó una vez. Traefik no lo reintenta solo: hay que reiniciarlo con `kubectl -n traefik rollout restart deploy/traefik`.

Qué revisar si no abre: [`docs/operar-el-cluster-de-aws.md`](../../../docs/operar-el-cluster-de-aws.md#la-tienda-no-abre-por-https).
