CREATE TABLE IF NOT EXISTS password_recoveries (
    id BIGSERIAL PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email VARCHAR(255) NOT NULL,
    token_hash CHAR(64) UNIQUE NOT NULL,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    used_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_password_recoveries_email_created_at
    ON password_recoveries(email, created_at);
CREATE INDEX IF NOT EXISTS idx_password_recoveries_token_active
    ON password_recoveries(token_hash, expires_at)
    WHERE used_at IS NULL;