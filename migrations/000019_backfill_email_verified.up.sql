-- Backfill: un users.email_verified por cada usuario con el email ya
-- verificado, para que el backoffice (discordia-admin) no los muestre como
-- pendientes. Reaplicarlo es seguro: admin lo trata como idempotente (la
-- verificación no se deshace). Para relanzarlo a mano, correr este mismo INSERT.
INSERT INTO outbox_events (id, routing_key, payload)
SELECT gen_random_uuid(),
       'users.email_verified',
       jsonb_build_object(
           'event_id',    gen_random_uuid()::text,
           'occurred_at', to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
           'user_id',     id::text,
           'verified_at', to_char(COALESCE(verified_at, created_at) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
       )
  FROM users
 WHERE email_verified;
