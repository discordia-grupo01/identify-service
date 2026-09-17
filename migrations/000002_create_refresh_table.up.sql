DROP TABLE IF EXISTS sessions;
 
CREATE TABLE IF NOT EXISTS refresh_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  VARCHAR(64) NOT NULL,           -- SHA-256 del refresh token real
    family_id   UUID NOT NULL,                  -- agrupa la cadena de rotaciones de una sesión
    expires_at  TIMESTAMP WITH TIME ZONE NOT NULL,
    revoked_at  TIMESTAMP WITH TIME ZONE,
    created_at  TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
 
CREATE UNIQUE INDEX IF NOT EXISTS idx_refresh_tokens_hash
    ON refresh_tokens (token_hash);
 
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id
    ON refresh_tokens (user_id);
 
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_family_active
    ON refresh_tokens (family_id)
    WHERE revoked_at IS NULL;
 
-- Housekeeping opcional: correr periódicamente (cron / job) para no
-- acumular filas muertas indefinidamente. No es parte del flujo de negocio.
-- DELETE FROM refresh_tokens
-- WHERE revoked_at IS NOT NULL OR expires_at < CURRENT_TIMESTAMP - INTERVAL '30 days';