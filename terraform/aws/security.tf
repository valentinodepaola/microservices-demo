resource "aws_security_group" "k3s" {
  name        = "${var.project}-k3s"
  description = "Nodo de k3s: API de Kubernetes y la tienda. Sin SSH, se entra por SSM."
  vpc_id      = aws_vpc.main.id

  tags = {
    Name = "${var.project}-sg-k3s"
  }
}

resource "aws_vpc_security_group_ingress_rule" "api_kubernetes" {
  security_group_id = aws_security_group.k3s.id
  description       = "API de k3s. Abierto porque los runners de GitHub Actions no tienen IP fija; el acceso lo controla el certificado de cliente del kubeconfig."
  cidr_ipv4         = "0.0.0.0/0"
  from_port         = 6443
  to_port           = 6443
  ip_protocol       = "tcp"
}

resource "aws_vpc_security_group_ingress_rule" "tienda" {
  security_group_id = aws_security_group.k3s.id
  description       = "La tienda, publicada por el ServiceLB de k3s en el puerto 80 del nodo."
  cidr_ipv4         = "0.0.0.0/0"
  from_port         = 80
  to_port           = 80
  ip_protocol       = "tcp"
}

resource "aws_vpc_security_group_egress_rule" "salida" {
  security_group_id = aws_security_group.k3s.id
  description       = "Salida sin restriccion: el nodo necesita descargar k3s, las imagenes de ghcr.io y registrarse en SSM."
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"
}