package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/discordia-grupo01/identify-service/internal/model"
)

type DevicePinRepository interface {
	GetByDeviceID(ctx context.Context, deviceID string) (*model.DevicePin, error)
	Upsert(ctx context.Context, pin *model.DevicePin) error
}

type postgresDevicePinRepository struct {
	db *sql.DB
}

func NewDevicePinRepository(db *sql.DB) DevicePinRepository {
	return &postgresDevicePinRepository{db: db}
}

func (r *postgresDevicePinRepository) GetByDeviceID(ctx context.Context, deviceID string) (*model.DevicePin, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, device_id, pin_hash, created_at, updated_at
		FROM device_pins WHERE device_id = $1`, deviceID)

	var pin model.DevicePin
	err := row.Scan(&pin.ID, &pin.UserID, &pin.DeviceID, &pin.PinHash, &pin.CreatedAt, &pin.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &pin, nil
}

func (r *postgresDevicePinRepository) Upsert(ctx context.Context, pin *model.DevicePin) error {
	query := `
		INSERT INTO device_pins (id, user_id, device_id, pin_hash)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (device_id) DO UPDATE
		SET user_id = EXCLUDED.user_id, pin_hash = EXCLUDED.pin_hash, updated_at = CURRENT_TIMESTAMP
		RETURNING id, created_at, updated_at`

	return r.db.QueryRowContext(ctx, query, pin.ID, pin.UserID, pin.DeviceID, pin.PinHash).
		Scan(&pin.ID, &pin.CreatedAt, &pin.UpdatedAt)
}
