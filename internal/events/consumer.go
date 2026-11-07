package events

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/discordia-grupo01/events-contract/broker"
	"github.com/discordia-grupo01/events-contract/membership"
	"github.com/discordia-grupo01/identify-service/internal/audit"
	"github.com/discordia-grupo01/identify-service/internal/events/deadletter"
	"github.com/discordia-grupo01/identify-service/internal/logging"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

// QueueName is specific to this consumer, unlike the exchange/routing keys
// (shared with the publisher via events-contract) -- no other service binds
// this queue.
const QueueName = "identify.server_memberships"

type MembershipHandler interface {
	HandleMemberJoined(ctx context.Context, payload membership.MemberJoined) error
	HandleMemberLeft(ctx context.Context, payload membership.MemberLeft) error
}

type Consumer struct {
	url      string
	handler  MembershipHandler
	recorder *audit.Recorder
}

func NewConsumer(url string, handler MembershipHandler, recorder *audit.Recorder) *Consumer {
	return &Consumer{url: url, handler: handler, recorder: recorder}
}

func (c *Consumer) Start(ctx context.Context) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for ctx.Err() == nil {
		if err := c.consumeOnce(ctx); err != nil {
			logging.Warnf("events: conexión a RabbitMQ interrumpida, reintentando en %s: %v", backoff, err)
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

// connectionName se muestra en la UI/API de management de RabbitMQ, para
// poder identificar esta conexión (ej. en tests que la cierran a propósito
// para verificar la reconexión).
const connectionName = "discordia-identify-server-memberships"

func (c *Consumer) consumeOnce(ctx context.Context) error {
	properties := amqp.NewConnectionProperties()
	properties.SetClientConnectionName(connectionName)
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
	queue, err := ch.QueueDeclare(QueueName, true, false, false, false, nil)
	if err != nil {
		return err
	}
	for _, routingKey := range []string{membership.RoutingKeyMemberJoined, membership.RoutingKeyMemberLeft} {
		if err := ch.QueueBind(queue.Name, routingKey, broker.ExchangeName, false, nil); err != nil {
			return err
		}
	}

	// Ack manual (autoAck=false): el mensaje solo se confirma después de
	// procesarlo, para no perder eventos ante un fallo transitorio de DB.
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
				return errors.New("canal de consumo cerrado")
			}
			retrier.Settle(ctx, delivery, shouldRequeue(ctx, c.handler, c.recorder, delivery.RoutingKey, delivery.Body))
		}
	}
}

// Handler para manejo de errores en un envio de mensajes por diferentes factores como errores de red, errores de base de datos, etc.
// Devuelve true si el mensaje debe reintentarse, false si se descarta.
func shouldRequeue(ctx context.Context, handler MembershipHandler, recorder *audit.Recorder, routingKey string, body []byte) bool {
	if routingKey != membership.RoutingKeyMemberJoined && routingKey != membership.RoutingKeyMemberLeft {
		logging.Warnf("events: routing key desconocida %q, descartando mensaje", routingKey)
		recorder.Record(ctx, audit.Entry{RoutingKey: routingKey, RawBody: body, Status: audit.StatusDiscardedUnknownKey})
		return false
	}

	// MemberJoined and MemberLeft share the same wire shape, so decoding into
	// MemberJoined here (regardless of which one this actually is) is fine --
	// it's only used to read/validate fields before dispatching below.
	var payload membership.MemberJoined
	if err := json.Unmarshal(body, &payload); err != nil {
		logging.Warnf("events: payload malformado en %q, descartando mensaje: %v", routingKey, err)
		recorder.Record(ctx, audit.Entry{RoutingKey: routingKey, RawBody: body, Status: audit.StatusDiscardedMalformed, Err: err})
		return false
	}
	// server_id llega como TEXT en el JSON pero la columna es UUID: un valor
	// que no parsea como UUID (o un user_id vacío) haría fallar el INSERT de
	// forma permanente, no transitoria. Sin este chequeo, shouldRequeue lo
	// trataría como fallo de DB y lo reintentaría para siempre (mensaje
	// "poison"). Se descarta acá, igual que un payload malformado.
	if payload.UserID == "" {
		logging.Warnf("events: payload inválido en %q (user_id vacío), event_id=%s, descartando mensaje", routingKey, payload.EventID)
		recorder.Record(ctx, audit.Entry{EventID: payload.EventID, RoutingKey: routingKey, RawBody: body, Status: audit.StatusDiscardedInvalid, Err: errors.New("user_id vacío")})
		return false
	}
	if _, err := uuid.Parse(payload.ServerID); err != nil {
		logging.Warnf("events: payload inválido en %q (server_id %q no es un UUID), event_id=%s: %v, descartando mensaje", routingKey, payload.ServerID, payload.EventID, err)
		recorder.Record(ctx, audit.Entry{EventID: payload.EventID, RoutingKey: routingKey, RawBody: body, Status: audit.StatusDiscardedInvalid, Err: err})
		return false
	}

	var err error
	switch routingKey {
	case membership.RoutingKeyMemberJoined:
		err = handler.HandleMemberJoined(ctx, payload)
	case membership.RoutingKeyMemberLeft:
		err = handler.HandleMemberLeft(ctx, membership.MemberLeft{Meta: payload.Meta, ServerID: payload.ServerID, UserID: payload.UserID})
	}
	if err != nil {
		logging.Errorf("events: fallo transitorio procesando %q, event_id=%s, reintentando: %v", routingKey, payload.EventID, err)
		recorder.Record(ctx, audit.Entry{EventID: payload.EventID, RoutingKey: routingKey, RawBody: body, Status: audit.StatusPending, Err: err})
		return true
	}
	// event_id identifica este evento en los logs de discordia-servers (el
	// publisher) para poder correlacionar ambos lados de principio a fin; no
	// participa en la idempotencia acá (esa la da occurred_at vía Upsert).
	logging.Infof("events: procesado %q event_id=%s server_id=%s user_id=%s", routingKey, payload.EventID, payload.ServerID, payload.UserID)
	recorder.Record(ctx, audit.Entry{EventID: payload.EventID, RoutingKey: routingKey, RawBody: body, Status: audit.StatusProcessed})
	return false
}
