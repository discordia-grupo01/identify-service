package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/discordia-grupo01/events-contract/validate"
	"github.com/discordia-grupo01/identify-service/internal/logging"
	"github.com/discordia-grupo01/identify-service/internal/telemetry"
)

// Publisher publica un evento y devuelve error si no llegó al broker, para
// que el Poller lo deje pendiente y lo reintente.
type Publisher interface {
	Publish(ctx context.Context, routingKey string, payload any) error
}

const PollInterval = 2 * time.Second

// maxPerTick acota cuántos eventos publica una vuelta. Si hay atrasados, el
// poller sigue publicando en vez de esperar al próximo tick: con un tope de 20
// por tick de 2s, todo el servicio quedaba limitado a 10 eventos por segundo.
const maxPerTick = 2000

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

// Drain publica hasta maxPerTick eventos pendientes, del más viejo al más
// nuevo. Frena en el primer error de base; un error del broker solo deja ese
// evento pendiente (con attempts y last_error) y sigue con el próximo tick.
func (p *Poller) Drain(ctx context.Context) {
	for i := 0; i < maxPerTick && ctx.Err() == nil; i++ {
		published, err := p.publishOne(ctx)
		if err != nil {
			logging.Errorf("outbox: fallo el tick del poller: %v", err)
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
	var traceparent sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT id, routing_key, payload, traceparent
		  FROM outbox_events
		 WHERE published_at IS NULL
		 ORDER BY created_at
		 LIMIT 1
		   FOR UPDATE SKIP LOCKED`).Scan(&id, &routingKey, &payload, &traceparent)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("select pending event: %w", err)
	}

	if err := validate.Validate(routingKey, payload); err != nil && !errors.Is(err, validate.ErrUnknownEvent) {
		logging.Warnf("outbox: %q (id=%s) no cumple el contrato de eventos: %v", routingKey, id, err)
	}

	publishCtx := telemetry.ContextWithTraceparent(ctx, traceparent.String)
	publishCtx, span := otel.Tracer("github.com/discordia-grupo01/identify-service").Start(publishCtx, "outbox publish "+routingKey,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "rabbitmq"),
			attribute.String("messaging.destination.name", routingKey),
		),
	)
	pubErr := p.publisher.Publish(publishCtx, routingKey, json.RawMessage(payload))
	if pubErr != nil {
		span.SetStatus(codes.Error, "publish failed")
	}
	span.End()

	if pubErr != nil {
		logging.Warnf("outbox: no se pudo publicar %q (id=%s), se reintenta en el próximo tick: %v", routingKey, id, pubErr)
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
