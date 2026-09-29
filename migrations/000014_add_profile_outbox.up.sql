-- profile_version ordena las fotos del perfil público de un usuario que viajan
-- en users.profile_updated: sube en cada cambio de perfil o estado, y el
-- consumidor solo aplica una versión mayor a la que ya tiene.
ALTER TABLE users ADD COLUMN IF NOT EXISTS profile_version BIGINT NOT NULL DEFAULT 1;

-- Outbox transaccional: cada escritura del perfil inserta acá su evento en la
-- misma transacción, y un poller lo publica en RabbitMQ (mismo diseño que
-- discordia-servers).
CREATE TABLE IF NOT EXISTS outbox_events (
    id           UUID PRIMARY KEY,
    routing_key  TEXT NOT NULL,
    payload      JSONB NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    attempts     INTEGER NOT NULL DEFAULT 0,
    last_error   TEXT
);

CREATE INDEX IF NOT EXISTS idx_outbox_events_pending ON outbox_events (created_at) WHERE published_at IS NULL;
