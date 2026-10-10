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
