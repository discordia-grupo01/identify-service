package events

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type membershipHandlerStub struct {
	joinedCalls []EventPayload
	leftCalls   []EventPayload
	joinedErr   error
	leftErr     error
}

func (s *membershipHandlerStub) HandleMemberJoined(_ context.Context, payload EventPayload) error {
	s.joinedCalls = append(s.joinedCalls, payload)
	return s.joinedErr
}

func (s *membershipHandlerStub) HandleMemberLeft(_ context.Context, payload EventPayload) error {
	s.leftCalls = append(s.leftCalls, payload)
	return s.leftErr
}

func TestShouldRequeueDiscardsUnknownRoutingKey(t *testing.T) {
	handler := &membershipHandlerStub{}

	requeue := shouldRequeue(context.Background(), handler, "servers.server_deleted", []byte(`{}`))

	if requeue {
		t.Fatalf("shouldRequeue() = true, want false (ack sin reintento)")
	}
	if len(handler.joinedCalls) != 0 || len(handler.leftCalls) != 0 {
		t.Fatalf("handler no debería haber sido invocado para una routing key desconocida")
	}
}

func TestShouldRequeueDiscardsMalformedPayload(t *testing.T) {
	handler := &membershipHandlerStub{}

	requeue := shouldRequeue(context.Background(), handler, RoutingKeyMemberJoined, []byte(`not-json`))

	if requeue {
		t.Fatalf("shouldRequeue() = true, want false (ack sin reintento)")
	}
	if len(handler.joinedCalls) != 0 {
		t.Fatalf("handler no debería haber sido invocado para un payload malformado")
	}
}

func TestShouldRequeueDiscardsInvalidServerID(t *testing.T) {
	handler := &membershipHandlerStub{}
	body, err := json.Marshal(EventPayload{
		EventID: "11111111-1111-1111-1111-111111111111", ServerID: "not-a-uuid", UserID: "user-id", OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	requeue := shouldRequeue(context.Background(), handler, RoutingKeyMemberJoined, body)

	if requeue {
		t.Fatalf("shouldRequeue() = true, want false (ack sin reintento: un server_id inválido es un error permanente, no transitorio)")
	}
	if len(handler.joinedCalls) != 0 {
		t.Fatalf("handler no debería haber sido invocado con un server_id inválido")
	}
}

func TestShouldRequeueDiscardsEmptyUserID(t *testing.T) {
	handler := &membershipHandlerStub{}
	body, err := json.Marshal(EventPayload{
		EventID: "11111111-1111-1111-1111-111111111111", ServerID: "22222222-2222-2222-2222-222222222222", UserID: "", OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	requeue := shouldRequeue(context.Background(), handler, RoutingKeyMemberLeft, body)

	if requeue {
		t.Fatalf("shouldRequeue() = true, want false (ack sin reintento)")
	}
	if len(handler.leftCalls) != 0 {
		t.Fatalf("handler no debería haber sido invocado con un user_id vacío")
	}
}

func TestShouldRequeueOnTransientHandlerFailure(t *testing.T) {
	handler := &membershipHandlerStub{joinedErr: errors.New("database unavailable")}
	body := validPayloadJSON(t)

	requeue := shouldRequeue(context.Background(), handler, RoutingKeyMemberJoined, body)

	if !requeue {
		t.Fatalf("shouldRequeue() = false, want true (nack + requeue ante fallo transitorio)")
	}
	if len(handler.joinedCalls) != 1 {
		t.Fatalf("handler.HandleMemberJoined llamado %d veces, want 1", len(handler.joinedCalls))
	}
}

func TestShouldRequeueDispatchesMemberJoined(t *testing.T) {
	handler := &membershipHandlerStub{}
	body := validPayloadJSON(t)

	requeue := shouldRequeue(context.Background(), handler, RoutingKeyMemberJoined, body)

	if requeue {
		t.Fatalf("shouldRequeue() = true, want false")
	}
	if len(handler.joinedCalls) != 1 || len(handler.leftCalls) != 0 {
		t.Fatalf("dispatch incorrecto: joined=%d left=%d", len(handler.joinedCalls), len(handler.leftCalls))
	}
}

func TestShouldRequeueDispatchesMemberLeft(t *testing.T) {
	handler := &membershipHandlerStub{}
	body := validPayloadJSON(t)

	requeue := shouldRequeue(context.Background(), handler, RoutingKeyMemberLeft, body)

	if requeue {
		t.Fatalf("shouldRequeue() = true, want false")
	}
	if len(handler.leftCalls) != 1 || len(handler.joinedCalls) != 0 {
		t.Fatalf("dispatch incorrecto: joined=%d left=%d", len(handler.joinedCalls), len(handler.leftCalls))
	}
}

func validPayloadJSON(t *testing.T) []byte {
	t.Helper()
	payload := EventPayload{
		EventID:    "11111111-1111-1111-1111-111111111111",
		ServerID:   "22222222-2222-2222-2222-222222222222",
		UserID:     "user-id",
		OccurredAt: time.Date(2026, time.September, 13, 10, 0, 0, 0, time.UTC),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return body
}
