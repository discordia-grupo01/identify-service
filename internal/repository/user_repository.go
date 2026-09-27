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
	UpdateEmailVerification(ctx context.Context, userID string, verified bool) error
	UpdateProfile(ctx context.Context, userID string, name string, description string, avatarURL string) error
	// UpdateStatus aplica un merge atómico: un puntero nil deja esa columna
	// sin tocar (COALESCE la conserva), un puntero no nil la fija a ese valor
	// (incluida cadena vacía, para poder limpiarla). Evita el race condition
	// de un read-modify-write hecho en aplicación. Devuelve el usuario ya
	// actualizado, o nil si userID no existe.
	UpdateStatus(ctx context.Context, userID string, statusText *string, statusEmoji *string) (*model.User, error)
	// UpdatePasswordHash setea la contraseña de una cuenta que nunca tuvo una
	// (alta puramente federada por Google): a diferencia del reset de
	// contraseña, no depende de un token por mail.
	UpdatePasswordHash(ctx context.Context, userID string, passwordHash string) error
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
	query := `SELECT id, name, email, description, avatar_url, status_text, status_emoji, email_verified, verified_at, password_hash, created_at, updated_at FROM users WHERE email = $1`
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
		SELECT id, name, email, description, avatar_url, status_text, status_emoji, email_verified, verified_at, password_hash, created_at, updated_at
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
		INSERT INTO users (id, name, email, password_hash, email_verified, verified_at)
		VALUES ($1, $2, $3, $4, $5, CASE WHEN $5 THEN CURRENT_TIMESTAMP ELSE NULL END)
		RETURNING created_at, updated_at`

	err := r.db.QueryRowContext(ctx, query, user.ID, user.Name, user.Email, user.PasswordHash, user.EmailVerified).
		Scan(&user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return err
	}
	return nil
}

func (r *postgresUserRepository) UpdateEmailVerification(ctx context.Context, userID string, verified bool) error {
	if verified {
		_, err := r.db.ExecContext(ctx, `
			UPDATE users
			SET email_verified = TRUE, verified_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
			WHERE id = $1`, userID)
		return err
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE users
		SET email_verified = FALSE, verified_at = NULL, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1`, userID)
	return err
}

func (r *postgresUserRepository) UpdateProfile(ctx context.Context, userID string, name string, description string, avatarURL string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE users
		SET name = $1, description = $2, avatar_url = $3, updated_at = CURRENT_TIMESTAMP
		WHERE id = $4`, name, description, avatarURL, userID)
	return err
}

func (r *postgresUserRepository) UpdateStatus(ctx context.Context, userID string, statusText *string, statusEmoji *string) (*model.User, error) {
	row := r.db.QueryRowContext(ctx, `
		UPDATE users
		SET status_text = COALESCE($1, status_text),
		    status_emoji = COALESCE($2, status_emoji),
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $3
		RETURNING id, name, email, description, avatar_url, status_text, status_emoji, email_verified, verified_at, password_hash, created_at, updated_at`,
		statusText, statusEmoji, userID)

	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
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
		&user.StatusText,
		&user.StatusEmoji,
		&user.EmailVerified,
		&user.VerifiedAt,
		&user.PasswordHash,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	return &user, err
}
