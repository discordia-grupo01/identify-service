package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/discordia-grupo01/identify-service/internal/model"
)

type UserRepository interface {
	Create(ctx context.Context, user *model.User) error
	GetByEmail(ctx context.Context, email string) (*model.User, error)
	GetByID(ctx context.Context, userID string) (*model.User, error)
	UpdateProfile(ctx context.Context, userID string, name string, description string, avatarURL string) error
}

func (r *postgresUserRepository) UpdatePasswordHash(ctx context.Context, userID string, passwordHash string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE users
		SET password_hash = $1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $2`, passwordHash, userID)
	return err
}

type postgresUserRepository struct {
	db *sql.DB
}

func NewUserRepository(db *sql.DB) UserRepository {
	return &postgresUserRepository{db: db}
}

func (r *postgresUserRepository) GetByEmail(ctx context.Context, email string) (*model.User, error) {
	query := `SELECT id, name, email, description, avatar_url, password_hash, created_at, updated_at FROM users WHERE email = $1`
	row := r.db.QueryRowContext(ctx, query, email)

	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
}

func (r *postgresUserRepository) GetByID(ctx context.Context, userID string) (*model.User, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, email, description, avatar_url, password_hash, created_at, updated_at
		FROM users WHERE id = $1`, userID)

	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
}

func (r *postgresUserRepository) Create(ctx context.Context, user *model.User) error {
	query := `
		INSERT INTO users (id, name, email, password_hash)
		VALUES ($1, $2, $3, $4)
		RETURNING created_at, updated_at`

	err := r.db.QueryRowContext(ctx, query, user.ID, user.Name, user.Email, user.PasswordHash).
		Scan(&user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return err
	}
	return nil
}

func (r *postgresUserRepository) UpdateProfile(ctx context.Context, userID string, name string, description string, avatarURL string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE users
		SET name = $1, description = $2, avatar_url = $3, updated_at = CURRENT_TIMESTAMP
		WHERE id = $4`, name, description, avatarURL, userID)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(row rowScanner) (*model.User, error) {
	var user model.User
	err := row.Scan(
		&user.ID,
		&user.Name,
		&user.Email,
		&user.Description,
		&user.AvatarURL,
		&user.PasswordHash,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	return &user, err
}
