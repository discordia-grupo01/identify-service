# Historias de usuario

Las historias del catálogo de la cátedra que resuelve este servicio, con su número, y las que resuelve junto con otros servicios. Las rutas completas con sus parámetros y respuestas están en [`openapi.yaml`](../openapi.yaml).

## Obligatorias

| # | Historia | Puntos | Rutas |
|---|---|---|---|
| 1 | Registro de usuarios | 3 | `POST /v1/users`, `POST /v1/email-confirmation`, `POST /v1/email-confirmation/confirm` |
| 2 | Login con email y contraseña | 2 | `POST /v1/login`, `POST /v1/logout`, `POST /v1/refresh` |
| 3 | Recupero de contraseña | 3 | `POST /v1/password-recovery`, `POST /v1/password-reset` |
| 4 | Edición de perfil | 2 | `PATCH /v1/me/profile` |
| 5 | Visualización de perfil propio | 1 | `GET /v1/me/profile` |

## Optativas

| # | Historia | Puntos | Rutas |
|---|---|---|---|
| 33 | Login con proveedor federado | 5 | `POST /v1/oauth/google`, `GET /oauth/mobile-callback` |
| 34 | Registro con PIN | 2 | `POST /v1/pin`, `POST /v1/pin/login` |
| 35 | Autenticación de dos factores (2FA) | 5 | `POST /v1/me/2fa/setup`, `POST /v1/me/2fa/activate`, `POST /v1/me/2fa/disable`, `POST /v1/me/2fa/recovery-codes`, `GET /v1/me/2fa`, `POST /v1/login/2fa` |
| 36 | Visualización de perfil público | 2 | `GET /v1/users/{id}` |
| 38 | Estado personalizado | 2 | `PUT /v1/me/status`, `DELETE /v1/me/status` |

## Historias en las que participa

Estas historias las completan otros servicios. Acá está la parte que le toca a `identify-service`:

| # | Historia | Qué hace este servicio |
|---|---|---|
| 29 | Suspender usuario a nivel plataforma | Recibe el evento firmado de `admin`, marca la cuenta como suspendida y revoca todas sus sesiones. Desde ese momento rechaza login, renovación y cualquier pedido. |
| 30 | Reactivar usuario suspendido | Recibe el evento firmado de `admin` y vuelve a habilitar la cuenta. |
| 35 | Autenticación de dos factores (2FA) | Si el usuario perdió el segundo factor, `POST /v1/login/2fa/recovery-request` pide la recuperación de la cuenta, que el staff aprueba o rechaza desde el backoffice. |
| 76 | Suscribirse a Plan Premium | Guarda el estado de Premium de cada usuario a partir del evento firmado de `billing`, para mostrarlo en el perfil. |
| 31 | Ver listado de usuarios con filtros | Publica los datos del usuario (registro, mail confirmado y cambios de perfil) para que `admin` arme su listado. |
