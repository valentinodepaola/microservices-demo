#!/usr/bin/env bash
# Deja Vault listo despues de inicializarlo y abrirlo (#78): el motor KV,
# la bitacora, las politicas y los dos secretos. Se puede correr varias
# veces. No crea tokens: esos se crean a mano (ver vault/README.md).
#
# Necesita VAULT_ADDR, VAULT_CACERT y una sesion con el token raiz:
#   vault login
#   ./vault/configurar.sh
set -euo pipefail

VAULT_DIR="$(cd "$(dirname "$0")" && pwd)"
KUBECONFIG_ARCHIVO="${KUBECONFIG_ARCHIVO:-$HOME/.kube/boutique-k3s.yaml}"

if [ ! -f "$KUBECONFIG_ARCHIVO" ]; then
  echo "No encuentro el kubeconfig en $KUBECONFIG_ARCHIVO." >&2
  echo "Sacalo con: terraform -chdir=terraform/aws output -raw kubeconfig_comando" >&2
  exit 1
fi

# --- El motor de secretos: KV version 2 en boutique/ ---
if vault secrets list -format=json | jq -e 'has("boutique/")' > /dev/null; then
  echo "El motor boutique/ ya existe."
else
  vault secrets enable -path=boutique kv-v2
fi

# --- La bitacora de auditoria (la carpeta la crea Ansible) ---
if vault audit list -format=json 2> /dev/null | jq -e 'has("file/")' > /dev/null 2>&1; then
  echo "La bitacora ya esta encendida."
else
  vault audit enable file file_path=/var/log/vault/audit.log
fi

# --- Las politicas: write reemplaza la que haya, asi que siempre se repite ---
vault policy write pipeline "$VAULT_DIR/policies/pipeline.hcl"
vault policy write respaldo "$VAULT_DIR/policies/respaldo.hcl"

# --- El kubeconfig: solo se carga si cambio, para no sumar versiones ---
ACTUAL="$(vault kv get -field=kubeconfig boutique/kubeconfig 2> /dev/null || true)"
if [ "$ACTUAL" = "$(cat "$KUBECONFIG_ARCHIVO")" ]; then
  echo "El kubeconfig en Vault ya esta al dia."
else
  vault kv put boutique/kubeconfig kubeconfig=@"$KUBECONFIG_ARCHIVO"
fi

# --- La contrasena de redis-orders (#79): solo si no existe ---
# Se genera aqui y va directo a Vault por la entrada estandar (password=-):
# nunca aparece en la pantalla ni en el historial.
if vault kv get boutique/redis-orders > /dev/null 2>&1; then
  echo "La contrasena de redis-orders ya existe; no la cambio."
else
  openssl rand -hex 24 | tr -d '\n' | vault kv put boutique/redis-orders password=-
fi

echo "Listo."