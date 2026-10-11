# Sacar fotos de Vault antes de recrear su maquina (ADR 0011, pieza 7):
#   vault operator raft snapshot save vault.snap
# Solo eso: no puede leer secretos ni restaurar una foto.
path "sys/storage/raft/snapshot" {
  capabilities = ["read"]
}
