# Log Group donde Fluent Bit manda los logs de los pods de la tienda.
#
# La retención es de 7 días a propósito: sin ella CloudWatch guarda los logs
# para siempre, y como el loadgenerator escribe sin parar, el almacenamiento
# crece y se cobra cada mes. Para consultar y para la demo basta con lo reciente.
#
# Lo crea Terraform y no Fluent Bit. El permiso del lab sí deja que Fluent Bit
# cree grupos (lo comprobé desde un pod), pero entonces quedarían fuera del
# código y terraform plan no los vería. Por eso Fluent Bit va configurado para
# no crearlos.
resource "aws_cloudwatch_log_group" "tienda" {
  name              = "/${var.project}/tienda"
  retention_in_days = 7

  tags = {
    Name = "${var.project}-logs-tienda"
  }
}