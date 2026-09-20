CREATE TABLE IF NOT EXISTS server_memberships (
    user_id    TEXT NOT NULL,
    server_id  UUID NOT NULL,
    is_member  BOOLEAN NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, server_id)
);

CREATE INDEX IF NOT EXISTS idx_server_memberships_active_user ON server_memberships (user_id) WHERE is_member;
