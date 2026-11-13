package events

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/discordia-grupo01/events-contract/admin"
	"github.com/discordia-grupo01/events-contract/broker"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/discordia-grupo01/identify-service/internal/audit"
	"github.com/discordia-grupo01/identify-service/internal/events/deadletter"
	"github.com/discordia-grupo01/identify-service/internal/logging"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/telemetry"
)

// AdminQueueName es la cola durable de este consumidor de admin.*; ningún otro
// servicio la bindea.
const AdminQueueName = "identify.admin_events"

// AdminConnectionName se muestra en la UI/API de management de RabbitMQ, para
// identificar la conexión del consumidor de admin (ej. en tests que la cierran
// a propósito).
const AdminConnectionName = "discordia-identify-admin-events"

// SuspensionApplier es lo que el consumidor necesita del repositorio de
// usuarios: aplicar un evento solo si su versión es mayor a la guardada.
type SuspensionApplier interface {
	ApplySuspension(ctx context.Context, userID string, suspended bool, suspendedAt time.Time, version int64) (repository.SuspensionResult, error)
}

// AccountRecoveryResolver es lo que el consumidor necesita para aplicar la
// respuesta del staff a una solicitud de recuperación de cuenta.
type AccountRecoveryResolver interface {
	Resolve(ctx context.Context, requestID, userID string, approved bool) (repository.RecoveryResolution, error)
}

// AdminConsumer escucha admin.user_suspended / admin.user_reactivated y, si se
// configuró con HandleAccountRecovery, admin.account_recovery_approved /
// admin.account_recovery_rejected. Estos eventos pueden dejar a cualquiera sin
// acceso o sacarle el 2FA a una cuenta, así que antes de tocar nada se
// verifica su firma HMAC con el secreto compartido con discordia-admin.
type AdminConsumer struct {
	url       string
	queueName string
	secret    string
	applier   SuspensionApplier
	recovery  AccountRecoveryResolver
	recorder  *audit.Recorder
	onApplied func(userID string)
}

func NewAdminConsumer(url string, secret string, applier SuspensionApplier, recorder *audit.Recorder) *AdminConsumer {
	return &AdminConsumer{url: url, queueName: AdminQueueName, secret: secret, applier: applier, recorder: recorder}
}

// OnApplied registra un callback que se invoca cada vez que un evento cambió el
// estado de un usuario (no con duplicados ni eventos viejos). Sirve para
// invalidar cachés de estado de esta instancia.
func (c *AdminConsumer) OnApplied(fn func(userID string)) {
	c.onApplied = fn
}

// HandleAccountRecovery suma las respuestas del staff a las solicitudes de
// recuperación de cuenta. Hay que llamarlo antes de Start.
func (c *AdminConsumer) HandleAccountRecovery(resolver AccountRecoveryResolver) {
	c.recovery = resolver
}

func (c *AdminConsumer) routingKeys() []string {
	keys := []string{admin.RoutingKeyUserSuspended, admin.RoutingKeyUserReactivated}
	if c.recovery != nil {
		keys = append(keys, admin.RoutingKeyAccountRecoveryApproved, admin.RoutingKeyAccountRecoveryRejected)
	}
	return keys
}

// SetQueueName cambia la cola que consume (por defecto AdminQueueName). Existe
// para que los tests contra un RabbitMQ compartido usen una cola propia.
func (c *AdminConsumer) SetQueueName(name string) {
	c.queueName = name
}

func (c *AdminConsumer) Start(ctx context.Context) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for ctx.Err() == nil {
		if err := c.consumeOnce(ctx); err != nil {
			logging.Warnf("events: conexión de admin a RabbitMQ interrumpida, reintentando en %s: %v", backoff, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		backoff = time.Second
	}
}

func (c *AdminConsumer) consumeOnce(ctx context.Context) error {
	properties := amqp.NewConnectionProperties()
	properties.SetClientConnectionName(AdminConnectionName)
	conn, err := amqp.DialConfig(c.url, amqp.Config{Properties: properties})
	if err != nil {
		return err
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	if err := ch.ExchangeDeclare(broker.ExchangeName, broker.ExchangeType, true, false, false, false, nil); err != nil {
		return err
	}
	queue, err := ch.QueueDeclare(c.queueName, true, false, false, false, nil)
	if err != nil {
		return err
	}
	for _, routingKey := range c.routingKeys() {
		if err := ch.QueueBind(queue.Name, routingKey, broker.ExchangeName, false, nil); err != nil {
			return err
		}
	}

	// Ack manual: solo se confirma después de procesar, para no perder un
	// evento ante una falla transitoria de la base.
	retrier, err := deadletter.New(conn, queue.Name, deadletter.DefaultPolicy())
	if err != nil {
		return err
	}
	defer retrier.Close()

	messages, err := ch.Consume(queue.Name, "", false, false, false, false, nil)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case delivery, ok := <-messages:
			if !ok {
				return errors.New("canal de consumo de admin cerrado")
			}
			spanCtx, end := telemetry.StartConsumer(ctx, queue.Name, delivery)
			requeue := c.Process(spanCtx, delivery.RoutingKey, delivery.Body)
			retrier.Settle(spanCtx, delivery, requeue)
			end(requeue)
		}
	}
}

// Process aplica un mensaje y devuelve true si hay que reencolarlo (única
// causa: falla transitoria de la base). Todo lo demás se confirma: firma
// inválida, mensaje roto, usuario inexistente, duplicado o evento viejo.
func (c *AdminConsumer) Process(ctx context.Context, routingKey string, body []byte) bool {
	if c.recovery != nil && (routingKey == admin.RoutingKeyAccountRecoveryApproved || routingKey == admin.RoutingKeyAccountRecoveryRejected) {
		return c.processAccountRecovery(ctx, routingKey, body)
	}
	if routingKey != admin.RoutingKeyUserSuspended && routingKey != admin.RoutingKeyUserReactivated {
		logging.Warnf("events: routing key de admin desconocida %q, descartando mensaje", routingKey)
		c.recorder.Record(ctx, audit.Entry{RoutingKey: routingKey, RawBody: body, Status: audit.StatusDiscardedUnknownKey})
		return false
	}

	// UserSuspended y UserReactivated comparten forma en el cable.
	var event admin.UserSuspended
	if err := json.Unmarshal(body, &event); err != nil {
		logging.Warnf("events: payload malformado en %q, descartando mensaje: %v", routingKey, err)
		c.recorder.Record(ctx, audit.Entry{RoutingKey: routingKey, RawBody: body, Status: audit.StatusDiscardedMalformed, Err: err})
		return false
	}

	suspend := routingKey == admin.RoutingKeyUserSuspended
	var validSignature bool
	if suspend {
		validSignature = event.Verify(c.secret)
	} else {
		validSignature = admin.UserReactivated(event).Verify(c.secret)
	}
	if !validSignature {
		logging.Warnf("events: WARN firma inválida en %q, event_id=%s, descartando mensaje sin cambiar nada", routingKey, event.EventID)
		// Sin EventID: el event_id de un mensaje sin autenticar no es confiable
		// y, como es único en consumed_events, uno falsificado pisaría la fila
		// de auditoría de un evento legítimo.
		c.recorder.Record(ctx, audit.Entry{RoutingKey: routingKey, RawBody: body, Status: audit.StatusDiscardedInvalidSignature, Err: errors.New("firma inválida o ausente")})
		return false
	}

	if _, err := uuid.Parse(event.UserID); err != nil {
		logging.Warnf("events: payload inválido en %q (user_id %q no es un UUID), event_id=%s, descartando mensaje", routingKey, event.UserID, event.EventID)
		c.recorder.Record(ctx, audit.Entry{EventID: event.EventID, RoutingKey: routingKey, RawBody: body, Status: audit.StatusDiscardedInvalid, Err: err})
		return false
	}

	result, err := c.applier.ApplySuspension(ctx, event.UserID, suspend, event.OccurredAt.UTC(), event.Version)
	if err != nil {
		logging.Errorf("events: fallo transitorio procesando %q, event_id=%s, reintentando: %v", routingKey, event.EventID, err)
		c.recorder.Record(ctx, audit.Entry{EventID: event.EventID, RoutingKey: routingKey, RawBody: body, Status: audit.StatusPending, Err: err})
		return true
	}

	switch result {
	case repository.SuspensionUserNotFound:
		logging.Warnf("events: %q event_id=%s para un user_id inexistente (%s), no se crea nada", routingKey, event.EventID, event.UserID)
		c.recorder.Record(ctx, audit.Entry{EventID: event.EventID, RoutingKey: routingKey, RawBody: body, Status: audit.StatusDiscardedUnknownUser})
	case repository.SuspensionStale:
		logging.Debugf("events: %q event_id=%s user_id=%s version=%d ya aplicada o más vieja, sin cambios", routingKey, event.EventID, event.UserID, event.Version)
		c.recorder.Record(ctx, audit.Entry{EventID: event.EventID, RoutingKey: routingKey, RawBody: body, Status: audit.StatusProcessed})
	default:
		logging.Infof("events: procesado %q event_id=%s user_id=%s version=%d", routingKey, event.EventID, event.UserID, event.Version)
		c.recorder.Record(ctx, audit.Entry{EventID: event.EventID, RoutingKey: routingKey, RawBody: body, Status: audit.StatusProcessed})
		if c.onApplied != nil {
			c.onApplied(event.UserID)
		}
	}
	return false
}

// processAccountRecovery aplica la aprobación o el rechazo de una solicitud de
// recuperación. Mismas reglas que las suspensiones: solo se reencola ante una
// falla transitoria de la base.
func (c *AdminConsumer) processAccountRecovery(ctx context.Context, routingKey string, body []byte) bool {
	// AccountRecoveryApproved y AccountRecoveryRejected comparten forma en el cable.
	var event admin.AccountRecoveryApproved
	if err := json.Unmarshal(body, &event); err != nil {
		logging.Warnf("events: payload malformado en %q, descartando mensaje: %v", routingKey, err)
		c.recorder.Record(ctx, audit.Entry{RoutingKey: routingKey, RawBody: body, Status: audit.StatusDiscardedMalformed, Err: err})
		return false
	}

	approved := routingKey == admin.RoutingKeyAccountRecoveryApproved
	var validSignature bool
	if approved {
		validSignature = event.Verify(c.secret)
	} else {
		validSignature = admin.AccountRecoveryRejected(event).Verify(c.secret)
	}
	if !validSignature {
		logging.Warnf("events: WARN firma inválida en %q, event_id=%s, descartando mensaje sin cambiar nada", routingKey, event.EventID)
		c.recorder.Record(ctx, audit.Entry{RoutingKey: routingKey, RawBody: body, Status: audit.StatusDiscardedInvalidSignature, Err: errors.New("firma inválida o ausente")})
		return false
	}

	for field, id := range map[string]string{"request_id": event.RequestID, "user_id": event.UserID} {
		if _, err := uuid.Parse(id); err != nil {
			logging.Warnf("events: payload inválido en %q (%s %q no es un UUID), event_id=%s, descartando mensaje", routingKey, field, id, event.EventID)
			c.recorder.Record(ctx, audit.Entry{EventID: event.EventID, RoutingKey: routingKey, RawBody: body, Status: audit.StatusDiscardedInvalid, Err: err})
			return false
		}
	}

	resolution, err := c.recovery.Resolve(ctx, event.RequestID, event.UserID, approved)
	if err != nil {
		logging.Errorf("events: fallo transitorio procesando %q, event_id=%s, reintentando: %v", routingKey, event.EventID, err)
		c.recorder.Record(ctx, audit.Entry{EventID: event.EventID, RoutingKey: routingKey, RawBody: body, Status: audit.StatusPending, Err: err})
		return true
	}

	switch resolution {
	case repository.RecoveryNotFound:
		logging.Warnf("events: %q event_id=%s para una solicitud inexistente (%s del usuario %s), no se cambia nada", routingKey, event.EventID, event.RequestID, event.UserID)
		c.recorder.Record(ctx, audit.Entry{EventID: event.EventID, RoutingKey: routingKey, RawBody: body, Status: audit.StatusDiscardedInvalid, Err: errors.New("solicitud de recuperación inexistente")})
	case repository.RecoveryAlreadyResolved:
		logging.Debugf("events: %q event_id=%s request_id=%s ya estaba resuelta, sin cambios", routingKey, event.EventID, event.RequestID)
		c.recorder.Record(ctx, audit.Entry{EventID: event.EventID, RoutingKey: routingKey, RawBody: body, Status: audit.StatusProcessed})
	default:
		logging.Infof("events: procesado %q event_id=%s request_id=%s user_id=%s", routingKey, event.EventID, event.RequestID, event.UserID)
		c.recorder.Record(ctx, audit.Entry{EventID: event.EventID, RoutingKey: routingKey, RawBody: body, Status: audit.StatusProcessed})
	}
	return false
}
