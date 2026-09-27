# identify-service

Servicio de identidad de Discordia: registro, confirmacion de email, login, sesiones y recuperacion de contraseña. Es donde vive todo lo de usuarios (y, a futuro, perfil).

## Stack
- Lenguaje: Go
- Framework: Gin (github.com/gin-gonic/gin)
- BDD: PostgreSQL (Supabase en produccion, ver seccion "Supabase" mas abajo)
- Infra extra: SMTP (envio de emails de confirmacion de registro y recuperacion de contraseña)

## Setup local
```bash
cp .env.example .env
docker compose up -d db
make run
```

## Variables de entorno
| Variable | Default | Descripcion |
|---|---|---|
| ENVIRONMENT | development | `development` o `production`. |
| HOST | 0.0.0.0 | IP donde escucha el servicio. `0.0.0.0` permite acceso desde otras redes/contenedores; `127.0.0.1` restringe a conexiones locales. |
| PORT | 8080 | Puerto donde escucha el servicio. La API REST y el WebSocket comparten `${HOST}:${PORT}`. |
| DATABASE_HOST | localhost | Host de PostgreSQL. |
| DATABASE_PORT | 5432 | Puerto de PostgreSQL. |
| DATABASE_NAME | - | Nombre de la base. En local, coincide con `POSTGRES_DB` de docker-compose.yml. |
| DATABASE_USER | - | Usuario de conexion. En local, coincide con `POSTGRES_USER` de docker-compose.yml. |
| DATABASE_PASSWORD | - | Password de conexion. En local, coincide con `POSTGRES_PASSWORD` de docker-compose.yml. |
| DATABASE_SSLMODE | disable | `disable` en local; `require` cuando la BDD es Supabase (exige TLS). |
| DATABASE_SCHEMA | - (vacio) | Vacio en local. En Supabase, el schema dedicado de este servicio dentro del proyecto compartido (ver seccion "Supabase" mas abajo). |
| JWT_SECRET | - | Secreto para firmar/validar JWT. |
| TWO_FACTOR_ENCRYPTION_KEY | (cae a `JWT_SECRET`) | Clave con la que se cifra el secreto TOTP del segundo factor antes de guardarlo. **Rotarla (o rotar `JWT_SECRET` mientras esta esté vacía) deja ilegibles los secretos ya guardados: todos los usuarios con 2FA activo quedarían afuera y tendrían que volver a activarlo.** |
| RABBITMQ_URL | - | URL de conexion a RabbitMQ. |
| SMTP_HOST | - | Host del servidor SMTP. |
| SMTP_PORT | 587 | Puerto SMTP con STARTTLS. |
| SMTP_USERNAME | - | Usuario SMTP. |
| SMTP_PASSWORD | - | Password SMTP. |
| SMTP_FROM | - | Direccion remitente. |
| SMTP_REPLY_TO | - | Direccion opcional de respuesta. |
| APP_NAME | Discordia | Nombre mostrado en el email. |
| PASSWORD_RESET_URL | http://localhost:3000/reset-password | URL base del frontend para recuperar la contraseña. |
| PASSWORD_RESET_EXPIRATION | 1h | Expiracion del enlace, limitada a una hora. |
| PASSWORD_RESET_MAX_ATTEMPTS | 3 | Solicitudes permitidas por email en una hora. |
| EMAIL_CONFIRMATION_URL | http://localhost:3000/confirm-email | URL base del frontend para confirmar el email de registro. |
| EMAIL_CONFIRMATION_EXPIRATION | 24h | Expiracion del enlace de confirmacion. |
| EMAIL_CONFIRMATION_MAX_ATTEMPTS | 3 | Solicitudes de confirmacion permitidas por email en una hora. |
| PROFILE_IMAGE_DIR | ./storage/profile-images | Directorio local para imagenes de perfil. |
| PROFILE_IMAGE_URL | /uploads/profile-images | URL publica base de las imagenes de perfil. |
| PROFILE_IMAGE_STORAGE | local | Backend de imagenes: `local` o `supabase`. |
| SUPABASE_URL | - | URL del proyecto Supabase, necesaria con `PROFILE_IMAGE_STORAGE=supabase`. |
| SUPABASE_SERVICE_ROLE_KEY | - | Clave privada del servidor para Supabase Storage. Nunca exponerla al navegador. |
| PROFILE_IMAGE_BUCKET | profile-images | Bucket publico de Supabase Storage. |
| PROFILE_IMAGE_PUBLIC_URL | - | URL publica opcional del bucket; por defecto se construye desde `SUPABASE_URL`. |

`.env.example` solo trae la estructura (sin valores sensibles). Los valores reales van en tu propio `.env`, que nunca se sube (ya esta en `.gitignore`).

## Estructura de carpetas
```
identify-service/
├── cmd/
│   └── api/
│       ├── main.go          entrypoint: arma el router y levanta el server
│       └── database.go       conexion a Postgres (initDB)
├── internal/
│   ├── handler/              handlers HTTP: auth, confirmacion de email y password recovery
│   ├── service/               logica de negocio: auth, confirmacion y password recovery
│   ├── repository/            acceso a datos: users, refresh_tokens, email_confirmations y password_recoveries
│   ├── model/                 structs de dominio / DTOs
│   ├── middleware/            middlewares de Gin (auth, logging, etc.)
│   ├── utils/email/            plantillas y envio SMTP de confirmacion y recuperacion
│   ├── events/                 consumer de RabbitMQ: membresias de servidor para "servidores en comun" (HU 36)
│   └── audit/                  registra cada evento recibido (tabla consumed_events), procesado o descartado
├── migrations/                migraciones de BDD (golang-migrate)
├── .github/
│   └── workflows/
│       └── ci.yml            gofmt, build, vet y test en cada PR/push a main
├── .githooks/
│   └── pre-commit             hook de gofmt local
├── openapi.yaml               contrato de la API REST -- generado (make openapi), no se edita a mano
├── docker-compose.yml         levanta el servicio + Postgres para desarrollo local
├── Dockerfile                 build multi-stage para la imagen de produccion
├── Makefile                   comandos run/test/build/migrate/openapi
└── .env.example                estructura de variables de entorno (sin valores sensibles)
```
`internal/` esta pensado para no exponer estos paquetes fuera del modulo (restriccion de Go).

## Comandos (Makefile)
- `make run` - corre el servicio localmente (`go run ./cmd/api`).
- `make test` - corre los tests con cobertura (`go test ./... -coverpkg=./internal/... -cover`). El `-coverpkg` es necesario porque `test/e2e` ejercita `internal/repository` a traves del router real: sin el, esos paquetes aparecen en 0% aunque esten cubiertos.
- `make build` - compila el binario en `bin/api`.
- `make migrate` - corre las migraciones (golang-migrate) contra la BDD que indique `.env` (local o Supabase).
- `make openapi` - regenera `openapi.yaml` desde las anotaciones swag de los handlers (`internal/handler/**`). Necesita Go y Node. No hace falta correrlo a mano: el CI lo corre en cada push/PR y, si detecta que cambió, commitea `openapi.yaml` solo de vuelta a la rama.

## Tests
```bash
make test
```
Cobertura minima: 70% (requisito del proyecto).

Los tests en `test/e2e/` son de punta a punta: arman el router real (repos, servicios y handlers reales) contra un Postgres real, sin stubs. Se saltean automaticamente si no hay `DATABASE_HOST` seteado. Para correrlos:
```bash
docker compose up -d db
make migrate
DATABASE_HOST=localhost DATABASE_PORT=5432 DATABASE_USER=discordia DATABASE_PASSWORD=discordia DATABASE_NAME=discordia_servicio \
  go test ./test/e2e/... -v
```

`TestMutualServersRabbitMQPipeline` ademas necesita un RabbitMQ real; se saltea sola si no hay `RABBITMQ_URL` seteado:
```bash
docker run -d --name discordia-rabbitmq -p 5672:5672 -p 15672:15672 rabbitmq:3.13-management-alpine
DATABASE_HOST=localhost RABBITMQ_URL=amqp://guest:guest@localhost:5672/ \
  go test ./test/e2e/... -run TestMutualServersRabbitMQPipeline -v
```

Los tests e2e comprueban el flujo real de confirmacion: el login se rechaza antes de confirmar, el reenvio invalida el token anterior, el token nuevo se consume una sola vez y el login se habilita despues de confirmar.

## Supabase (produccion)
Este servicio no usa Supabase Auth: el login/JWT/sesiones son 100% propios. "Migrar a Supabase" es solo cambiar a que Postgres se conecta, no la logica de auth.

Cada servicio de Discordia tiene su **propio proyecto de Supabase** (no comparten uno solo) -- este servicio usa el proyecto `discordia-identity`. Pasos:
1. Opcional: si queres organizar las tablas en un schema propio en vez de `public`, crealo una sola vez desde **Database > Schemas > New schema** (nombre `identify`), o corriendo en el SQL Editor:
   ```sql
   CREATE SCHEMA IF NOT EXISTS identify;
   ```
2. Completar en tu `.env` los datos de conexion del proyecto `discordia-identity`, tomados del boton **Connect**, pestaña **Session pooler** (no "Direct connection": ese host solo resuelve por IPv6 y muchas redes no tienen esa salida; tampoco "Transaction pooler", que no sostiene el `search_path` por conexion):
   ```
   DATABASE_HOST=aws-0-<region>.pooler.supabase.com
   DATABASE_PORT=5432
   DATABASE_NAME=postgres
   DATABASE_USER=postgres.<project-ref>
   DATABASE_PASSWORD=<password del proyecto>
   DATABASE_SSLMODE=require
   DATABASE_SCHEMA=identify
   ```
3. Correr `make migrate` y despues `make run`. Confirmar con `GET /readyz` (200). Un `POST /v1/users` envia un email de confirmacion; el login devuelve 401 hasta confirmar el token recibido mediante `POST /v1/email-confirmation/confirm`.

## Endpoints
- `POST /v1/users` - registro de usuario.
- `POST /v1/login` - login. Requiere que el email este confirmado. Devuelve un access token (JWT, ~15 min) en el body y setea el refresh token (~30 dias) como cookie httpOnly; el refresh token nunca viaja en el body. **Si la cuenta tiene segundo factor activo, responde 202 (Accepted) en vez de 200**: trae `two_factor_required: true` + `challenge_token` y no setea cookie. El cliente sigue distinguiendo las dos respuestas por el campo `two_factor_required` (no por el código HTTP): el 202 solo existe para que la documentación de la API sea correcta. **429 (`LOGIN_ATTEMPTS_EXCEEDED`)** si hubo 5 intentos fallidos en los ultimos 15 minutos para esa combinacion de email + IP de origen -- se cuenta por (email, ip) y no por cuenta entera, para que nadie pueda bloquear la cuenta de otra persona solo sabiendo su email (ver `AuthService.Login` / `login_attempts`). Ademas, el gateway (Kong) limita `/v1/login` a 60 req/min por IP como proteccion adicional contra volumen.
- `POST /v1/login/2fa` - segundo paso del login. Acepta tanto el codigo de 6 digitos de la app autenticadora como uno de los codigos de recuperacion de un solo uso (se distinguen por el formato). El desafio dura 5 minutos, se consume al primer codigo aceptado y se quema a los 5 intentos fallidos. Publico: quien lo llama todavia no tiene sesion.
- `GET /v1/me/2fa` - estado del segundo factor de la cuenta (activo, pendiente de confirmar, codigos de recuperacion sin usar). Requiere Bearer JWT.
- `POST /v1/me/2fa/setup` - genera el secreto TOTP y devuelve la URI `otpauth://` para que el cliente dibuje el QR (el backend no devuelve imagenes). No activa nada: el 2FA queda pendiente. Llamarlo dos veces devuelve el mismo secreto pendiente, para no invalidar un QR ya escaneado. Requiere Bearer JWT.
- `POST /v1/me/2fa/activate` - confirma el QR con un codigo de la app, activa el 2FA y devuelve los codigos de recuperacion. Es la unica respuesta donde los codigos viajan en claro: despues solo queda su hash. Requiere Bearer JWT.
- `POST /v1/me/2fa/disable` - desactiva el segundo factor reingresando la contraseña, y borra secreto, codigos y desafios pendientes. Requiere Bearer JWT.
- `POST /v1/me/2fa/recovery-codes` - regenera la lista de codigos de recuperacion (invalida la anterior completa) reingresando la contraseña. Requiere Bearer JWT.
- `POST /v1/me/password` - le da una contraseña a una cuenta que nunca tuvo una (alta puramente federada por Google). No es un cambio de contraseña: responde 409 (`PASSWORD_ALREADY_SET`) si la cuenta ya tiene una. Paso previo obligatorio para activar el 2FA en esas cuentas (`POST /v1/me/2fa/setup` responde 409 `PASSWORD_REQUIRED_FOR_TWO_FACTOR` si todavia no la tienen) -- sin contraseña, no habria forma de reingresarla despues para desactivar el 2FA o regenerar codigos de recuperacion. Requiere Bearer JWT.
- `POST /v1/email-confirmation` - solicita o reenvia el email de confirmacion. Responde de forma generica para no revelar si un email esta registrado y aplica un limite de solicitudes.
- `POST /v1/email-confirmation/confirm` - confirma el email usando el token recibido. El token es de un solo uso y expira segun `EMAIL_CONFIRMATION_EXPIRATION`.
- `POST /v1/refresh` - rota el refresh token (via cookie) y devuelve un access token y un refresh token nuevos. Si el refresh token ya fue rotado antes y se reutiliza, se revoca toda la sesion (se asume robo).
- `POST /v1/logout` - revoca el refresh token de la sesion actual (via cookie) y limpia la cookie. El access token vigente en ese momento sigue siendo valido hasta que expira solo (maximo ~15 min), pero ya no se puede renovar.
- `POST /v1/pin` - activa (o reemplaza) el PIN de acceso rapido (6 a 8 digitos) para este dispositivo (historia "Registro con PIN"). Requiere Bearer JWT; el PIN se guarda hasheado con bcrypt, nunca en texto plano.
- `POST /v1/pin/login` - login rapido solo con PIN y `device_id`, sin pedir email. Solo funciona en un dispositivo que ya tiene un PIN configurado via `POST /v1/pin`; en cualquier otro dispositivo no hay PIN asociado y siempre falla. Devuelve la misma forma de respuesta que `/v1/login`, incluido el 202 con el desafio de 2FA si la cuenta lo tiene activo: el PIN no es una forma de esquivarlo.
- `POST /v1/password-recovery` - solicita el email de recuperacion de contraseña.
- `POST /v1/password-reset` - aplica la nueva contraseña con el token recibido por email.
- `PATCH /v1/me/profile` - actualiza nombre, descripcion y/o imagen del perfil. Requiere Bearer JWT y usa `multipart/form-data`.
- `GET /v1/me/profile` - Envia informacion del usuario. Requiere Bearer JWT del mismo
- `GET /v1/users/{id}` - perfil publico de cualquier usuario (id, name, avatar_url, status_text, status_emoji, created_at, mutual_server_ids; sin email ni description). `mutual_server_ids` son los IDs (no nombre/icono, ver PLANNING_RABBITMQ.md) de los servidores que el usuario autenticado comparte con `{id}`; vacio si no comparten ninguno o si es el propio perfil. Se alimenta de un consumer de RabbitMQ que escucha `servers.member_joined`/`servers.member_left` publicados por `discordia-servers`. Requiere Bearer JWT.
- `PUT /v1/me/status` - fija o actualiza el estado personalizado (status_text, status_emoji). Cada campo es independiente: omitirlo no lo toca, mandarlo vacio lo limpia. Requiere Bearer JWT.
- `DELETE /v1/me/status` - limpia el estado personalizado (idempotente). Requiere Bearer JWT.

Ver [`openapi.yaml`](openapi.yaml) para el detalle de cada request/response --
**generado automáticamente y actualizado solo**, no se edita a mano ni hace
falta correr nada vos. Sale de las anotaciones `@Summary`/`@Param`/`@Success`/
etc. (swag) arriba de cada handler en `internal/handler/**`. En cada
push/PR, el CI corre `make openapi` (swag → swagger2openapi, de Swagger 2.0
a OpenAPI 3.0) y si el resultado cambió respecto a lo commiteado, lo
commitea de vuelta a la rama solo. Si querés verlo antes de pushear,
`make openapi` local hace lo mismo (necesita Go y Node).

Las imagenes se almacenan localmente cuando `PROFILE_IMAGE_STORAGE=local`, y se sirven desde `PROFILE_IMAGE_URL`. Con `PROFILE_IMAGE_STORAGE=supabase`, se suben al bucket `PROFILE_IMAGE_BUCKET` mediante la API de Supabase Storage y se devuelve su URL publica; en ese modo el bucket debe ser publico. `SUPABASE_SERVICE_ROLE_KEY` solo se usa en el backend y nunca debe llegar al navegador. El servicio valida el contenido real de la imagen, acepta JPEG, PNG y GIF, limita el archivo a 5 MiB y las dimensiones a 4096x4096. El almacenamiento esta aislado detras de una interfaz para poder reemplazarlo sin cambiar el servicio de perfil.

### Supabase Storage

Crear el bucket `profile-images` en **Storage** y marcarlo como publico. Luego configurar:

```env
PROFILE_IMAGE_STORAGE=supabase
SUPABASE_URL=https://<project-ref>.supabase.co
SUPABASE_SERVICE_ROLE_KEY=<server-only-key>
PROFILE_IMAGE_BUCKET=profile-images
```

La clave `service_role` tiene privilegios elevados: guardarla solo como secreto del entorno del servicio. `PROFILE_IMAGE_PUBLIC_URL` solo es necesario si las imagenes se sirven mediante un dominio CDN o proxy propio.

## Eventos que publica
Todavia no publica eventos (no hay integracion con RabbitMQ implementada).

## Eventos que consume
- `servers.member_joined` / `servers.member_left` (exchange `discordia.events`, cola `identify.server_memberships`) - publicados por `discordia-servers` cuando un usuario se une/abandona un servidor. Se procesan de forma idempotente y tolerante a desorden (last-write-wins por `occurred_at`) para alimentar `mutual_server_ids` en `GET /v1/users/{id}`. Si `RABBITMQ_URL` no esta seteada, el consumer no se inicia y el resto del servicio sigue funcionando normalmente. Ver `PLANNING_RABBITMQ.md` para el contrato completo.

**Auditoría**: cada entrega (procesada o descartada) queda registrada en
`consumed_events` -- `internal/audit`, llamado desde `shouldRequeue` en
`internal/events/consumer.go`. Un evento válido se identifica por
`event_id` (upsert: reintentos del mismo evento actualizan la misma fila,
`attempts` cuenta cuántas veces); un payload malformado o una routing key
desconocida no tienen `event_id` parseable, así que cada uno genera su
propia fila (no hay upsert posible, y tampoco se reintregan -- se
descartan con ack inmediato). Sirve para comparar contra `outbox_events`
de `servers` y detectar un evento que se publicó pero nunca llegó -- una
falla en este registro nunca afecta el ack/nack real (es best-effort).

## Health checks
- GET /livez
- GET /readyz

## Formateo automatico
Este repo se autoformatea con gofmt en cada commit. Activalo una vez por clone:
```bash
git config core.hooksPath .githooks
```
Si usan VS Code con la extension oficial de Go, tambien formatea al guardar (ya configurado en .vscode/settings.json).

El CI corre `gofmt -l` como red de seguridad (falla si encuentra archivos sin formatear), por si alguien clono sin activar el hook.
