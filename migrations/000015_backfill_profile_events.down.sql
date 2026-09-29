-- No-op: los eventos del backfill no se distinguen de los que generan los
-- cambios de perfil, así que no se borran (borrar uno pendiente perdería un
-- cambio real). Son idempotentes para el consumidor, dejarlos no rompe nada.
SELECT 1;
