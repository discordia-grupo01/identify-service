-- Pedidos de un usuario con 2FA activo que perdió la app autenticadora y los
-- códigos de recuperación. Los revisa el staff desde el backoffice (admin) y
-- la respuesta vuelve firmada por evento.
CREATE TABLE IF NOT EXISTS account_recovery_requests (
    id          UUID PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    message     TEXT NOT NULL DEFAULT '',
    status      VARCHAR(16) NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'approved', 'rejected')),
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at TIMESTAMP WITH TIME ZONE
);

-- Una sola solicitud pendiente por usuario. El índice la garantiza aunque
-- lleguen dos pedidos a la vez.
CREATE UNIQUE INDEX IF NOT EXISTS ux_account_recovery_requests_pending
    ON account_recovery_requests(user_id) WHERE status = 'pending';
