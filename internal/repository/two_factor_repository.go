package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/google/uuid"
)

var ErrTwoFactorChallengeNotFound = errors.New("two factor challenge not found or expired")

// TwoFactorRepository agrupa las tres tablas del segundo factor (secreto,
// codigos de recuperacion y desafios de login). Van juntas porque se crean y
// se borran como una sola unidad: activar 2FA escribe en dos, desactivarlo
// limpia las tres.
type TwoFactorRepository interface {
	GetByUserID(ctx context.Context, userID string) (*model.UserTwoFactor, error)
	UpsertSecret(ctx context.Context, userID string, encryptedSecret string) error
	Confirm(ctx context.Context, userID string, step int64) error
	MarkStepUsed(ctx context.Context, userID string, step int64) error
	Disable(ctx context.Context, userID string) error

	ReplaceRecoveryCodes(ctx context.Context, userID string, codeHashes []string) error
	ConsumeRecoveryCode(ctx context.Context, userID string, codeHash string) (bool, error)
	CountActiveRecoveryCodes(ctx context.Context, userID string) (int, error)

	CreateChallenge(ctx context.Context, challenge *model.TwoFactorChallenge) error
	GetChallengeByHash(ctx context.Context, tokenHash string) (*model.TwoFactorChallenge, error)
	IncrementChallengeAttempts(ctx context.Context, id string) (int, error)
	ConsumeChallenge(ctx context.Context, id string) error
}

type postgresTwoFactorRepository struct {
	db *sql.DB
}

func NewTwoFactorRepository(db *sql.DB) TwoFactorRepository {
	return &postgresTwoFactorRepository{db: db}
}

func (r *postgresTwoFactorRepository) GetByUserID(ctx context.Context, userID string) (*model.UserTwoFactor, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT user_id, secret, confirmed_at, last_used_step, created_at, updated_at
		FROM user_two_factor WHERE user_id = $1`, userID)

	var tf model.UserTwoFactor
	err := row.Scan(&tf.UserID, &tf.Secret, &tf.ConfirmedAt, &tf.LastUsedStep, &tf.CreatedAt, &tf.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &tf, nil
}

func (r *postgresTwoFactorRepository) UpsertSecret(ctx context.Context, userID string, encryptedSecret string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_two_factor (user_id, secret)
		VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE
		SET secret = EXCLUDED.secret,
		    confirmed_at = NULL,
		    last_used_step = 0,
		    updated_at = CURRENT_TIMESTAMP`, userID, encryptedSecret)
	return err
}

func (r *postgresTwoFactorRepository) Confirm(ctx context.Context, userID string, step int64) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE user_two_factor
		SET confirmed_at = CURRENT_TIMESTAMP, last_used_step = $2, updated_at = CURRENT_TIMESTAMP
		WHERE user_id = $1`, userID, step)
	return err
}

func (r *postgresTwoFactorRepository) MarkStepUsed(ctx context.Context, userID string, step int64) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE user_two_factor
		SET last_used_step = $2, updated_at = CURRENT_TIMESTAMP
		WHERE user_id = $1`, userID, step)
	return err
}

func (r *postgresTwoFactorRepository) Disable(ctx context.Context, userID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	statements := []string{
		`DELETE FROM two_factor_challenges WHERE user_id = $1`,
		`DELETE FROM two_factor_recovery_codes WHERE user_id = $1`,
		`DELETE FROM user_two_factor WHERE user_id = $1`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement, userID); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (r *postgresTwoFactorRepository) ReplaceRecoveryCodes(ctx context.Context, userID string, codeHashes []string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM two_factor_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return err
	}

	for _, hash := range codeHashes {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO two_factor_recovery_codes (id, user_id, code_hash)
			VALUES ($1, $2, $3)`, uuid.NewString(), userID, hash); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (r *postgresTwoFactorRepository) ConsumeRecoveryCode(ctx context.Context, userID string, codeHash string) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE two_factor_recovery_codes
		SET used_at = CURRENT_TIMESTAMP
		WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL`, userID, codeHash)
	if err != nil {
		return false, err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func (r *postgresTwoFactorRepository) CountActiveRecoveryCodes(ctx context.Context, userID string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM two_factor_recovery_codes
		WHERE user_id = $1 AND used_at IS NULL`, userID).Scan(&count)
	return count, err
}

func (r *postgresTwoFactorRepository) CreateChallenge(ctx context.Context, challenge *model.TwoFactorChallenge) error {
	challenge.ID = uuid.NewString()
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO two_factor_challenges (id, user_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)`, challenge.ID, challenge.UserID, challenge.TokenHash, challenge.ExpiresAt)
	return err
}

func (r *postgresTwoFactorRepository) GetChallengeByHash(ctx context.Context, tokenHash string) (*model.TwoFactorChallenge, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, token_hash, attempts, expires_at, used_at
		FROM two_factor_challenges WHERE token_hash = $1`, tokenHash)

	var challenge model.TwoFactorChallenge
	err := row.Scan(&challenge.ID, &challenge.UserID, &challenge.TokenHash,
		&challenge.Attempts, &challenge.ExpiresAt, &challenge.UsedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTwoFactorChallengeNotFound
		}
		return nil, err
	}
	return &challenge, nil
}

func (r *postgresTwoFactorRepository) IncrementChallengeAttempts(ctx context.Context, id string) (int, error) {
	var attempts int
	err := r.db.QueryRowContext(ctx, `
		UPDATE two_factor_challenges
		SET attempts = attempts + 1
		WHERE id = $1
		RETURNING attempts`, id).Scan(&attempts)
	return attempts, err
}

func (r *postgresTwoFactorRepository) ConsumeChallenge(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE two_factor_challenges
		SET used_at = CURRENT_TIMESTAMP
		WHERE id = $1`, id)
	return err
}
