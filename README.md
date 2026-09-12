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
│   ├── repository/            acceso a datos: users, sessions, password_recoveries
│   ├── model/                 structs de dominio / DTOs
│   ├── middleware/            middlewares de Gin (auth, logging, etc.)
│   ├── utils/email/            envio de emails (SMTP) para recuperacion de contraseña
│   └── events/                 publish/consume de eventos (RabbitMQ, etc.) - todavia sin uso
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
`internal/` esta pensado para no exponer estos paquetes fuera del modulo (restriccion de Go). Solo `internal/events/` sigue vacia (`.gitkeep`): todavia no se publican ni consumen eventos desde este servicio.

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
- `POST /v1/login` - login, devuelve el JWT.
- `POST /v1/logout` - revoca la sesion actual.
- `POST /v1/password-recovery` - solicita el email de recuperacion de contraseña.
- `POST /v1/password-reset` - aplica la nueva contraseña con el token recibido por email.
- `PATCH /v1/me/profile` - actualiza nombre, descripcion y/o imagen del perfil. Requiere Bearer JWT y usa `multipart/form-data`.
- `GET /v1/me/profile` - Envia informacion del usuario. Requiere Bearer JWT del mismo

Ver `openapi.yaml` para el detalle de cada request/response.

Las imagenes locales se guardan en `PROFILE_IMAGE_DIR` y se sirven desde `PROFILE_IMAGE_URL`. El servicio valida el contenido real de la imagen, acepta JPEG, PNG y GIF, limita el archivo a 5 MiB y las dimensiones a 4096x4096. El almacenamiento esta aislado detras de una interfaz para poder reemplazarlo por Oracle Object Storage sin cambiar el servicio de perfil.

## Eventos que publica
Todavia no publica eventos (no hay integracion con RabbitMQ implementada).

## Eventos que consume
Todavia no consume eventos.

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
