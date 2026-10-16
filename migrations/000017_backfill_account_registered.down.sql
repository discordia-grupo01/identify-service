-- No-op: los eventos del backfill no se distinguen de los que genera un alta
-- real, así que no se borran (borrar uno pendiente perdería un aviso real).
-- Son idempotentes para el consumidor, dejarlos no rompe nada.
SELECT 1;
