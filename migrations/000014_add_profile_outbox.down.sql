DROP TABLE IF EXISTS outbox_events;

ALTER TABLE users DROP COLUMN IF EXISTS profile_version;
