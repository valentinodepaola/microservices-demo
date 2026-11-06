# Componente `cloudwatch-logs`

Manda los logs de los pods de la tienda al Log Group `/boutique/tienda` de CloudWatch (#111). Es el reto 3 de la Fase 4.

Contiene a Fluent Bit como DaemonSet, en su propio namespace (`logging`), con lo que necesita: su ServiceAccount, un ClusterRole de solo lectura para preguntar por los pods y un ConfigMap con la configuración. La configuración está comentada en [`fluent-bit.yaml`](fluent-bit.yaml).

## Cómo funciona

1. Cada contenedor escribe sus logs en un archivo del nodo, en `/var/log/containers/<pod>_<namespace>_<contenedor>-<id>.log`. Fluent Bit los lee, pero solo los del namespace `default`, que es el de la tienda.
2. A cada línea le agrega el nombre del pod, el namespace y el contenedor, preguntándole a la API de Kubernetes.
3. Las manda a `/boutique/tienda`, con un stream por pod y contenedor.

Fluent Bit no tiene llaves de AWS: le pide credenciales al servicio de metadatos de la EC2, que se las da con el permiso de `LabInstanceProfile`. Por eso este componente **solo sirve en el clúster de AWS**, y por eso `terraform/aws/compute.tf` fija en 2 el límite de saltos del servicio de metadatos.

## Lo que no hace

- **No crea el Log Group.** Lo crea Terraform ([`terraform/aws/logs.tf`](../../../terraform/aws/logs.tf)), y `auto_create_group` está apagado. El permiso del lab sí dejaría a Fluent Bit crearlo, así que esa opción es lo único que evita que aparezcan grupos fuera del código. Si el grupo no existe, Fluent Bit falla con `ResourceNotFoundException` y lo regresa un `terraform apply`.
- **No manda sus propios logs ni los de `kube-system`.** Solo los de la tienda.

## Dónde se enciende

| Ruta | Encendido |
|---|---|
| Skaffold | `skaffold run -p aws,cloudwatch-logs` |
| CD (`cd-main.yaml`) | siempre encendido |
| A mano | `kubectl apply -k kustomize/components/cloudwatch-logs` |

No está en `kustomize/kustomization.yaml` ni en el chart de Helm, porque fuera de AWS no tendría de dónde sacar credenciales.

## Revisarlo

```bash
kubectl -n logging get pods
kubectl -n logging logs ds/fluent-bit --tail=30
aws logs tail /boutique/tienda --follow --region us-east-1
```

Lo que hacer en cada caso de error está en [`docs/operar-el-cluster-de-aws.md`](../../../docs/operar-el-cluster-de-aws.md#ver-los-logs-en-cloudwatch).
