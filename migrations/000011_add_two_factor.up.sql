CREATE TABLE IF NOT EXISTS user_two_factor (
    user_id        UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    secret         TEXT NOT NULL,
    confirmed_at   TIMESTAMP WITH TIME ZONE,
    -- Ultimo paso de 30s ya consumido. Impide reusar el mismo codigo dos veces
    -- mientras sigue siendo valido (quien lo vea de reojo no puede repetirlo).
    last_used_step BIGINT NOT NULL DEFAULT 0,
    created_at     TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS two_factor_recovery_codes (
    id         UUID PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  VARCHAR(64) NOT NULL,
    used_at    TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_two_factor_recovery_codes_user_id
    ON two_factor_recovery_codes(user_id);

CREATE TABLE IF NOT EXISTS two_factor_challenges (
    id         UUID PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash VARCHAR(64) NOT NULL UNIQUE,
    attempts   INTEGER NOT NULL DEFAULT 0,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    used_at    TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_two_factor_challenges_user_id
    ON two_factor_challenges(user_id);
