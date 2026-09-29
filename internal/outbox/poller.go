package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"
)

// Publisher publica un evento y devuelve error si no llegó al broker, para
// que el Poller lo deje pendiente y lo reintente.
type Publisher interface {
	Publish(ctx context.Context, routingKey string, payload any) error
}

const PollInterval = 2 * time.Second

const batchSize = 20

type Poller struct {
	db        *sql.DB
	publisher Publisher
}

func NewPoller(db *sql.DB, publisher Publisher) *Poller {
	return &Poller{db: db, publisher: publisher}
}

// Start publica los eventos pendientes cada PollInterval hasta que se cancele
// ctx.
func (p *Poller) Start(ctx context.Context) {
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.Drain(ctx)
		}
	}
}

// Drain publica hasta batchSize eventos pendientes, del más viejo al más
// nuevo. Frena en el primer error de base; un error del broker solo deja ese
// evento pendiente (con attempts y last_error) y sigue con el próximo tick.
func (p *Poller) Drain(ctx context.Context) {
	for i := 0; i < batchSize; i++ {
		published, err := p.publishOne(ctx)
		if err != nil {
			log.Printf("outbox: fallo el tick del poller: %v", err)
			return
		}
		if !published {
			return
		}
	}
}

// publishOne toma el evento pendiente más viejo con FOR UPDATE SKIP LOCKED
// (varias instancias no publican el mismo evento a la vez), lo publica y lo
// marca publicado en la misma transacción. Devuelve false si no había
// pendientes o si el broker falló (así Drain no reintenta en loop el mismo
// evento dentro del tick).
func (p *Poller) publishOne(ctx context.Context) (published bool, err error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var id, routingKey string
	var payload []byte
	err = tx.QueryRowContext(ctx, `
		SELECT id, routing_key, payload
		  FROM outbox_events
		 WHERE published_at IS NULL
		 ORDER BY created_at
		 LIMIT 1
		   FOR UPDATE SKIP LOCKED`).Scan(&id, &routingKey, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("select pending event: %w", err)
	}

	if pubErr := p.publisher.Publish(ctx, routingKey, json.RawMessage(payload)); pubErr != nil {
		log.Printf("outbox: no se pudo publicar %q (id=%s), se reintenta en el próximo tick: %v", routingKey, id, pubErr)
		if _, err := tx.ExecContext(ctx, `
			UPDATE outbox_events SET attempts = attempts + 1, last_error = $2 WHERE id = $1`,
			id, pubErr.Error(),
		); err != nil {
			return false, fmt.Errorf("record failed attempt: %w", err)
		}
		return false, tx.Commit()
	}

	if _, err := tx.ExecContext(ctx, `UPDATE outbox_events SET published_at = now() WHERE id = $1`, id); err != nil {
		return false, fmt.Errorf("mark published: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return true, nil
}
