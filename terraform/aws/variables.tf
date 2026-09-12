variable "region" {
  description = "Region de AWS. El entorno que tenemos de AWS Academy solo nos permite us-east-1 y us-west-2."
  type        = string
  default     = "us-east-1"
}

variable "project" {
  description = "Prefijo para nombrar y etiquetar todos los recursos de modulo"
  type        = string
  default     = "boutique"
}

variable "instance_type" {
  description = "Tipo de instancia del nodo de k3s. AWS Academy limita el tamaño a 'large' (2 vCPU). Se usa m5 y no t3 porque t3 es burstable y el loadgenerator genera carga continua."
  type        = string
  default     = "m5.large"
}

variable "vpc_cidr" {
  description = "Rango de direcciones privadas de la VPC"
  type        = string
  default     = "10.0.0.0/16"
}

variable "subnet_cidr" {
  description = "Rango de la subred publica, contenido dentro del de la VPC"
  type        = string
  default     = "10.0.1.0/24"
}
