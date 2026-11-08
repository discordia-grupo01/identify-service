package audit

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"

	"github.com/discordia-grupo01/identify-service/internal/logging"
)

type Status string

const (
	StatusProcessed           Status = "processed"
	StatusPending             Status = "pending"
	StatusDiscardedMalformed  Status = "discarded_malformed_payload"
	StatusDiscardedUnknownKey Status = "discarded_unknown_routing_key"
	StatusDiscardedInvalid    Status = "discarded_invalid_payload"
	// Un admin.* sin firma, con firma inválida o con la de otra routing key:
	// no cambió ningún estado.
	StatusDiscardedInvalidSignature Status = "discarded_invalid_signature"
	// Un admin.* válido para un user_id que no existe en identify.
	StatusDiscardedUnknownUser Status = "discarded_unknown_user"
)

type Entry struct {
	EventID    string
	RoutingKey string
	RawBody    []byte
	Status     Status
	Err        error
}

type Recorder struct {
	db *sql.DB
}

func NewRecorder(db *sql.DB) *Recorder {
	return &Recorder{db: db}
}

func (r *Recorder) Record(ctx context.Context, e Entry) {
	if r == nil {
		return
	}
	var errText *string
	if e.Err != nil {
		s := e.Err.Error()
		errText = &s
	}

	if e.EventID == "" {
		if _, err := r.db.ExecContext(ctx, `
			INSERT INTO consumed_events (id, event_id, routing_key, raw_body, status, processed_at, error)
			VALUES ($1, NULL, $2, $3, $4, now(), $5)`,
			uuid.NewString(), e.RoutingKey, string(e.RawBody), string(e.Status), errText,
		); err != nil {
			logging.Errorf("audit: could not record %q delivery: %v", e.RoutingKey, err)
		}
		return
	}

	var processedAt *time.Time
	if e.Status != StatusPending {
		now := time.Now().UTC()
		processedAt = &now
	}

	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO consumed_events (id, event_id, routing_key, raw_body, status, processed_at, error)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (event_id) WHERE event_id IS NOT NULL DO UPDATE SET
			status       = EXCLUDED.status,
			attempts     = consumed_events.attempts + 1,
			processed_at = EXCLUDED.processed_at,
			error        = EXCLUDED.error`,
		uuid.NewString(), e.EventID, e.RoutingKey, string(e.RawBody), string(e.Status), processedAt, errText,
	); err != nil {
		logging.Errorf("audit: could not record event_id=%s: %v", e.EventID, err)
	}
}
