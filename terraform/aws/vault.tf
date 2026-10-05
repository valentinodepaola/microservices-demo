resource "aws_instance" "vault" {
  ami                    = data.aws_ami.ubuntu.id
  instance_type          = var.vault_instance_type
  subnet_id              = aws_subnet.public.id
  vpc_security_group_ids = [aws_security_group.vault.id]
  iam_instance_profile   = "LabInstanceProfile"

  root_block_device {
    volume_size = 10
    volume_type = "gp3"
  }

  tags = {
    Name = "${var.project}-vault"
  }

  lifecycle {
    ignore_changes = [ami]
  }
}

resource "aws_eip" "vault" {
  domain = "vpc"

  tags = {
    Name = "${var.project}-eip-vault"
  }
}

resource "aws_eip_association" "vault" {
  instance_id   = aws_instance.vault.id
  allocation_id = aws_eip.vault.id
}

resource "aws_security_group" "vault" {
  name        = "${var.project}-vault"
  description = "Maquina de Vault: solo el 8200 por HTTPS. Sin SSH, se entra por SSM."
  vpc_id      = aws_vpc.main.id

  tags = {
    Name = "${var.project}-sg-vault"
  }
}

resource "aws_vpc_security_group_ingress_rule" "vault_api" {
  security_group_id = aws_security_group.vault.id
  description       = "API e interfaz web de Vault. Abierto porque los runners de GitHub Actions no tienen IP fija; el acceso lo controla el token de Vault."
  cidr_ipv4         = "0.0.0.0/0"
  from_port         = 8200
  to_port           = 8200
  ip_protocol       = "tcp"
}

resource "aws_vpc_security_group_egress_rule" "vault_salida" {
  security_group_id = aws_security_group.vault.id
  description       = "Salida sin restriccion: la maquina necesita descargar Vault, registrarse en SSM y leer los archivos de Ansible en S3."
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"
}

data "aws_caller_identity" "current" {}

# Bucket por donde el plugin de SSM de Ansible pasa archivos a la máquina.
# Va sin versioning a propósito: esos archivos pueden llevar secretos en texto
# plano, y con versioning las copias borradas se quedan para siempre. Por lo
# mismo no es el bucket del estado, que sí lo necesita (ver ADR 0011).
#
# El bucket se crea a mano, como el del estado (ver el README): el provider lee
# su configuración de object lock cada vez que revisa un aws_s3_bucket, y el
# lab niega s3:GetBucketObjectLockConfiguration. Lo demás sí lo maneja Terraform.
locals {
  ansible_ssm_bucket = "${var.project}-ansible-ssm-${data.aws_caller_identity.current.account_id}"
}

resource "aws_s3_bucket_public_access_block" "ansible_ssm" {
  bucket                  = local.ansible_ssm_bucket
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# El plugin borra sus archivos al terminar, pero si una corrida se corta se
# quedan. Esto los limpia al día.
resource "aws_s3_bucket_lifecycle_configuration" "ansible_ssm" {
  bucket = local.ansible_ssm_bucket

  rule {
    id     = "borrar-al-dia"
    status = "Enabled"

    filter {}

    expiration {
      days = 1
    }
  }
}
