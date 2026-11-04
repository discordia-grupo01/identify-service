package events

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/discordia-grupo01/events-contract/billing"
	"github.com/discordia-grupo01/events-contract/envelope"

	"github.com/discordia-grupo01/identify-service/internal/repository"
)

const (
	billingTestSecret = "test-billing-secret"
	billingTestUserID = "11111111-2222-3333-4444-555555555555"
)

type premiumCall struct {
	userID  string
	active  bool
	version int64
}

type premiumApplierStub struct {
	calls  []premiumCall
	result repository.PremiumResult
	err    error
}

func (s *premiumApplierStub) ApplyPremium(_ context.Context, userID string, active bool, version int64) (repository.PremiumResult, error) {
	s.calls = append(s.calls, premiumCall{userID, active, version})
	return s.result, s.err
}

func signedPremium(active bool, version int64) billing.PremiumUpdated {
	until := time.Date(2026, 12, 3, 12, 0, 0, 0, time.UTC)
	event := billing.PremiumUpdated{
		Meta:         envelope.Meta{EventID: "evt-premium", OccurredAt: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)},
		UserID:       billingTestUserID,
		Active:       active,
		PremiumUntil: &until,
		Renews:       true,
		Version:      version,
	}
	event.Sign(billingTestSecret)
	return event
}

func newBillingConsumerForTest(applier PremiumApplier) *BillingConsumer {
	return NewBillingConsumer("amqp://unused", billingTestSecret, applier, nil)
}

func TestBillingProcessAppliesASignedEvent(t *testing.T) {
	for _, active := range []bool{true, false} {
		applier := &premiumApplierStub{result: repository.PremiumApplied}
		consumer := newBillingConsumerForTest(applier)

		if requeue := consumer.Process(context.Background(), billing.RoutingKeyPremiumUpdated, mustJSON(t, signedPremium(active, 4))); requeue {
			t.Fatal("Process() = true, want false")
		}
		want := premiumCall{billingTestUserID, active, 4}
		if len(applier.calls) != 1 || applier.calls[0] != want {
			t.Errorf("ApplyPremium calls = %+v, want [%+v]", applier.calls, want)
		}
	}
}

// Sin firma, con firma de otro secreto o alterado después de firmar (por
// ejemplo, alguien que se pone active=true): se confirma sin tocar nada.
func TestBillingProcessDiscardsInvalidSignatures(t *testing.T) {
	valid := signedPremium(false, 3)
	unsigned := valid
	unsigned.Signature = ""
	otherSecret := valid
	otherSecret.Sign("another-secret")
	selfGranted := valid
	selfGranted.Active = true
	otherUser := valid
	otherUser.UserID = "99999999-2222-3333-4444-555555555555"
	bumped := valid
	bumped.Version = 99
	garbage := valid
	garbage.Signature = "not-hex"

	cases := map[string]billing.PremiumUpdated{
		"unsigned":          unsigned,
		"other secret":      otherSecret,
		"tampered active":   selfGranted,
		"tampered user_id":  otherUser,
		"tampered version":  bumped,
		"non-hex signature": garbage,
	}
	for name, event := range cases {
		t.Run(name, func(t *testing.T) {
			applier := &premiumApplierStub{}
			if requeue := newBillingConsumerForTest(applier).Process(context.Background(), billing.RoutingKeyPremiumUpdated, mustJSON(t, event)); requeue {
				t.Fatal("Process() = true, want false (ack sin reintento)")
			}
			if len(applier.calls) != 0 {
				t.Fatalf("ApplyPremium called %d times, want 0", len(applier.calls))
			}
		})
	}
}

func TestBillingProcessWithEmptySecretNeverVerifies(t *testing.T) {
	applier := &premiumApplierStub{}
	consumer := NewBillingConsumer("amqp://unused", "", applier, nil)

	consumer.Process(context.Background(), billing.RoutingKeyPremiumUpdated, mustJSON(t, signedPremium(true, 1)))

	if len(applier.calls) != 0 {
		t.Fatalf("ApplyPremium called %d times with an empty secret, want 0", len(applier.calls))
	}
}

func TestBillingProcessDiscardsBrokenMessages(t *testing.T) {
	notUUID := signedPremium(true, 1)
	notUUID.UserID = "not-a-uuid"
	notUUID.Sign(billingTestSecret)

	cases := map[string]struct {
		routingKey string
		body       []byte
	}{
		"not json":              {billing.RoutingKeyPremiumUpdated, []byte(`not-json`)},
		"user_id is not a uuid": {billing.RoutingKeyPremiumUpdated, mustJSON(t, notUUID)},
		"unknown routing key":   {"billing.server_boost_added", []byte(`{}`)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			applier := &premiumApplierStub{}
			if requeue := newBillingConsumerForTest(applier).Process(context.Background(), tc.routingKey, tc.body); requeue {
				t.Fatal("Process() = true, want false (descarte permanente)")
			}
			if len(applier.calls) != 0 {
				t.Fatalf("ApplyPremium called %d times, want 0", len(applier.calls))
			}
		})
	}
}

// Duplicado, fuera de orden o usuario inexistente: se confirma.
func TestBillingProcessAcksStaleAndUnknownUser(t *testing.T) {
	for name, result := range map[string]repository.PremiumResult{
		"stale":        repository.PremiumStale,
		"unknown user": repository.PremiumUserNotFound,
	} {
		t.Run(name, func(t *testing.T) {
			applier := &premiumApplierStub{result: result}
			if requeue := newBillingConsumerForTest(applier).Process(context.Background(), billing.RoutingKeyPremiumUpdated, mustJSON(t, signedPremium(true, 2))); requeue {
				t.Fatal("Process() = true, want false")
			}
			if len(applier.calls) != 1 {
				t.Fatalf("ApplyPremium calls = %d, want 1", len(applier.calls))
			}
		})
	}
}

// Una falla de base es lo único que se reintenta.
func TestBillingProcessRequeuesOnTransientFailure(t *testing.T) {
	applier := &premiumApplierStub{err: errors.New("db down")}
	if requeue := newBillingConsumerForTest(applier).Process(context.Background(), billing.RoutingKeyPremiumUpdated, mustJSON(t, signedPremium(true, 1))); !requeue {
		t.Fatal("Process() = false, want true (nack requeue)")
	}
}

func TestBillingSetQueueName(t *testing.T) {
	consumer := newBillingConsumerForTest(&premiumApplierStub{})
	if consumer.queueName != BillingQueueName {
		t.Fatalf("default queue = %q, want %q", consumer.queueName, BillingQueueName)
	}
	consumer.SetQueueName("test.queue")
	if consumer.queueName != "test.queue" {
		t.Errorf("queue = %q, want test.queue", consumer.queueName)
	}
}
