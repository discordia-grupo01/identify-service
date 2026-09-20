package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
)

var ErrPasswordRecoveryNotFound = errors.New("password recovery token not found or expired")

type PasswordRecoveryRepository interface {
	Create(ctx context.Context, recovery *model.PasswordRecovery) error
	CountRecentByEmail(ctx context.Context, email string, since time.Time) (int, error)
	ResetPassword(ctx context.Context, tokenHash string, passwordHash string) error
}

type postgresPasswordRecoveryRepository struct {
	db *sql.DB
}

func NewPostgresPasswordRecoveryRepository(db *sql.DB) *postgresPasswordRecoveryRepository {
	return &postgresPasswordRecoveryRepository{
		db: db,
	}
}

func (r *postgresPasswordRecoveryRepository) Create(ctx context.Context, recovery *model.PasswordRecovery) error {
	return r.db.QueryRowContext(ctx, `
		INSERT INTO password_recoveries (user_id, email, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at`, recovery.UserID, recovery.Email, recovery.TokenHash, recovery.ExpiresAt).
		Scan(&recovery.ID, &recovery.CreatedAt)
}

func (r *postgresPasswordRecoveryRepository) CountRecentByEmail(ctx context.Context, email string, since time.Time) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM password_recoveries
		WHERE email = $1 AND created_at >= $2`, email, since).Scan(&count)
	return count, err
}

func (r *postgresPasswordRecoveryRepository) ResetPassword(ctx context.Context, tokenHash string, passwordHash string) error {
	result, err := r.db.ExecContext(ctx, `
		WITH consumed AS (
			UPDATE password_recoveries
			SET used_at = CURRENT_TIMESTAMP
			WHERE token_hash = $1
			  AND used_at IS NULL
			  AND expires_at > CURRENT_TIMESTAMP
			RETURNING user_id
		)
		UPDATE users
		SET password_hash = $2, updated_at = CURRENT_TIMESTAMP
		WHERE id = (SELECT user_id FROM consumed)`, tokenHash, passwordHash)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrPasswordRecoveryNotFound
	}
	return nil
}
