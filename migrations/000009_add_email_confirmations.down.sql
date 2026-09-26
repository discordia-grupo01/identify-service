DROP TABLE IF EXISTS email_confirmations;

ALTER TABLE users
    DROP COLUMN IF EXISTS verified_at,
    DROP COLUMN IF EXISTS email_verified;
