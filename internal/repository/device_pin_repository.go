package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
)

type DevicePinRepository interface {
	GetByDeviceID(ctx context.Context, deviceID string) (*model.DevicePin, error)
	Upsert(ctx context.Context, pin *model.DevicePin) error
	IncrementFailedAttempts(ctx context.Context, deviceID string) (int, error)
	Lock(ctx context.Context, deviceID string, until time.Time) error
	ResetFailedAttempts(ctx context.Context, deviceID string) error
}

type postgresDevicePinRepository struct {
	db *sql.DB
}

func NewDevicePinRepository(db *sql.DB) DevicePinRepository {
	return &postgresDevicePinRepository{db: db}
}

func (r *postgresDevicePinRepository) GetByDeviceID(ctx context.Context, deviceID string) (*model.DevicePin, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, device_id, pin_hash, failed_attempts, locked_until, created_at, updated_at
		FROM device_pins WHERE device_id = $1`, deviceID)

	var pin model.DevicePin
	err := row.Scan(&pin.ID, &pin.UserID, &pin.DeviceID, &pin.PinHash, &pin.FailedAttempts, &pin.LockedUntil, &pin.CreatedAt, &pin.UpdatedAt)
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
		SET user_id = EXCLUDED.user_id, pin_hash = EXCLUDED.pin_hash,
			failed_attempts = 0, locked_until = NULL, updated_at = CURRENT_TIMESTAMP
		RETURNING id, created_at, updated_at`

	return r.db.QueryRowContext(ctx, query, pin.ID, pin.UserID, pin.DeviceID, pin.PinHash).
		Scan(&pin.ID, &pin.CreatedAt, &pin.UpdatedAt)
}

func (r *postgresDevicePinRepository) IncrementFailedAttempts(ctx context.Context, deviceID string) (int, error) {
	var attempts int
	err := r.db.QueryRowContext(ctx, `
		UPDATE device_pins
		SET failed_attempts = failed_attempts + 1, updated_at = CURRENT_TIMESTAMP
		WHERE device_id = $1
		RETURNING failed_attempts`, deviceID).Scan(&attempts)
	return attempts, err
}

func (r *postgresDevicePinRepository) Lock(ctx context.Context, deviceID string, until time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE device_pins
		SET locked_until = $2, updated_at = CURRENT_TIMESTAMP
		WHERE device_id = $1`, deviceID, until)
	return err
}

func (r *postgresDevicePinRepository) ResetFailedAttempts(ctx context.Context, deviceID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE device_pins
		SET failed_attempts = 0, locked_until = NULL, updated_at = CURRENT_TIMESTAMP
		WHERE device_id = $1`, deviceID)
	return err
}
