package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/discordia-grupo01/identify-service/internal/model"
	"time"
)

var ErrRefreshTokenNotFound = errors.New("refresh token not found")

type RefreshTokenRepository interface {
	GetByHash(ctx context.Context, hash string) (*model.RefreshToken, error)
	RevokeByHash(ctx context.Context, hash string) error
	RevokeFamily(ctx context.Context, familyID string) error
	RevokeAllForUser(ctx context.Context, userID string) error
	Create(ctx context.Context, refreshHash string, userID string, familyID string, expiresAt time.Time) error
}

type postgresRefreshTokenRepository struct {
	db *sql.DB
}

func NewRefreshTokenRepository(db *sql.DB) RefreshTokenRepository {
	return &postgresRefreshTokenRepository{db: db}
}

func (r *postgresRefreshTokenRepository) GetByHash(ctx context.Context, tokenHash string) (*model.RefreshToken, error) {
	var token model.RefreshToken
	err := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, family_id, token_hash, expires_at, revoked_at
		FROM refresh_tokens
		WHERE token_hash = $1
	`, tokenHash).Scan(&token.ID, &token.UserID, &token.FamilyID, &token.TokenHash, &token.ExpiresAt, &token.RevokedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRefreshTokenNotFound
		}
		return nil, err
	}
	return &token, nil
}

func (r *postgresRefreshTokenRepository) RevokeByHash(ctx context.Context, tokenHash string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE refresh_tokens
		SET revoked_at = CURRENT_TIMESTAMP
		WHERE token_hash = $1 AND revoked_at IS NULL`, tokenHash)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrRefreshTokenNotFound
	}
	return nil
}

func (r *postgresRefreshTokenRepository) RevokeFamily(ctx context.Context, familyID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE refresh_tokens
		SET revoked_at = CURRENT_TIMESTAMP
		WHERE family_id = $1 AND revoked_at IS NULL
	`, familyID)
	return err
}

func (r *postgresRefreshTokenRepository) RevokeAllForUser(ctx context.Context, userID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE refresh_tokens
		SET revoked_at = CURRENT_TIMESTAMP
		WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	return err
}

func (r *postgresRefreshTokenRepository) Create(ctx context.Context, refreshHash string, userID string, familyID string, expiresAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO refresh_tokens (user_id, family_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)
	`, userID, familyID, refreshHash, expiresAt)
	return err
}
