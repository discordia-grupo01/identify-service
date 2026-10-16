-- Backfill: un users.account_registered por cada usuario existente, para que
-- el listado de usuarios del backoffice (discordia-admin) los vea a todos.
-- Reaplicarlo es seguro: admin lo trata como idempotente (el email y la fecha
-- de alta no cambian). Para relanzarlo a mano, correr este mismo INSERT.
INSERT INTO outbox_events (id, routing_key, payload)
SELECT gen_random_uuid(),
       'users.account_registered',
       jsonb_build_object(
           'event_id',    gen_random_uuid()::text,
           'occurred_at', to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
           'user_id',     id::text,
           'email',       email,
           'name',        name,
           'created_at',  to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
       )
  FROM users;
