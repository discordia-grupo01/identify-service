ALTER TABLE users
    DROP COLUMN IF EXISTS avatar_url,
    DROP COLUMN IF EXISTS description;