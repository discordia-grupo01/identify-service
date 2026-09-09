package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrSessionNotFound = errors.New("session not found")

type SessionRepository interface {
	Create(ctx context.Context, tokenID string, userID int64, expiresAt time.Time) error
	IsActive(ctx context.Context, tokenID string) (bool, error)
	Revoke(ctx context.Context, tokenID string) error
}

type postgresSessionRepository struct {
	db *sql.DB
}

func NewSessionRepository(db *sql.DB) SessionRepository {
	return &postgresSessionRepository{db: db}
}

func (r *postgresSessionRepository) Create(ctx context.Context, tokenID string, userID int64, expiresAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO sessions (token_id, user_id, expires_at)
		VALUES ($1, $2, $3)`, tokenID, userID, expiresAt)
	return err
}

func (r *postgresSessionRepository) IsActive(ctx context.Context, tokenID string) (bool, error) {
	var active bool
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM sessions
			WHERE token_id = $1 AND revoked_at IS NULL AND expires_at > CURRENT_TIMESTAMP
		)`, tokenID).Scan(&active)
	return active, err
}

func (r *postgresSessionRepository) Revoke(ctx context.Context, tokenID string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE sessions
		SET revoked_at = CURRENT_TIMESTAMP
		WHERE token_id = $1 AND revoked_at IS NULL`, tokenID)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrSessionNotFound
	}
	return nil
}
