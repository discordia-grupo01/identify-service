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
	"github.com/lib/pq"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/outbox"
)

var ErrAccountRecoveryAlreadyPending = errors.New("the user already has a pending account recovery request")

// RecoveryResolution dice qué pasó al aplicar la respuesta del staff.
type RecoveryResolution int

const (
	// RecoveryResolved: la solicitud estaba pendiente y quedó resuelta.
	RecoveryResolved RecoveryResolution = iota
	// RecoveryAlreadyResolved: ya estaba resuelta (evento repetido).
	RecoveryAlreadyResolved
	// RecoveryNotFound: no hay una solicitud con ese id para ese usuario.
	RecoveryNotFound
)

type AccountRecoveryRepository interface {
	// Create guarda la solicitud pendiente y deja en el outbox
	// users.account_recovery_requested en la misma transacción. Devuelve
	// ErrAccountRecoveryAlreadyPending si el usuario ya tiene una.
	Create(ctx context.Context, request *model.AccountRecoveryRequest) error
	// Resolve marca la solicitud como aprobada o rechazada, solo si sigue
	// pendiente. Si se aprueba, en la misma transacción se borra el 2FA del
	// usuario: secreto, códigos de recuperación y desafíos abiertos.
	Resolve(ctx context.Context, requestID, userID string, status model.AccountRecoveryStatus, at time.Time) (RecoveryResolution, error)
}

type postgresAccountRecoveryRepository struct {
	db *sql.DB
}

func NewAccountRecoveryRepository(db *sql.DB) AccountRecoveryRepository {
	return &postgresAccountRecoveryRepository{db: db}
}

func (r *postgresAccountRecoveryRepository) Create(ctx context.Context, request *model.AccountRecoveryRequest) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	err = tx.QueryRowContext(ctx, `
		INSERT INTO account_recovery_requests (id, user_id, message, status, created_at)
		VALUES ($1, $2, $3, 'pending', $4)
		RETURNING created_at`,
		request.ID, request.UserID, request.Message, request.CreatedAt,
	).Scan(&request.CreatedAt)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return ErrAccountRecoveryAlreadyPending
		}
		return err
	}
	request.Status = model.AccountRecoveryPending

	if err := outbox.Insert(ctx, tx, users.RoutingKeyAccountRecoveryRequested, AccountRecoveryRequestedEvent(request)); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *postgresAccountRecoveryRepository) Resolve(ctx context.Context, requestID, userID string, status model.AccountRecoveryStatus, at time.Time) (RecoveryResolution, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
		UPDATE account_recovery_requests
		SET status = $3, resolved_at = $4
		WHERE id = $1 AND user_id = $2 AND status = 'pending'`,
		requestID, userID, string(status), at)
	if err != nil {
		return 0, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if updated == 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (SELECT 1 FROM account_recovery_requests WHERE id = $1 AND user_id = $2)`,
			requestID, userID).Scan(&exists); err != nil {
			return 0, err
		}
		if exists {
			return RecoveryAlreadyResolved, nil
		}
		return RecoveryNotFound, nil
	}

	if status == model.AccountRecoveryApproved {
		for _, statement := range []string{
			`DELETE FROM two_factor_challenges WHERE user_id = $1`,
			`DELETE FROM two_factor_recovery_codes WHERE user_id = $1`,
			`DELETE FROM user_two_factor WHERE user_id = $1`,
		} {
			if _, err := tx.ExecContext(ctx, statement, userID); err != nil {
				return 0, err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return RecoveryResolved, nil
}

// AccountRecoveryRequestedEvent arma el aviso para admin. El email no viaja:
// admin ya lo tiene por users.account_registered.
func AccountRecoveryRequestedEvent(request *model.AccountRecoveryRequest) users.AccountRecoveryRequested {
	return users.AccountRecoveryRequested{
		Meta:        envelope.Meta{EventID: uuid.NewString(), OccurredAt: time.Now().UTC()},
		RequestID:   request.ID,
		UserID:      request.UserID,
		Message:     request.Message,
		RequestedAt: request.CreatedAt.UTC(),
	}
}
