# Lo unico que puede hacer el pipeline: leer sus dos secretps
# Con KV v2, los valores viven bajo boutique/data/ y no bajo boutique/
# Una regla sobre "boutique/kubeconfig" no dejaria leer nada

path "boutique/data/kubeconfig" {
  capabilities = ["read"]
}

path "boutique/data/redis-orders" {
  capabilities = ["read"]
}
