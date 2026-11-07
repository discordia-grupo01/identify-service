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
| RABBITMQ_URL | - | URL de conexion a RabbitMQ. Sin ella no arrancan los consumers de membresias y de admin ni el publisher de eventos (**sin ella tampoco se aplican las suspensiones de cuentas**): los eventos `users.*` (`profile_updated`, `account_registered`, `email_verified`) quedan pendientes en `outbox_events` y se publican cuando el servicio arranque con la variable. |
| ADMIN_EVENTS_HMAC_SECRET | - | Secreto HMAC compartido con `discordia-admin` para verificar la firma de `admin.user_suspended` / `admin.user_reactivated`. **Obligatoria cuando `RABBITMQ_URL` esta seteada: si falta, el servicio no arranca** (no se aceptan eventos que no se pueden verificar). La carga el equipo en el entorno; no se genera ni se commitea. |
| BILLING_EVENTS_HMAC_SECRET | - | Secreto HMAC compartido con `discordia-billing` (y messaging) para verificar la firma de `billing.premium_updated`. Si falta, no arranca el consumer de billing y el campo `premium` de los perfiles no se actualiza; el resto del servicio funciona igual. |
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
│   ├── events/                 consumers de RabbitMQ (membresias de servidor, HU 36; admin.* de suspension) y publisher (exchange discordia.events)
│   ├── outbox/                 outbox transaccional: Insert (misma tx que la escritura) + Poller que publica
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

### Migraciones en el deploy
En cada push a `main`, el CI corre `make migrate` contra la base de
produccion (job `migrate`, despues de publicar la imagen y **antes** de
cambiarla en la VM). Si una migracion falla, no se despliega. Necesita estos
secrets del repo (Settings > Secrets and variables > Actions), con los mismos
valores que `DATABASE_*` en `~/identity.env` de la VM: `PROD_DATABASE_HOST`,
`PROD_DATABASE_PORT`, `PROD_DATABASE_NAME`, `PROD_DATABASE_USER`,
`PROD_DATABASE_PASSWORD` y `PROD_DATABASE_SSLMODE`. `PROD_DATABASE_SCHEMA`
solo si `identity.env` tiene `DATABASE_SCHEMA` (hoy no: las tablas estan en
`public`); si se setea sin que exista ese schema, `migrate` falla con
`no schema`.

Como la migracion se aplica antes del deploy, si despues el smoke test falla y
`deploy.sh` vuelve a la imagen anterior, esa imagen corre contra el schema
nuevo. Por eso las migraciones tienen que ser compatibles con la version
anterior del codigo: agregar columnas nullable o con default, y no renombrar ni
borrar nada en el mismo deploy en que se deja de usar.

Si una migracion falla a mitad de camino, golang-migrate deja la version
marcada como `dirty` y no aplica nada mas hasta resolverlo a mano: revisar que
dejo aplicado, y despues `migrate force <ultima version buena>` para que el
proximo `make migrate` la vuelva a intentar.

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
- `POST /v1/pin/login` - login rapido solo con PIN y `device_id`, sin pedir email. Solo funciona en un dispositivo que ya tiene un PIN configurado via `POST /v1/pin`; en cualquier otro dispositivo no hay PIN asociado y siempre falla. Devuelve la misma forma de respuesta que `/v1/login`, incluido el 202 con el desafio de 2FA si la cuenta lo tiene activo: el PIN no es una forma de esquivarlo. **423 (`PIN_LOCKED`)** si hubo 5 PIN incorrectos seguidos en este dispositivo: el acceso por PIN queda bloqueado 15 minutos y ni siquiera el PIN correcto es aceptado mientras dura el bloqueo -- hay que loguearse con `POST /v1/login` (email y contraseña). Un login exitoso, o reconfigurar el PIN con `POST /v1/pin`, resetea el contador.
- `POST /v1/password-recovery` - solicita el email de recuperacion de contraseña.
- `POST /v1/password-reset` - aplica la nueva contraseña con el token recibido por email.
- `PATCH /v1/me/profile` - actualiza nombre, descripcion y/o imagen del perfil. Requiere Bearer JWT y usa `multipart/form-data`.
- `GET /v1/me/profile` - Envia informacion del usuario. Requiere Bearer JWT del mismo
- `GET /v1/users/{id}` - perfil publico de cualquier usuario (id, name, avatar_url, status_text, status_emoji, created_at, mutual_server_ids; sin email ni description; `404` si el usuario esta suspendido). `mutual_server_ids` son los IDs (no nombre/icono, ver PLANNING_RABBITMQ.md) de los servidores que el usuario autenticado comparte con `{id}`; vacio si no comparten ninguno o si es el propio perfil. Se alimenta de un consumer de RabbitMQ que escucha `servers.member_joined`/`servers.member_left` publicados por `discordia-servers`. Requiere Bearer JWT.
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
- `users.profile_updated` (exchange `discordia.events`) - foto completa del perfil
  publico (`user_id`, `version`, `name`, `avatar_url`, `description`,
  `status_text`, `status_emoji`; nunca el email). Se publica al crear un usuario
  (registro con email o alta por Google) y en cada `PATCH /v1/me/profile`,
  `PUT /v1/me/status` y `DELETE /v1/me/status`. Lo consume `discordia-servers`
  para devolver el perfil de cada miembro en `GET /v1/servers/{id}/members`.
  - `version` es la columna `users.profile_version`: empieza en 1 y cada cambio
    la sube en el mismo `UPDATE`, asi que ordena las fotos de un usuario aunque
    lleguen repetidas o en desorden (el consumidor solo aplica una mayor).
  - **Outbox transaccional**: el repositorio escribe el usuario y la fila en
    `outbox_events` en la misma transaccion (`internal/repository/user_repository.go`).
    Si falla el insert en la outbox, falla la escritura entera. Un poller
    (`internal/outbox`, cada 2 s, lotes de 20, `FOR UPDATE SKIP LOCKED`) publica
    y marca `published_at`; si RabbitMQ falla, suma `attempts`, guarda
    `last_error` y reintenta en el proximo tick.

Para eliminar la foto de perfil, `PATCH /v1/me/profile` acepta
`remove_image=true` como campo del formulario multipart; el evento publica
`avatar_url` vacio para que las replicas y los clientes usen su fallback.
  - Verificar la confirmacion de email o setear contraseña no cambian el perfil
    publico: no suben `profile_version` ni publican.
  - **Backfill**: la migracion `000015_backfill_profile_events` deja un evento
    por cada usuario existente con su version actual. Reaplicarlo es seguro. Si
    hubo que relanzarlo (por ejemplo, porque se publico antes de que existiera
    la cola de `discordia-servers` y RabbitMQ descarto los mensajes), correr a
    mano el `INSERT ... SELECT` de ese archivo contra la base.
  - Deploy: `discordia-servers` va primero (declara la cola
    `servers.user_profiles`), despues se aplican las migraciones de este
    servicio con `make migrate` y se despliega. Suma una conexion a RabbitMQ.
- `users.account_registered` (exchange `discordia.events`) - aviso de alta de
  cuenta (`user_id`, `email`, `name`, `created_at` en UTC y `email_verified`:
  `true` en el alta por Google, `false` en el registro con email). Se publica una sola
  vez, al crear el usuario (registro con email o alta por Google), en la misma
  transaccion que su `users.profile_updated` (mismo outbox): si falla el insert,
  falla el alta entera. No tiene `version` porque email y fecha de alta no
  cambian; el consumidor (el listado de usuarios de `discordia-admin`) lo trata
  como idempotente. El orden entre este evento y el `profile_updated` del alta
  es arbitrario.
  - **Privacidad**: lleva el email. Solo la cola de `discordia-admin` debe
    enlazarse a esta routing key; ningun otro servicio deberia bindearla.
  - **Backfill**: la migracion `000017_backfill_account_registered` deja un
    evento por cada usuario existente. Reaplicarlo es seguro; para relanzarlo a
    mano, correr el `INSERT ... SELECT` de ese archivo.
- `users.email_verified` (exchange `discordia.events`) - aviso de que el usuario
  confirmo su email (`user_id`, `verified_at` en UTC). Se publica una sola vez,
  en la misma transaccion que marca `email_verified` (mismo outbox): si el email
  ya estaba verificado no se publica de nuevo, y si falla el insert la
  verificacion hace rollback. El alta por Google no lo publica (nace verificada
  en `users.account_registered`). El backoffice de `discordia-admin` lo usa para
  dejar de mostrar al usuario como "pendiente".
  - **Privacidad**: igual que `users.account_registered`, solo la cola de
    `discordia-admin` debe enlazarse a esta routing key.
  - **Backfill**: la migracion `000019_backfill_email_verified` deja un evento
    por cada usuario que ya tenia el email verificado. Reaplicarlo es seguro;
    para relanzarlo a mano, correr el `INSERT ... SELECT` de ese archivo.
  - Deploy: `discordia-admin` va primero (enlaza `admin.users` a esta routing
    key); si identify publica antes, RabbitMQ descarta los eventos y hay que
    relanzar el backfill a mano.

## Eventos que consume
- `servers.member_joined` / `servers.member_left` (exchange `discordia.events`, cola `identify.server_memberships`) - publicados por `discordia-servers` cuando un usuario se une/abandona un servidor. Se procesan de forma idempotente y tolerante a desorden (last-write-wins por `occurred_at`) para alimentar `mutual_server_ids` en `GET /v1/users/{id}`. Si `RABBITMQ_URL` no esta seteada, el consumer no se inicia y el resto del servicio sigue funcionando normalmente. Ver `PLANNING_RABBITMQ.md` para el contrato completo.
- `admin.user_suspended` / `admin.user_reactivated` (exchange `discordia.events`, cola durable `identify.admin_events`) - publicados por `discordia-admin` cuando el staff suspende o reactiva una cuenta. La decision es solo de admin; identify guarda el estado en `users.suspended_at` / `users.suspension_version` y lo aplica.
  - **Firma**: cada evento viaja firmado con HMAC (`ADMIN_EVENTS_HMAC_SECRET`, `Verify` de `events-contract/admin`). Sin firma, con firma invalida o con la firma de otra routing key se descarta con ack (no se reencola), se loguea `warn` con `event_id` y routing key, se audita como `discarded_invalid_signature` y **no cambia ningun estado**.
  - **Version**: se aplica solo si `version` es mayor a `suspension_version` guardada; un duplicado o un evento fuera de orden es un no-op confirmado. Suspender guarda `suspended_at = occurred_at` y **revoca todos los refresh tokens del usuario en la misma transaccion**; reactivar deja `suspended_at = NULL`.
  - Un `user_id` inexistente se confirma y se audita (`discarded_unknown_user`) sin crear nada; un mensaje roto o con `user_id` que no es UUID se descarta (`discarded_malformed_payload` / `discarded_invalid_payload`). Solo una falla transitoria de la base se reencola (nack requeue).
  - **Efectos de una cuenta suspendida**: `403 ACCOUNT_SUSPENDED` en el login (contraseña, 2FA, Google y PIN), en `POST /v1/refresh` y en cualquier endpoint autenticado (`RequireAuth` consulta el estado en cada request, con una cache en memoria de 5 s que se invalida en el momento en que esta instancia aplica el evento: un access token vigente deja de servir). `GET /v1/users/{id}` de un suspendido devuelve `404`. No se borra nada de lo que el usuario hizo. Reactivarlo basta para que vuelva a entrar.
  - El motivo de la suspension no viaja en los eventos (minimizacion de datos).
- `billing.premium_updated` (exchange `discordia.events`, cola durable `identify.billing_events`) - publicado por `discordia-billing` cada vez que cambia el Premium de un usuario (pago, renovacion, cancelacion, devolucion o vencimiento). Es una foto completa; identify solo guarda `active` en `users.premium_active` y lo muestra como `premium` en `GET /v1/me/profile` y `GET /v1/users/{id}`.
  - **Firma**: igual que los de admin, pero con `BILLING_EVENTS_HMAC_SECRET` (`Verify` de `events-contract/billing`). Un evento sin firma valida se descarta con ack y no cambia nada: un evento falso regalaria Premium.
  - **Version**: se aplica solo si `version` es mayor a `users.premium_version`; un duplicado o un evento fuera de orden es un no-op confirmado. Un `user_id` inexistente se confirma sin crear nada, y solo una falla transitoria de la base se reencola.
  - Si `BILLING_EVENTS_HMAC_SECRET` no esta seteada, el consumer no se inicia y `premium` queda en `false`.

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
- `GET /livez` - el proceso está vivo. No consulta la base ni ningún servicio externo.
- `GET /readyz` - el servicio puede operar (pinguea la base; 503 si no responde). No comparte lógica con `/livez`.

## Logs

Cada línea que escribe el servicio es un objeto JSON por la salida estándar, con `time`, `level` (`debug`, `info`, `warning` o `error`), `service` (`identify-service`) y `message`. Es el mismo formato que usan `messaging`, `notifications` y `voice-sfu`.

- **Nivel configurable** con `LOG_LEVEL` (`debug`, `info`, `warning` o `error`; por defecto `info`). Un valor inválido hace fallar el arranque con un mensaje claro. Al arrancar escribe `service_started` con el nivel elegido.
- **Qué significa cada nivel:**
  - `error`: falla que impide ejecutar la funcionalidad (no se pudo guardar, la base no responde, un evento agotó los reintentos).
  - `warning`: condición anómala que no impide la funcionalidad básica (un evento descartado por inválido o por firma mala, una conexión a RabbitMQ que se reintenta, una configuración opcional que falta).
  - `info`: acción iniciada por una persona o por el sistema y trazable (evento procesado, barrido de vencimientos, pedido HTTP).
  - `debug`: contexto técnico para diagnosticar (eventos ignorados por viejos o repetidos, `/livez` y `/readyz` cuando responden bien).
- **Pedidos HTTP:** se registran como `http_request` con `method`, `route`, `status` y `duration_ms`. `route` es el **patrón** (`/v1/servers/:serverId`), nunca la ruta real ni la query string, porque ahí viajan ids, invitaciones y tokens. Un pedido sin ruta sale como `unmatched`. Un 5xx se registra como `error`.
- **Qué no se escribe:** el valor de campos llamados `password`, `token`, `ticket`, `jwt`, `authorization`, `cookie`, `secret`, `api_key`, `email` o `content` sale como `[FILTERED]`. Como red de seguridad, cualquier email o JWT que aparezca dentro de un texto (por ejemplo, en el mensaje de un error de la base) se reemplaza por `[EMAIL]` o `[JWT]`.
- **Pánicos:** se registran como `panic_recovered` con la ruta, el error y la traza, sin volcar los headers del pedido.
- El código vive en `internal/logging`. Las llamadas viejas al paquete `log` de Go también salen como JSON, porque `Setup` redirige el logger estándar.

## Formateo automatico
Este repo se autoformatea con gofmt en cada commit. Activalo una vez por clone:
```bash
git config core.hooksPath .githooks
```
Si usan VS Code con la extension oficial de Go, tambien formatea al guardar (ya configurado en .vscode/settings.json).

El CI corre `gofmt -l` como red de seguridad (falla si encuentra archivos sin formatear), por si alguien clono sin activar el hook.
