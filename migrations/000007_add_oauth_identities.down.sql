DROP TABLE IF EXISTS oauth_identities;

ALTER TABLE users ALTER COLUMN password_hash SET NOT NULL;
