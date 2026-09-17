package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/discordia-grupo01/identify-service/internal/model"
)

type OAuthIdentityRepository interface {
	GetByProvider(ctx context.Context, provider string, providerSubject string) (*model.OAuthIdentity, error)
	Create(ctx context.Context, identity *model.OAuthIdentity) error
}

type postgresOAuthIdentityRepository struct {
	db *sql.DB
}

func NewOAuthIdentityRepository(db *sql.DB) OAuthIdentityRepository {
	return &postgresOAuthIdentityRepository{db: db}
}

func (r *postgresOAuthIdentityRepository) GetByProvider(ctx context.Context, provider string, providerSubject string) (*model.OAuthIdentity, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, provider, provider_subject, email, created_at
		FROM oauth_identities WHERE provider = $1 AND provider_subject = $2`, provider, providerSubject)

	var identity model.OAuthIdentity
	err := row.Scan(
		&identity.ID,
		&identity.UserID,
		&identity.Provider,
		&identity.ProviderSubject,
		&identity.Email,
		&identity.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &identity, nil
}

func (r *postgresOAuthIdentityRepository) Create(ctx context.Context, identity *model.OAuthIdentity) error {
	query := `
		INSERT INTO oauth_identities (id, user_id, provider, provider_subject, email)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at`

	return r.db.QueryRowContext(ctx, query,
		identity.ID, identity.UserID, identity.Provider, identity.ProviderSubject, identity.Email,
	).Scan(&identity.CreatedAt)
}
