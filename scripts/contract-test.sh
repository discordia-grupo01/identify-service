#!/usr/bin/env bash
# Valida que identify cumpla su openapi.yaml con Schemathesis.
# Levanta el servicio contra un Postgres y un RabbitMQ LOCALES y efimeros y un
# SMTP falso local (no se envia ningun mail), nunca contra servicios remotos.
# Uso: make contract-test (antes, make contract-deps-up en local).
#
# Operaciones excluidas del fuzzing (tambien en el README):
#   POST /v1/oauth/google: con un token de forma valida el servicio contacta a
#   accounts.google.com para validarlo (llamada a un tercero real).
set -euo pipefail

cd "$(dirname "$0")/.."

# Puertos de la tabla del plan de contratos (identify).
DB_HOST="${CONTRACT_DB_HOST:-localhost}"
DB_PORT="${CONTRACT_DB_PORT:-55504}"
MQ_HOST="${CONTRACT_MQ_HOST:-localhost}"
MQ_PORT="${CONTRACT_MQ_PORT:-55514}"
SMTP_HOST="${CONTRACT_SMTP_HOST:-127.0.0.1}"
SMTP_PORT="${CONTRACT_SMTP_PORT:-55524}"
APP_PORT="${CONTRACT_APP_PORT:-18084}"
MAX_EXAMPLES="${CONTRACT_MAX_EXAMPLES:-25}"
DB_NAME="${CONTRACT_DB_NAME:-discordia_identify_contract}"

# Valores de juguete: no son secretos reales.
JWT_SECRET="contract-test-secret"
USER_ID="00000000-0000-4000-8000-0000000000b1"

# Guard: se rechaza todo host que no sea local (base, broker y SMTP).
for host in "$DB_HOST" "$MQ_HOST" "$SMTP_HOST"; do
  case "$host" in
    localhost | 127.0.0.1 | ::1) ;;
    *)
      echo "contract-test: host no local '$host'; este script solo corre contra servicios locales." >&2
      exit 1
      ;;
  esac
done

export MSYS_NO_PATHCONV=1
export PYTHONIOENCODING=utf-8 PYTHONUTF8=1
PY="${PYTHON:-python}"
VENV=".contract-venv"
if [ -x "$VENV/bin/schemathesis" ]; then
  ST="$VENV/bin/schemathesis"
elif [ -x "$VENV/Scripts/schemathesis.exe" ]; then
  ST="$VENV/Scripts/schemathesis.exe"
elif command -v schemathesis >/dev/null 2>&1; then
  ST="schemathesis"
else
  "$PY" -m venv "$VENV"
  if [ -x "$VENV/bin/pip" ]; then PIP="$VENV/bin/pip"; else PIP="$VENV/Scripts/pip.exe"; fi
  "$PIP" install -q "schemathesis==4.33.4"
  if [ -x "$VENV/bin/schemathesis" ]; then ST="$VENV/bin/schemathesis"; else ST="$VENV/Scripts/schemathesis.exe"; fi
fi

# Siembra el usuario del token en la base efimera: con psql si esta, o dentro
# del contenedor de contract-deps-up.
if command -v psql >/dev/null 2>&1; then
  PGPASSWORD=discordia psql -q -h "$DB_HOST" -p "$DB_PORT" -U discordia -d "$DB_NAME" -v ON_ERROR_STOP=1 -f scripts/contract/seed.sql
else
  docker exec -i contract-identify-pg psql -q -U discordia -d "$DB_NAME" -v ON_ERROR_STOP=1 <scripts/contract/seed.sql
fi

TOKEN="$("$PY" scripts/contract/make_token.py "$USER_ID" "$JWT_SECRET" 3600)"

LOG="$(mktemp)"
STORAGE="$(mktemp -d)"
export ENVIRONMENT=development HOST=127.0.0.1 PORT="$APP_PORT"
export DATABASE_HOST="$DB_HOST" DATABASE_PORT="$DB_PORT" DATABASE_USER=discordia DATABASE_PASSWORD=discordia
export DATABASE_NAME="$DB_NAME" DATABASE_SSLMODE=disable DATABASE_SCHEMA=
export RABBITMQ_URL="amqp://guest:guest@$MQ_HOST:$MQ_PORT/"
export JWT_SECRET TWO_FACTOR_ENCRYPTION_KEY="contract-test-2fa-key"
export ADMIN_EVENTS_HMAC_SECRET="contract-test-hmac" BILLING_EVENTS_HMAC_SECRET="contract-test-hmac"
export SMTP_HOST SMTP_PORT SMTP_FROM="contract@example.test"
export PROFILE_IMAGE_STORAGE=local PROFILE_IMAGE_DIR="$STORAGE"
export GOOGLE_CLIENT_ID="" SUPABASE_URL="" SUPABASE_SERVICE_ROLE_KEY=""

"$PY" scripts/contract/smtp_sink.py "$SMTP_PORT" &
SMTP_PID=$!

go build -o .contract-api ./cmd/api
./.contract-api >"$LOG" 2>&1 &
APP_PID=$!
cleanup() {
  kill "$APP_PID" "$SMTP_PID" 2>/dev/null || true
  rm -rf .contract-api .contract-api.exe "$LOG" "$STORAGE"
}
trap cleanup EXIT

for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:$APP_PORT/readyz" >/dev/null 2>&1; then break; fi
  if ! kill -0 "$APP_PID" 2>/dev/null; then
    echo "contract-test: el servicio no arranco:" >&2
    cat "$LOG" >&2
    exit 1
  fi
  sleep 1
done

"$ST" --config-file scripts/contract/schemathesis.toml run openapi.yaml \
  --url "http://127.0.0.1:$APP_PORT" \
  -H "Authorization: Bearer $TOKEN" \
  --max-examples "$MAX_EXAMPLES" \
  --seed 1 \
  --workers 1 \
  --exclude-path /v1/oauth/google
