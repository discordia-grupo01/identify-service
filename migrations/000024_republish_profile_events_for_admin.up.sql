-- Re-publica un users.profile_updated por usuario, con su profile_version
-- actual, para que discordia-admin complete la foto de perfil (avatar_url) de
-- los usuarios que ya tenía: antes la descartaba al recibir el evento.
--
-- Admin acepta la misma versión solo mientras el avatar del usuario es
-- desconocido; servers y messaging ignoran una versión que ya tienen, así que
-- para ellos es inofensivo. Admin tiene que estar desplegado (con la columna
-- avatar_url) antes que esta migración. Para relanzarlo a mano, correr este
-- mismo INSERT.
INSERT INTO outbox_events (id, routing_key, payload)
SELECT gen_random_uuid(),
       'users.profile_updated',
       jsonb_build_object(
           'event_id',     gen_random_uuid()::text,
           'occurred_at',  to_char(clock_timestamp() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
           'user_id',      id::text,
           'version',      profile_version,
           'name',         name,
           'avatar_url',   avatar_url,
           'description',  description,
           'status_text',  status_text,
           'status_emoji', status_emoji
       )
  FROM users;
