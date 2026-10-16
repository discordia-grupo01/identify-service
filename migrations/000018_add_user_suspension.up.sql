-- Suspensión de cuenta decidida por el staff (discordia-admin). identify solo
-- guarda el estado: suspended_at es NULL si la cuenta está activa, y
-- suspension_version es la versión del último admin.user_suspended /
-- admin.user_reactivated aplicado (solo se aplica un evento con versión mayor).
ALTER TABLE users ADD COLUMN IF NOT EXISTS suspended_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS suspension_version BIGINT NOT NULL DEFAULT 0;
