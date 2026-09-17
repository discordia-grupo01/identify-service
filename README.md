# identify-service

Servicio de identidad de Discordia: registro, login, sesiones y recuperacion de contraseña. Es donde vive todo lo de usuarios (y, a futuro, perfil).

## Stack
- Lenguaje: Go
- Framework: Gin (github.com/gin-gonic/gin)
- BDD: PostgreSQL (Supabase en produccion, ver seccion "Supabase" mas abajo)
- Infra extra: SMTP (envio de emails de recuperacion de contraseña)

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
| PROFILE_IMAGE_DIR | ./storage/profile-images | Directorio local para imagenes de perfil. |
| PROFILE_IMAGE_URL | /uploads/profile-images | URL publica base de las imagenes de perfil. |

`.env.example` solo trae la estructura (sin valores sensibles). Los valores reales van en tu propio `.env`, que nunca se sube (ya esta en `.gitignore`).

## Estructura de carpetas
```
identify-service/
├── cmd/
│   └── api/
│       ├── main.go          entrypoint: arma el router y levanta el server
│       └── database.go       conexion a Postgres (initDB)
├── internal/
│   ├── handler/              handlers HTTP: auth (registro/login/logout) y password recovery
│   ├── service/               logica de negocio (auth_service, password_recovery_service)
│   ├── repository/            acceso a datos: users, refresh_tokens, password_recoveries
│   ├── model/                 structs de dominio / DTOs
│   ├── middleware/            middlewares de Gin (auth, logging, etc.)
│   ├── utils/email/            envio de emails (SMTP) para recuperacion de contraseña
│   └── events/                 consumer de RabbitMQ: membresias de servidor para "servidores en comun" (HU 36)
├── migrations/                migraciones de BDD (golang-migrate)
├── .github/
│   └── workflows/
│       └── ci.yml            gofmt, build, vet y test en cada PR/push a main
├── .githooks/
│   └── pre-commit             hook de gofmt local
├── openapi.yaml               contrato de la API REST
├── docker-compose.yml         levanta el servicio + Postgres para desarrollo local
├── Dockerfile                 build multi-stage para la imagen de produccion
├── Makefile                   comandos run/test/build/migrate
└── .env.example                estructura de variables de entorno (sin valores sensibles)
```
`internal/` esta pensado para no exponer estos paquetes fuera del modulo (restriccion de Go).

## Comandos (Makefile)
- `make run` - corre el servicio localmente (`go run ./cmd/api`).
- `make test` - corre los tests con cobertura (`go test ./... -cover`).
- `make build` - compila el binario en `bin/api`.
- `make migrate` - corre las migraciones (golang-migrate) contra la BDD que indique `.env` (local o Supabase).

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

## Supabase (produccion)
Este servicio no usa Supabase Auth: el login/JWT/sesiones son 100% propios. "Migrar a Supabase" es solo cambiar a que Postgres se conecta, no la logica de auth.

El proyecto de Supabase es **compartido entre todos los servicios de Discordia** (un solo proyecto, un schema por servicio). Pasos:
1. Una sola vez por proyecto de Supabase: crear el schema de este servicio. Se puede hacer sin SQL desde **Database > Schemas > New schema** (nombre `identify`), o corriendo en el SQL Editor:
   ```sql
   CREATE SCHEMA IF NOT EXISTS identify;
   ```
2. Completar en tu `.env` los datos de conexion de Supabase, tomados del boton **Connect** del proyecto, pestaña **Session pooler** (no "Direct connection": ese host solo resuelve por IPv6 y muchas redes no tienen esa salida; tampoco "Transaction pooler", que no sostiene el `search_path` por conexion):
   ```
   DATABASE_HOST=aws-0-<region>.pooler.supabase.com
   DATABASE_PORT=5432
   DATABASE_NAME=postgres
   DATABASE_USER=postgres.<project-ref>
   DATABASE_PASSWORD=<password del proyecto>
   DATABASE_SSLMODE=require
   DATABASE_SCHEMA=identify
   ```
3. Correr `make migrate` y despues `make run`. Confirmar con `GET /readyz` (200) y un `POST /v1/users` + `POST /v1/login` de prueba.

## Endpoints
- `POST /v1/users` - registro de usuario.
- `POST /v1/login` - login. Devuelve un access token (JWT, ~15 min) en el body y setea el refresh token (~30 dias) como cookie httpOnly; el refresh token nunca viaja en el body.
- `POST /v1/refresh` - rota el refresh token (via cookie) y devuelve un access token y un refresh token nuevos. Si el refresh token ya fue rotado antes y se reutiliza, se revoca toda la sesion (se asume robo).
- `POST /v1/logout` - revoca el refresh token de la sesion actual (via cookie) y limpia la cookie. El access token vigente en ese momento sigue siendo valido hasta que expira solo (maximo ~15 min), pero ya no se puede renovar.
- `POST /v1/password-recovery` - solicita el email de recuperacion de contraseña.
- `POST /v1/password-reset` - aplica la nueva contraseña con el token recibido por email.
- `PATCH /v1/me/profile` - actualiza nombre, descripcion y/o imagen del perfil. Requiere Bearer JWT y usa `multipart/form-data`.
- `GET /v1/me/profile` - Envia informacion del usuario. Requiere Bearer JWT del mismo
- `GET /v1/users/{id}` - perfil publico de cualquier usuario (id, name, avatar_url, status_text, status_emoji, created_at, mutual_server_ids; sin email ni description). `mutual_server_ids` son los IDs (no nombre/icono, ver PLANNING_RABBITMQ.md) de los servidores que el usuario autenticado comparte con `{id}`; vacio si no comparten ninguno o si es el propio perfil. Se alimenta de un consumer de RabbitMQ que escucha `servers.member_joined`/`servers.member_left` publicados por `discordia-servers`. Requiere Bearer JWT.
- `PUT /v1/me/status` - fija o actualiza el estado personalizado (status_text, status_emoji). Cada campo es independiente: omitirlo no lo toca, mandarlo vacio lo limpia. Requiere Bearer JWT.
- `DELETE /v1/me/status` - limpia el estado personalizado (idempotente). Requiere Bearer JWT.

Ver `openapi.yaml` para el detalle de cada request/response.

Las imagenes locales se guardan en `PROFILE_IMAGE_DIR` y se sirven desde `PROFILE_IMAGE_URL`. El servicio valida el contenido real de la imagen, acepta JPEG, PNG y GIF, limita el archivo a 5 MiB y las dimensiones a 4096x4096. El almacenamiento esta aislado detras de una interfaz para poder reemplazarlo por Oracle Object Storage sin cambiar el servicio de perfil.

## Eventos que publica
Todavia no publica eventos (no hay integracion con RabbitMQ implementada).

## Eventos que consume
- `servers.member_joined` / `servers.member_left` (exchange `discordia.events`, cola `identify.server_memberships`) - publicados por `discordia-servers` cuando un usuario se une/abandona un servidor. Se procesan de forma idempotente y tolerante a desorden (last-write-wins por `occurred_at`) para alimentar `mutual_server_ids` en `GET /v1/users/{id}`. Si `RABBITMQ_URL` no esta seteada, el consumer no se inicia y el resto del servicio sigue funcionando normalmente. Ver `PLANNING_RABBITMQ.md` para el contrato completo.

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
