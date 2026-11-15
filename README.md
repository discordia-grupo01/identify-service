# identify-service

Servicio de identidad de Discordia.

## Tabla de contenidos

- [Introducción](#introducción)
- [Pre-requisitos](#pre-requisitos)
- [Cómo correr](#cómo-correr)
- [Variables de entorno](#variables-de-entorno)
- [Comandos Docker](#comandos-docker)
- [Tests](#tests)
- [Más documentación](#más-documentación)

## Introducción

`identify-service` es el servicio que sabe quién es cada usuario de Discordia. Se divide en tres partes:

- **Cuentas:** registro con confirmación por mail, registro con PIN desde la app, login con email y contraseña, login con Google, cierre de sesión y renovación de la sesión.
- **Seguridad de la cuenta:** recupero de contraseña, cambio de contraseña, autenticación de dos factores (2FA) con códigos de recuperación, y el pedido de recuperación de la cuenta cuando se pierde el segundo factor.
- **Perfil:** ver y editar el perfil propio, ver el perfil público de otro usuario, foto de perfil y estado personalizado.

Está hecho en Go con Gin y guarda todo en su propia base PostgreSQL, que ningún otro servicio lee. Es el único que emite los tokens de sesión (JWT); el gateway (Kong) los valida en cada pedido. Cuando cambia el perfil de un usuario avisa por RabbitMQ para que `servers`, `messaging` y `admin` actualicen su copia, y escucha las suspensiones que firma `admin` para bloquear la cuenta al instante.

Para mandar los mails de confirmación y de recupero usa un servidor SMTP. En local se usa MailHog, que atrapa los mails y los muestra en una página.

La API REST está descripta en [`openapi.yaml`](openapi.yaml).

## Pre-requisitos

- **Docker** y **Docker Compose** v2.24 o más nuevo.
- **Go 1.22**, solo si querés correr el servicio con Go en tu máquina.
- **make**.
- **Node.js**, solo para regenerar el `openapi.yaml` con `make openapi`.
- **RabbitMQ**, solo si querés probar los eventos. Se levanta con el repositorio [`rabbit-mq`](https://github.com/discordia-grupo01/rabbit-mq).

## Cómo correr

### Con Docker Compose

Levanta el servicio, su base PostgreSQL y MailHog, sin configurar nada:

```bash
docker compose up --build
```

Antes de arrancar el servicio, un contenedor `migrate` aplica las migraciones pendientes de la carpeta `migrations/` y termina. Cuando todo esté arriba, comprobá que el servicio esté vivo:

```bash
curl http://localhost:8080/readyz
```

Para probarlo, registrá un usuario:

```bash
curl -X POST http://localhost:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name": "Prueba", "email": "prueba@example.com", "password": "Clave-Segura-123"}'
```

El mail de confirmación no sale a internet: abrilo en MailHog, en http://localhost:8025.

Si querés cambiar puertos, usuario de la base o los secretos de prueba, copiá el archivo de ejemplo y editalo antes de levantar:

```bash
cp .env.example .env
```

Este compose levanta solo lo que es de este servicio. Para el sistema completo (con el gateway y los demás servicios) usá el compose del repositorio `infrastructure`.

### Con eventos (RabbitMQ)

Sin RabbitMQ el servicio funciona igual, pero no publica ni consume eventos. Para probarlos, levantá primero RabbitMQ desde el repositorio `rabbit-mq`:

```bash
cd ../rabbit-mq
docker compose up -d
```

Después, en el `.env` de este repositorio, completá la variable según cómo corras el servicio:

| Cómo lo corrés | Qué completar en `.env` |
|---|---|
| Con `docker compose up` | `RABBITMQ_URL_DOCKER=amqp://guest:guest@host.docker.internal:5672/` |
| Con `make run` | `RABBITMQ_URL=amqp://guest:guest@localhost:5672/` |

Desde un contenedor, `localhost` es el propio contenedor, por eso hace falta `host.docker.internal`.

### Para desarrollar

Si vas a tocar el código, te conviene correr el servicio con Go en tu máquina (así recompilás rápido y podés usar el debugger) y dejar en contenedores solo la base y MailHog:

```bash
cp .env.example .env
docker compose up -d db mailhog
make migrate
make run
```

## Variables de entorno

Se leen del entorno o de un archivo `.env`. El archivo [`.env.example`](.env.example) trae valores listos para correr en local.

| Variable | Default | Descripción |
|---|---|---|
| `ENVIRONMENT` | `development` | `development` o `production`. |
| `HOST` | `0.0.0.0` | IP donde escucha el servicio. |
| `PORT` | `8080` | Puerto donde escucha el servicio. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warning` o `error`. |
| `DATABASE_HOST` | obligatoria | Host de PostgreSQL. |
| `DATABASE_PORT` | `5432` | Puerto de PostgreSQL. |
| `DATABASE_NAME` | obligatoria | Nombre de la base. |
| `DATABASE_USER` | obligatoria | Usuario de la base. |
| `DATABASE_PASSWORD` | obligatoria | Contraseña de la base. |
| `DATABASE_SSLMODE` | `disable` | `disable` en local, `require` con Supabase. |
| `DATABASE_SCHEMA` | vacío | Schema del servicio dentro de la base. Vacío en local; en Supabase es `identify`. |
| `JWT_SECRET` | obligatoria | Secreto con el que se firman los tokens de sesión. Es un secreto. |
| `TWO_FACTOR_ENCRYPTION_KEY` | vacío | Clave con la que se cifra el secreto del segundo factor. Si está vacía se usa `JWT_SECRET`. Si la cambiás, los usuarios con 2FA tienen que volver a activarlo. |
| `GOOGLE_CLIENT_ID` | vacío | Client ID de OAuth de Google. Tiene que ser el mismo que usan la web y la app. Vacío = sin login con Google. |
| `SMTP_HOST` | vacío | Servidor SMTP para los mails. En local, MailHog. |
| `SMTP_PORT` | `587` | Puerto del servidor SMTP. |
| `SMTP_USERNAME` | vacío | Usuario SMTP. Vacío = sin autenticación. |
| `SMTP_PASSWORD` | vacío | Contraseña SMTP. Es un secreto. |
| `SMTP_FROM` | vacío | Dirección desde la que salen los mails. |
| `SMTP_REPLY_TO` | vacío | Dirección de respuesta. |
| `APP_NAME` | `Discordia` | Nombre que aparece en los mails y en la app de autenticación del 2FA. |
| `EMAIL_CONFIRMATION_URL` | `http://localhost:3000/confirm-email` | Link de la web que va en el mail de confirmación. |
| `EMAIL_CONFIRMATION_EXPIRATION` | `24h` | Cuánto dura el link de confirmación. |
| `EMAIL_CONFIRMATION_MAX_ATTEMPTS` | `3` | Cantidad máxima de pedidos de confirmación. |
| `PASSWORD_RESET_URL` | `http://localhost:3000/reset-password` | Link de la web que va en el mail de recupero. |
| `PASSWORD_RESET_EXPIRATION` | `1h` | Cuánto dura el link de recupero. |
| `PASSWORD_RESET_MAX_ATTEMPTS` | `3` | Cantidad máxima de pedidos de recupero. |
| `LOGIN_URL` | `http://localhost:3000/login` | Link al login, para el mail de recuperación de cuenta aprobada. |
| `PROFILE_IMAGE_STORAGE` | `local` | `local` o `supabase`: dónde se guardan las fotos de perfil. |
| `PROFILE_IMAGE_DIR` | `./storage/profile-images` | Carpeta de las fotos, con `local`. |
| `SUPABASE_URL` | vacío | URL del proyecto de Supabase, con `PROFILE_IMAGE_STORAGE=supabase`. |
| `SUPABASE_SERVICE_ROLE_KEY` | vacío | Clave `service_role` del proyecto. Es un secreto. |
| `PROFILE_IMAGE_BUCKET` | `profile-images` | Bucket de Supabase Storage. |
| `RABBITMQ_URL` | vacío | Conexión a RabbitMQ. Si está vacía el servicio funciona igual, pero no publica ni consume eventos. |
| `ADMIN_EVENTS_HMAC_SECRET` | vacío | Secreto compartido con `admin` para verificar las suspensiones. Obligatorio si hay `RABBITMQ_URL`. |
| `BILLING_EVENTS_HMAC_SECRET` | vacío | Secreto compartido con `billing` para verificar el estado de Premium. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | vacío | Backend OpenTelemetry (por ejemplo New Relic). Vacío = sin trazas ni métricas. |
| `OTEL_EXPORTER_OTLP_HEADERS` | vacío | Credenciales del backend. Es un secreto. |
| `LOG_SHIP_URL` | vacío | Si se completa, también manda los logs a New Relic. |

Variables que usa solo `docker-compose.yml`:

| Variable | Default | Descripción |
|---|---|---|
| `API_PORT` | `8080` | Puerto del servicio en tu máquina. |
| `DATABASE_PORT` | `5432` | Puerto de PostgreSQL en tu máquina. |
| `MAILHOG_PORT` | `8025` | Puerto de la página de MailHog en tu máquina. |
| `RABBITMQ_URL_DOCKER` | vacío | Conexión a RabbitMQ para el servicio que corre dentro del compose (ver [Con eventos](#con-eventos-rabbitmq)). |

## Comandos Docker

Todos desde la raíz del repositorio.

| Qué querés hacer | Comando |
|---|---|
| Levantar todo | `docker compose up --build` |
| Levantar todo en segundo plano | `docker compose up -d --build` |
| Levantar solo la base y MailHog | `docker compose up -d db mailhog` |
| Ver los logs del servicio | `docker compose logs -f api` |
| Ver qué está corriendo | `docker compose ps` |
| Reconstruir solo el servicio | `docker compose up -d --build api` |
| Abrir una consola de la base | `docker compose exec db psql -U discordia -d discordia_servicio` |
| Bajar todo, conservando los datos | `docker compose down` |
| Bajar todo y borrar los datos | `docker compose down -v` |

Las migraciones nuevas se aplican solas cada vez que levantás el compose.

Para construir y correr la imagen a mano, contra una base que ya tengas:

```bash
docker build -t identify-service .
docker run --rm -p 8080:8080 --env-file .env identify-service
```

Dentro de un contenedor, `DATABASE_HOST`, `SMTP_HOST` y `RABBITMQ_URL` no pueden apuntar a `localhost`: tienen que usar el nombre del contenedor o `host.docker.internal`.

## Tests

Los tests unitarios no necesitan nada más:

```bash
make test
```

Los tests de punta a punta (`test/e2e`) arman el router real contra un Postgres real y se saltean solos si no encuentran `DATABASE_HOST`. Cuidado: usan la base indicada, así que mejor no apuntarlos a una que uses para probar a mano. Creá una aparte:

```bash
docker compose up -d db
docker compose exec db psql -U discordia -c "CREATE DATABASE discordia_test"

DATABASE_HOST=localhost DATABASE_PORT=5432 DATABASE_NAME=discordia_test \
DATABASE_USER=discordia DATABASE_PASSWORD=discordia \
go test ./test/e2e/... -v
```

El test `TestMutualServersRabbitMQPipeline` además necesita RabbitMQ, que se levanta desde el repositorio `rabbit-mq`. Si no encuentra `RABBITMQ_URL`, se saltea:

```bash
RABBITMQ_URL=amqp://guest:guest@localhost:5672/ \
go test ./test/e2e/... -run TestMutualServersRabbitMQPipeline -v
```

El CI exige una cobertura mínima del 70 % y corre toda la suite en cada pull request. También hay mutation testing de la capa de servicios (`make mutation`, tarda unos 10 minutos y no corre en el CI).

El repositorio se formatea con gofmt. Activá el hook una vez por clon, y el CI falla si hay archivos sin formatear:

```bash
git config core.hooksPath .githooks
```

## Más documentación

En este repositorio:

- [`docs/historias-de-usuario.md`](docs/historias-de-usuario.md): las historias del catálogo que resuelve este servicio, con sus rutas.
- [`openapi.yaml`](openapi.yaml): contrato de la API REST. Se genera con `make openapi`, no se edita a mano.
- [`docs/mutation-testing`](docs/mutation-testing/README.md): resultados del mutation testing.

En el repositorio [`docs`](https://github.com/discordia-grupo01/docs):

- [`asyncapi`](https://github.com/discordia-grupo01/docs/tree/main/asyncapi): los eventos que este servicio publica y consume.
