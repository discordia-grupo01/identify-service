package events

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/discordia-grupo01/events-contract/admin"
	"github.com/discordia-grupo01/events-contract/envelope"

	"github.com/discordia-grupo01/identify-service/internal/repository"
)

const (
	adminTestSecret = "test-admin-secret"
	adminTestUserID = "11111111-2222-3333-4444-555555555555"
)

type applyCall struct {
	userID      string
	suspended   bool
	suspendedAt time.Time
	version     int64
}

type suspensionApplierStub struct {
	calls  []applyCall
	result repository.SuspensionResult
	err    error
}

func (s *suspensionApplierStub) ApplySuspension(_ context.Context, userID string, suspended bool, suspendedAt time.Time, version int64) (repository.SuspensionResult, error) {
	s.calls = append(s.calls, applyCall{userID, suspended, suspendedAt, version})
	return s.result, s.err
}

var adminOccurredAt = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func signedSuspended(version int64) admin.UserSuspended {
	event := admin.UserSuspended{
		Meta:    envelope.Meta{EventID: "evt-susp", OccurredAt: adminOccurredAt},
		UserID:  adminTestUserID,
		Version: version,
	}
	event.Sign(adminTestSecret)
	return event
}

func signedReactivated(version int64) admin.UserReactivated {
	event := admin.UserReactivated{
		Meta:    envelope.Meta{EventID: "evt-react", OccurredAt: adminOccurredAt},
		UserID:  adminTestUserID,
		Version: version,
	}
	event.Sign(adminTestSecret)
	return event
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return body
}

func newAdminConsumerForTest(applier SuspensionApplier) *AdminConsumer {
	return NewAdminConsumer("amqp://unused", adminTestSecret, applier, nil)
}

func TestAdminProcessAppliesASignedSuspension(t *testing.T) {
	applier := &suspensionApplierStub{result: repository.SuspensionApplied}
	consumer := newAdminConsumerForTest(applier)
	var invalidated []string
	consumer.OnApplied(func(userID string) { invalidated = append(invalidated, userID) })

	requeue := consumer.Process(context.Background(), admin.RoutingKeyUserSuspended, mustJSON(t, signedSuspended(3)))

	if requeue {
		t.Fatal("Process() = true, want false")
	}
	want := applyCall{adminTestUserID, true, adminOccurredAt, 3}
	if len(applier.calls) != 1 || applier.calls[0] != want {
		t.Fatalf("ApplySuspension calls = %+v, want [%+v]", applier.calls, want)
	}
	if len(invalidated) != 1 || invalidated[0] != adminTestUserID {
		t.Errorf("OnApplied calls = %v, want one for %s", invalidated, adminTestUserID)
	}
}

func TestAdminProcessAppliesASignedReactivation(t *testing.T) {
	applier := &suspensionApplierStub{result: repository.SuspensionApplied}
	consumer := newAdminConsumerForTest(applier)

	requeue := consumer.Process(context.Background(), admin.RoutingKeyUserReactivated, mustJSON(t, signedReactivated(4)))

	if requeue {
		t.Fatal("Process() = true, want false")
	}
	if len(applier.calls) != 1 || applier.calls[0].suspended || applier.calls[0].version != 4 {
		t.Fatalf("ApplySuspension calls = %+v, want one reactivation at version 4", applier.calls)
	}
}

// Sin firma, con firma de otro secreto, con la firma de otra routing key
// (replay de una suspensión como reactivación) o alterado después de firmar:
// se confirma sin tocar nada.
func TestAdminProcessDiscardsInvalidSignatures(t *testing.T) {
	suspended := signedSuspended(3)
	unsigned := suspended
	unsigned.Signature = ""
	otherSecret := admin.UserSuspended{Meta: suspended.Meta, UserID: suspended.UserID, Version: suspended.Version}
	otherSecret.Sign("another-secret")
	tampered := suspended
	tampered.UserID = "99999999-2222-3333-4444-555555555555"
	bumped := suspended
	bumped.Version = 99
	garbage := suspended
	garbage.Signature = "not-hex"

	cases := []struct {
		name       string
		routingKey string
		body       []byte
	}{
		{"unsigned", admin.RoutingKeyUserSuspended, mustJSON(t, unsigned)},
		{"other secret", admin.RoutingKeyUserSuspended, mustJSON(t, otherSecret)},
		{"suspension replayed as reactivation", admin.RoutingKeyUserReactivated, mustJSON(t, suspended)},
		{"tampered user_id", admin.RoutingKeyUserSuspended, mustJSON(t, tampered)},
		{"tampered version", admin.RoutingKeyUserSuspended, mustJSON(t, bumped)},
		{"non-hex signature", admin.RoutingKeyUserSuspended, mustJSON(t, garbage)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			applier := &suspensionApplierStub{}
			consumer := newAdminConsumerForTest(applier)
			consumer.OnApplied(func(string) { t.Error("OnApplied called for a discarded event") })

			if requeue := consumer.Process(context.Background(), tc.routingKey, tc.body); requeue {
				t.Fatal("Process() = true, want false (ack sin reintento)")
			}
			if len(applier.calls) != 0 {
				t.Fatalf("ApplySuspension called %d times, want 0", len(applier.calls))
			}
		})
	}
}

func TestAdminProcessWithEmptySecretNeverVerifies(t *testing.T) {
	applier := &suspensionApplierStub{}
	consumer := NewAdminConsumer("amqp://unused", "", applier, nil)

	consumer.Process(context.Background(), admin.RoutingKeyUserSuspended, mustJSON(t, signedSuspended(1)))

	if len(applier.calls) != 0 {
		t.Fatalf("ApplySuspension called %d times with an empty secret, want 0", len(applier.calls))
	}
}

func TestAdminProcessDiscardsBrokenMessages(t *testing.T) {
	notUUID := signedSuspended(1)
	notUUID.UserID = "not-a-uuid"
	notUUID.Sign(adminTestSecret)

	cases := []struct {
		name       string
		routingKey string
		body       []byte
	}{
		{"not json", admin.RoutingKeyUserSuspended, []byte(`not-json`)},
		{"user_id is not a uuid", admin.RoutingKeyUserSuspended, mustJSON(t, notUUID)},
		{"unknown routing key", "admin.user_deleted", []byte(`{}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			applier := &suspensionApplierStub{}
			consumer := newAdminConsumerForTest(applier)

			if requeue := consumer.Process(context.Background(), tc.routingKey, tc.body); requeue {
				t.Fatal("Process() = true, want false (descarte permanente)")
			}
			if len(applier.calls) != 0 {
				t.Fatalf("ApplySuspension called %d times, want 0", len(applier.calls))
			}
		})
	}
}

// Duplicado, fuera de orden o usuario inexistente: se confirma y no avisa a
// OnApplied (el estado no cambió).
func TestAdminProcessAcksStaleAndUnknownUserWithoutInvalidating(t *testing.T) {
	for name, result := range map[string]repository.SuspensionResult{
		"stale":        repository.SuspensionStale,
		"unknown user": repository.SuspensionUserNotFound,
	} {
		t.Run(name, func(t *testing.T) {
			applier := &suspensionApplierStub{result: result}
			consumer := newAdminConsumerForTest(applier)
			consumer.OnApplied(func(string) { t.Error("OnApplied called although the state did not change") })

			if requeue := consumer.Process(context.Background(), admin.RoutingKeyUserSuspended, mustJSON(t, signedSuspended(2))); requeue {
				t.Fatal("Process() = true, want false")
			}
			if len(applier.calls) != 1 {
				t.Fatalf("ApplySuspension calls = %d, want 1", len(applier.calls))
			}
		})
	}
}

// Una falla de base es lo único que se reintenta.
func TestAdminProcessRequeuesOnTransientFailure(t *testing.T) {
	applier := &suspensionApplierStub{err: errors.New("db down")}
	consumer := newAdminConsumerForTest(applier)
	consumer.OnApplied(func(string) { t.Error("OnApplied called on a failed apply") })

	if requeue := consumer.Process(context.Background(), admin.RoutingKeyUserSuspended, mustJSON(t, signedSuspended(1))); !requeue {
		t.Fatal("Process() = false, want true (nack requeue)")
	}
}
