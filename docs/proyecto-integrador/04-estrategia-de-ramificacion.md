# Estrategia de ramificación — GitHub Flow

Documento del criterio *"Elegir una estrategia de ramificación para aplicarla en el proyecto"* (25 pts). Ver instrucciones-fase-1.

---

## La estrategia

El proyecto adopta **GitHub Flow**.

La regla que la define es una sola: **`main` se mantiene siempre desplegable**. Todo el trabajo ocurre en ramas de vida corta que parten de `main`, y ninguna vuelve a `main` sin pasar por un Pull Request revisado.

No existen ramas `develop`, de release ni de ambiente. La rama principal es la única fuente de verdad.

## Estructura de ramas

```
main ──●────────●────────────────●──────────►  siempre desplegable
        \      /                 /
         \    /                 /
          ●──●                 /   feature/order-tracking-service
                              /
                 ●───────────●        docs/fase-1-entregables
```

## Convención de nombres

| Prefijo | Para qué | Ejemplo |
|---|---|---|
| `feature/` | Funcionalidad nueva | `feature/order-tracking-service` |
| `fix/` | Corrección de un defecto | `fix/tracking-id-collision` |
| `docs/` | Documentación y entregables de la materia | `docs/fase-1-entregables` |
| `chore/` | Configuración, dependencias, herramental | `chore/kustomize-component` |

## Flujo de trabajo

1. Partir siempre de `main` actualizado:
   ```
   git checkout main && git pull
   ```
2. Crear la rama de trabajo con nombre descriptivo:
   ```
   git checkout -b feature/order-tracking-service
   ```
3. Trabajar en commits pequeños, con mensajes que se entiendan sin abrir el diff.
4. Publicar la rama y abrir un Pull Request hacia `main`, describiendo qué resuelve:
   ```
   git push -u origin feature/order-tracking-service
   ```
5. El otro integrante revisa el Pull Request.
6. Integrar el Pull Request a `main` y borrar la rama.

## Reglas del equipo

- **Nadie hace commit directo a `main`.** Todo entra por Pull Request.
- **Ningún Pull Request se integra sin revisión** del otro integrante.
- **Un Pull Request, un propósito.** No mezclar documentación de la materia con código del servicio.
- **La rama se borra al integrarse.** El historial vive en `main` y en el Pull Request.
- **Si `main` se rompe, repararlo es prioridad** sobre cualquier trabajo en curso.

## Configuración en GitHub

Sobre la rama `main` se activa protección de rama con dos reglas:

- Exigir Pull Request antes de integrar
- Exigir al menos una aprobación

Con esto el acuerdo del equipo deja de depender de la disciplina de cada quien y pasa a ser una regla que la plataforma hace cumplir.

## Páginas relacionadas

- instrucciones-fase-1 — criterios de evaluación
- portafolio-de-evidencias — evidencias de la aplicación de la estrategia
- [propuesta-de-proyecto](03-propuesta-de-proyecto.md) — la propuesta que aplica esta estrategia: qué se construye y cómo
