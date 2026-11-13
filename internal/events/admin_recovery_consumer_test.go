package events

import (
	"context"
	"errors"
	"testing"

	"github.com/discordia-grupo01/events-contract/admin"
	"github.com/discordia-grupo01/events-contract/envelope"

	"github.com/discordia-grupo01/identify-service/internal/repository"
)

const adminTestRequestID = "99999999-8888-7777-6666-555555555555"

type resolveCall struct {
	requestID string
	userID    string
	approved  bool
}

type recoveryResolverStub struct {
	calls  []resolveCall
	result repository.RecoveryResolution
	err    error
}

func (s *recoveryResolverStub) Resolve(_ context.Context, requestID, userID string, approved bool) (repository.RecoveryResolution, error) {
	s.calls = append(s.calls, resolveCall{requestID, userID, approved})
	return s.result, s.err
}

func signedRecoveryApproved() admin.AccountRecoveryApproved {
	event := admin.AccountRecoveryApproved{
		Meta:      envelope.Meta{EventID: "evt-approved", OccurredAt: adminOccurredAt},
		RequestID: adminTestRequestID,
		UserID:    adminTestUserID,
	}
	event.Sign(adminTestSecret)
	return event
}

func signedRecoveryRejected() admin.AccountRecoveryRejected {
	event := admin.AccountRecoveryRejected{
		Meta:      envelope.Meta{EventID: "evt-rejected", OccurredAt: adminOccurredAt},
		RequestID: adminTestRequestID,
		UserID:    adminTestUserID,
	}
	event.Sign(adminTestSecret)
	return event
}

func newRecoveryConsumerForTest(resolver AccountRecoveryResolver) *AdminConsumer {
	consumer := newAdminConsumerForTest(&suspensionApplierStub{})
	consumer.HandleAccountRecovery(resolver)
	return consumer
}

func TestAdminProcessAppliesSignedRecoveryDecisions(t *testing.T) {
	cases := map[string]struct {
		routingKey string
		body       any
		approved   bool
	}{
		"aprobación": {admin.RoutingKeyAccountRecoveryApproved, signedRecoveryApproved(), true},
		"rechazo":    {admin.RoutingKeyAccountRecoveryRejected, signedRecoveryRejected(), false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			resolver := &recoveryResolverStub{result: repository.RecoveryResolved}
			consumer := newRecoveryConsumerForTest(resolver)

			if consumer.Process(context.Background(), tc.routingKey, mustJSON(t, tc.body)) {
				t.Fatal("Process() = true, want false")
			}
			want := resolveCall{adminTestRequestID, adminTestUserID, tc.approved}
			if len(resolver.calls) != 1 || resolver.calls[0] != want {
				t.Fatalf("Resolve calls = %+v, want [%+v]", resolver.calls, want)
			}
		})
	}
}

func TestAdminProcessDiscardsRecoveryWithInvalidSignature(t *testing.T) {
	tampered := signedRecoveryApproved()
	tampered.UserID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	unsigned := signedRecoveryApproved()
	unsigned.Signature = ""

	// Un rechazo firmado no puede reenviarse como aprobación.
	rejected := signedRecoveryRejected()
	replayed := admin.AccountRecoveryApproved(rejected)

	for name, body := range map[string]admin.AccountRecoveryApproved{
		"otro usuario": tampered,
		"sin firma":    unsigned,
		"reenviado":    replayed,
	} {
		t.Run(name, func(t *testing.T) {
			resolver := &recoveryResolverStub{}
			consumer := newRecoveryConsumerForTest(resolver)

			if consumer.Process(context.Background(), admin.RoutingKeyAccountRecoveryApproved, mustJSON(t, body)) {
				t.Fatal("Process() = true, want false")
			}
			if len(resolver.calls) != 0 {
				t.Fatalf("Resolve calls = %+v, want none", resolver.calls)
			}
		})
	}
}

func TestAdminProcessDiscardsBrokenRecoveryMessages(t *testing.T) {
	notUUID := admin.AccountRecoveryApproved{
		Meta:      envelope.Meta{EventID: "evt-x", OccurredAt: adminOccurredAt},
		RequestID: "no-es-un-uuid",
		UserID:    adminTestUserID,
	}
	notUUID.Sign(adminTestSecret)

	for name, body := range map[string][]byte{
		"json roto":   []byte("{"),
		"id inválido": mustJSON(t, notUUID),
	} {
		t.Run(name, func(t *testing.T) {
			resolver := &recoveryResolverStub{}
			consumer := newRecoveryConsumerForTest(resolver)

			if consumer.Process(context.Background(), admin.RoutingKeyAccountRecoveryApproved, body) {
				t.Fatal("Process() = true, want false")
			}
			if len(resolver.calls) != 0 {
				t.Fatalf("Resolve calls = %+v, want none", resolver.calls)
			}
		})
	}
}

func TestAdminProcessAcksResolvedOrUnknownRecoveryRequests(t *testing.T) {
	for _, result := range []repository.RecoveryResolution{repository.RecoveryAlreadyResolved, repository.RecoveryNotFound} {
		resolver := &recoveryResolverStub{result: result}
		consumer := newRecoveryConsumerForTest(resolver)

		if consumer.Process(context.Background(), admin.RoutingKeyAccountRecoveryApproved, mustJSON(t, signedRecoveryApproved())) {
			t.Errorf("resolution %d: Process() = true, want false", result)
		}
	}
}

func TestAdminProcessRequeuesRecoveryOnTransientFailure(t *testing.T) {
	resolver := &recoveryResolverStub{err: errors.New("db caída")}
	consumer := newRecoveryConsumerForTest(resolver)

	if !consumer.Process(context.Background(), admin.RoutingKeyAccountRecoveryRejected, mustJSON(t, signedRecoveryRejected())) {
		t.Fatal("Process() = false, want true")
	}
}

func TestAdminConsumerBindsRecoveryKeysOnlyWhenConfigured(t *testing.T) {
	without := newAdminConsumerForTest(&suspensionApplierStub{})
	if contains(without.routingKeys(), admin.RoutingKeyAccountRecoveryApproved) {
		t.Error("sin HandleAccountRecovery no debería bindear las claves de recuperación")
	}
	// Sin resolver, un evento de recuperación es una clave desconocida.
	if without.Process(context.Background(), admin.RoutingKeyAccountRecoveryApproved, mustJSON(t, signedRecoveryApproved())) {
		t.Error("Process() = true, want false")
	}

	with := newRecoveryConsumerForTest(&recoveryResolverStub{})
	for _, key := range []string{admin.RoutingKeyAccountRecoveryApproved, admin.RoutingKeyAccountRecoveryRejected, admin.RoutingKeyUserSuspended} {
		if !contains(with.routingKeys(), key) {
			t.Errorf("falta bindear %q", key)
		}
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
