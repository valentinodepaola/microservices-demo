output "instance_id" {
  description = "Id de la instancia del nodo de k3s. Se usa para apagarla, encenderla y entrar por SSM."
  value       = aws_instance.k3s.id
}

output "public_ip" {
  description = "IP elastica del nodo. Es la direccion del API de Kubernetes y la de la tienda."
  value       = aws_eip.k3s.public_ip
}

output "tienda_url" {
  description = "URL publica de la tienda, una vez que el despliegue la publique en el puerto 80."
  value       = "http://${aws_eip.k3s.public_ip}"
}

output "kubeconfig_comando" {
  description = "Extrae el kubeconfig de la instancia y lo deja apuntando a la IP publica."
  value       = <<-EOT
    CMD=$(aws ssm send-command --instance-ids ${aws_instance.k3s.id} --document-name "AWS-RunShellScript" --parameters 'commands=["cat /etc/rancher/k3s/k3s.yaml"]' --query Command.CommandId --output text)
    sleep 5 && aws ssm get-command-invocation --command-id "$CMD" --instance-id ${aws_instance.k3s.id} --query StandardOutputContent --output text > ~/.kube/boutique-k3s.yaml
    sed -i '' 's|https://127.0.0.1:6443|https://${aws_eip.k3s.public_ip}:6443|' ~/.kube/boutique-k3s.yaml
    chmod 600 ~/.kube/boutique-k3s.yaml
  EOT
}

output "vault_instance_id" {
  description = "Id de la instancia de Vault. Se usa para apagarla, encenderla, entrar por SSM y como host del inventario de Ansible."
  value       = aws_instance.vault.id
}

output "vault_public_ip" {
  description = "IP elastica de Vault. Es la que va dentro de su certificado."
  value       = aws_eip.vault.public_ip
}

output "vault_url" {
  description = "Direccion del API y la interfaz web de Vault, una vez que Ansible lo instale."
  value       = "https://${aws_eip.vault.public_ip}:8200"
}

output "ansible_ssm_bucket" {
  description = "Bucket por donde el plugin de SSM de Ansible pasa archivos a la maquina de Vault."
  value       = local.ansible_ssm_bucket
}
