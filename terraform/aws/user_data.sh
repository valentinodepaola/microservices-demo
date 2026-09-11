#!/bin/bash
set -euxo pipefail

# k3s se instala con el certificado del API valido tambien para la IP publica,
# para que kubectl pueda alcanzarlo desde fuera de la instancia.
#
# --disable=traefik: k3s trae Traefik y ocupa el puerto 80, que es donde el
#   ServiceLB tiene que publicar la tienda. La aplicacion no usa Ingress.
# --write-kubeconfig-mode 644: deja el kubeconfig legible sin sudo, para poder
#   extraerlo por SSM sin elevar privilegios.
curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="--tls-san ${eip} --disable=traefik --write-kubeconfig-mode 644" sh -