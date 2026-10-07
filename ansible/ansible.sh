#!/usr/bin/env bash
# Corre Ansible dentro del contenedor (ver Dockerfile), desde cualquier
# carpeta del repo:
#   ./ansible/ansible.sh ansible vault -m ansible.builtin.ping
#   ./ansible/ansible.sh ansible-playbook vault.yml
set -euo pipefail

ANSIBLE_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(dirname "$ANSIBLE_DIR")"

# El bucket de SSM sale de Terraform, no del repo (lleva el numero de cuenta).
ANSIBLE_SSM_BUCKET="$(terraform -chdir="$REPO_DIR/terraform/aws" output -raw ansible_ssm_bucket)"
export ANSIBLE_SSM_BUCKET

# Colores solo si la salida es una terminal.
[ -t 1 ] && export ANSIBLE_FORCE_COLOR=1

# Aqui vive la llave de la autoridad propia (ADR 0011, pieza 5). Fuera del
# repo y solo para tu usuario.
mkdir -p -m 700 "$HOME/.vault-boutique"

# Reconstruye la imagen si cambio el Dockerfile o requirements.yml. Si no
# cambio nada, sale de la cache en un par de segundos.
docker build -q -t boutique-ansible "$ANSIBLE_DIR" > /dev/null

exec docker run --rm \
  -v "$ANSIBLE_DIR:/ansible" \
  -v "$HOME/.aws:/root/.aws:ro" \
  -v "$HOME/.vault-boutique:/root/.vault-boutique" \
  -e ANSIBLE_SSM_BUCKET \
  -e ANSIBLE_FORCE_COLOR \
  boutique-ansible "$@"
