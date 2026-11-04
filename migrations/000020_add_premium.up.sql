-- Premium lo decide billing (que cobra con Mercado Pago) y llega por
-- billing.premium_updated. identify solo guarda la réplica: premium_active es
-- lo que se muestra en el perfil, y premium_version es la versión del último
-- evento aplicado (solo se aplica uno con versión mayor).
ALTER TABLE users ADD COLUMN IF NOT EXISTS premium_active BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN IF NOT EXISTS premium_version BIGINT NOT NULL DEFAULT 0;
