package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
)

var ErrEmailConfirmationNotFound = errors.New("email confirmation token not found or expired")

type EmailConfirmationRepository interface {
	Create(ctx context.Context, confirmation *model.EmailConfirmation) error
	GetByHash(ctx context.Context, tokenHash string) (*model.EmailConfirmation, error)
	MarkUsed(ctx context.Context, tokenHash string) error
	InvalidatePrevious(ctx context.Context, userID string, newTokenHash string) error
	CountRecentByEmail(ctx context.Context, email string, since time.Time) (int, error)
}

type postgresEmailConfirmationRepository struct {
	db *sql.DB
}

func NewPostgresEmailConfirmationRepository(db *sql.DB) *postgresEmailConfirmationRepository {
	return &postgresEmailConfirmationRepository{db: db}
}

func (r *postgresEmailConfirmationRepository) Create(ctx context.Context, confirmation *model.EmailConfirmation) error {
	return r.db.QueryRowContext(ctx, `
		INSERT INTO email_confirmations (user_id, email, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at`, confirmation.UserID, confirmation.Email, confirmation.TokenHash, confirmation.ExpiresAt).
		Scan(&confirmation.ID, &confirmation.CreatedAt)
}

func (r *postgresEmailConfirmationRepository) GetByHash(ctx context.Context, tokenHash string) (*model.EmailConfirmation, error) {
	var confirmation model.EmailConfirmation
	err := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, email, token_hash, expires_at, used_at, created_at
		FROM email_confirmations
		WHERE token_hash = $1`, tokenHash).Scan(
		&confirmation.ID,
		&confirmation.UserID,
		&confirmation.Email,
		&confirmation.TokenHash,
		&confirmation.ExpiresAt,
		&confirmation.UsedAt,
		&confirmation.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrEmailConfirmationNotFound
		}
		return nil, err
	}
	return &confirmation, nil
}

func (r *postgresEmailConfirmationRepository) MarkUsed(ctx context.Context, tokenHash string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE email_confirmations
		SET used_at = CURRENT_TIMESTAMP
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > CURRENT_TIMESTAMP`, tokenHash)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrEmailConfirmationNotFound
	}
	return nil
}

func (r *postgresEmailConfirmationRepository) InvalidatePrevious(ctx context.Context, userID string, newTokenHash string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE email_confirmations
		SET used_at = CURRENT_TIMESTAMP
		WHERE user_id = $1 AND token_hash <> $2 AND used_at IS NULL`, userID, newTokenHash)
	return err
}

func (r *postgresEmailConfirmationRepository) CountRecentByEmail(ctx context.Context, email string, since time.Time) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM email_confirmations
		WHERE email = $1 AND created_at >= $2`, email, since).Scan(&count)
	return count, err
}
