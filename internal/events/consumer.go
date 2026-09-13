package events

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	ExchangeName = "discordia.events"
	QueueName    = "identify.server_memberships"

	RoutingKeyMemberJoined = "servers.member_joined"
	RoutingKeyMemberLeft   = "servers.member_left"
)

type EventPayload struct {
	EventID    string    `json:"event_id"`
	ServerID   string    `json:"server_id"`
	UserID     string    `json:"user_id"`
	OccurredAt time.Time `json:"occurred_at"`
}

type MembershipHandler interface {
	HandleMemberJoined(ctx context.Context, payload EventPayload) error
	HandleMemberLeft(ctx context.Context, payload EventPayload) error
}

type Consumer struct {
	url     string
	handler MembershipHandler
}

func NewConsumer(url string, handler MembershipHandler) *Consumer {
	return &Consumer{url: url, handler: handler}
}

func (c *Consumer) Start(ctx context.Context) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for ctx.Err() == nil {
		if err := c.consumeOnce(ctx); err != nil {
			log.Printf("events: conexión a RabbitMQ interrumpida, reintentando en %s: %v", backoff, err)
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

func (c *Consumer) consumeOnce(ctx context.Context) error {
	conn, err := amqp.Dial(c.url)
	if err != nil {
		return err
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	if err := ch.ExchangeDeclare(ExchangeName, "topic", true, false, false, false, nil); err != nil {
		return err
	}
	queue, err := ch.QueueDeclare(QueueName, true, false, false, false, nil)
	if err != nil {
		return err
	}
	for _, routingKey := range []string{RoutingKeyMemberJoined, RoutingKeyMemberLeft} {
		if err := ch.QueueBind(queue.Name, routingKey, ExchangeName, false, nil); err != nil {
			return err
		}
	}

	// Ack manual (autoAck=false): el mensaje solo se confirma después de
	// procesarlo, para no perder eventos ante un fallo transitorio de DB.
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
			c.handleDelivery(ctx, delivery)
		}
	}
}

func (c *Consumer) handleDelivery(ctx context.Context, delivery amqp.Delivery) {
	if shouldRequeue(ctx, c.handler, delivery.RoutingKey, delivery.Body) {
		_ = delivery.Nack(false, true)
		return
	}
	_ = delivery.Ack(false)
}

// Handler para manejo de errores en un envio de mensajes por diferentes factores como errores de red, errores de base de datos, etc.
// Devuelve true si el mensaje debe reintentarse, false si se descarta.
func shouldRequeue(ctx context.Context, handler MembershipHandler, routingKey string, body []byte) bool {
	if routingKey != RoutingKeyMemberJoined && routingKey != RoutingKeyMemberLeft {
		log.Printf("events: routing key desconocida %q, descartando mensaje", routingKey)
		return false
	}

	var payload EventPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Printf("events: payload malformado en %q, descartando mensaje: %v", routingKey, err)
		return false
	}

	var err error
	switch routingKey {
	case RoutingKeyMemberJoined:
		err = handler.HandleMemberJoined(ctx, payload)
	case RoutingKeyMemberLeft:
		err = handler.HandleMemberLeft(ctx, payload)
	}
	if err != nil {
		log.Printf("events: fallo transitorio procesando %q, reintentando: %v", routingKey, err)
		return true
	}
	return false
}
