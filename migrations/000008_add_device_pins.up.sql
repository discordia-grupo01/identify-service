CREATE TABLE IF NOT EXISTS device_pins (
    id         UUID PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id  VARCHAR(255) NOT NULL,
    pin_hash   VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (device_id)
);

CREATE INDEX IF NOT EXISTS idx_device_pins_user_id ON device_pins(user_id);
