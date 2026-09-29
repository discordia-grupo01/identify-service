-- Backfill: un users.profile_updated por cada usuario existente, con su
-- profile_version actual, para que los consumidores (discordia-servers) armen
-- su réplica de perfiles. Reaplicarlo es seguro: el consumidor ignora una
-- versión que ya tiene. Para relanzarlo a mano, correr este mismo INSERT.
INSERT INTO outbox_events (id, routing_key, payload)
SELECT gen_random_uuid(),
       'users.profile_updated',
       jsonb_build_object(
           'event_id',     gen_random_uuid()::text,
           'occurred_at',  to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
           'user_id',      id::text,
           'version',      profile_version,
           'name',         name,
           'avatar_url',   avatar_url,
           'description',  description,
           'status_text',  status_text,
           'status_emoji', status_emoji
       )
  FROM users;
