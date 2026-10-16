package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/discordia-grupo01/events-contract/envelope"
	"github.com/discordia-grupo01/events-contract/users"
	"github.com/google/uuid"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/outbox"
)

// userColumns es la lista de columnas que lee scanUser, en ese orden.
const userColumns = `id, name, email, description, avatar_url, status_text, status_emoji, email_verified, verified_at, password_hash, profile_version, created_at, updated_at`

// Las escrituras del perfil público (Create, UpdateProfile, UpdateStatus)
// suben profile_version (Create la deja en 1) y, en la misma transacción,
// dejan un users.profile_updated en outbox_events con la foto completa leída
// de la fila escrita (RETURNING). Si falla el insert en la outbox, falla la
// escritura entera: nunca queda un perfil guardado sin su evento.
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
	query := `SELECT ` + userColumns + ` FROM users WHERE email = $1`
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
	row := r.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, userID)

	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
}

// Create inserta el usuario (incluido avatar_url, que trae el alta por
// Google) con profile_version = 1 y deja, en la misma transacción, su primer
// users.profile_updated y su users.account_registered (este último lleva el
// email y solo lo consume el backoffice de admin).
func (r *postgresUserRepository) Create(ctx context.Context, user *model.User) error {
	return r.withProfileEvent(ctx, func(tx *sql.Tx) (*model.User, error) {
		row := tx.QueryRowContext(ctx, `
			INSERT INTO users (id, name, email, avatar_url, password_hash, email_verified, verified_at)
			VALUES ($1, $2, $3, $4, $5, $6, CASE WHEN $6 THEN CURRENT_TIMESTAMP ELSE NULL END)
			RETURNING `+userColumns,
			user.ID, user.Name, user.Email, user.AvatarURL, user.PasswordHash, user.EmailVerified)
		created, err := scanUser(row)
		if err != nil {
			return nil, err
		}
		user.CreatedAt, user.UpdatedAt, user.ProfileVersion = created.CreatedAt, created.UpdatedAt, created.ProfileVersion
		if err := outbox.Insert(ctx, tx, users.RoutingKeyAccountRegistered, AccountRegisteredEvent(created)); err != nil {
			return nil, err
		}
		return created, nil
	})
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

// UpdateProfile no falla si userID no existe (no escribe nada ni publica).
func (r *postgresUserRepository) UpdateProfile(ctx context.Context, userID string, name string, description string, avatarURL string) error {
	return r.withProfileEvent(ctx, func(tx *sql.Tx) (*model.User, error) {
		row := tx.QueryRowContext(ctx, `
			UPDATE users
			SET name = $1, description = $2, avatar_url = $3,
			    profile_version = profile_version + 1, updated_at = CURRENT_TIMESTAMP
			WHERE id = $4
			RETURNING `+userColumns, name, description, avatarURL, userID)
		return scanUpdatedUser(row)
	})
}

func (r *postgresUserRepository) UpdateStatus(ctx context.Context, userID string, statusText *string, statusEmoji *string) (*model.User, error) {
	var updated *model.User
	err := r.withProfileEvent(ctx, func(tx *sql.Tx) (*model.User, error) {
		row := tx.QueryRowContext(ctx, `
			UPDATE users
			SET status_text = COALESCE($1, status_text),
			    status_emoji = COALESCE($2, status_emoji),
			    profile_version = profile_version + 1,
			    updated_at = CURRENT_TIMESTAMP
			WHERE id = $3
			RETURNING `+userColumns,
			statusText, statusEmoji, userID)
		u, err := scanUpdatedUser(row)
		updated = u
		return u, err
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// withProfileEvent corre write en una transacción y, si escribió un usuario,
// deja en la outbox su users.profile_updated antes de confirmar. write
// devuelve nil (sin error) cuando no había fila que escribir: entonces no se
// publica nada. El UPDATE toma el lock de la fila, así que dos cambios
// concurrentes del mismo usuario obtienen versiones distintas y crecientes en
// el orden en que confirman.
func (r *postgresUserRepository) withProfileEvent(ctx context.Context, write func(tx *sql.Tx) (*model.User, error)) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	user, err := write(tx)
	if err != nil {
		return err
	}
	if user != nil {
		if err := outbox.Insert(ctx, tx, users.RoutingKeyProfileUpdated, ProfileUpdatedEvent(user)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ProfileUpdatedEvent arma la foto del perfil público de user. Nunca incluye
// el email ni otros datos privados.
func ProfileUpdatedEvent(user *model.User) users.ProfileUpdated {
	return users.ProfileUpdated{
		Meta:        envelope.Meta{EventID: uuid.NewString(), OccurredAt: time.Now().UTC()},
		UserID:      user.ID,
		Version:     user.ProfileVersion,
		Name:        user.Name,
		AvatarURL:   user.AvatarURL,
		Description: user.Description,
		StatusText:  user.StatusText,
		StatusEmoji: user.StatusEmoji,
	}
}

// AccountRegisteredEvent arma el aviso de alta de cuenta de user. A diferencia
// de ProfileUpdatedEvent lleva el email: es un dato privado, que solo debe
// recibir la cola de admin.
func AccountRegisteredEvent(user *model.User) users.AccountRegistered {
	return users.AccountRegistered{
		Meta:      envelope.Meta{EventID: uuid.NewString(), OccurredAt: time.Now().UTC()},
		UserID:    user.ID,
		Email:     user.Email,
		Name:      user.Name,
		CreatedAt: user.CreatedAt.UTC(),
	}
}

// scanUpdatedUser es scanUser para un UPDATE ... RETURNING: sin fila (userID
// inexistente) devuelve nil, nil.
func scanUpdatedUser(row rowScanner) (*model.User, error) {
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
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
		&user.ProfileVersion,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	return &user, err
}
