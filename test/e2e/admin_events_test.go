package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/discordia-grupo01/events-contract/admin"
	"github.com/discordia-grupo01/events-contract/broker"
	"github.com/discordia-grupo01/events-contract/envelope"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/discordia-grupo01/identify-service/internal/audit"
	"github.com/discordia-grupo01/identify-service/internal/events"
	"github.com/discordia-grupo01/identify-service/internal/repository"
)

const adminE2ESecret = "e2e-admin-secret"

func suspendedEvent(userID string, version int64) admin.UserSuspended {
	event := admin.UserSuspended{
		Meta:    envelope.Meta{EventID: uuid.NewString(), OccurredAt: time.Now().UTC().Truncate(time.Second)},
		UserID:  userID,
		Version: version,
	}
	event.Sign(adminE2ESecret)
	return event
}

func reactivatedEvent(userID string, version int64) admin.UserReactivated {
	event := admin.UserReactivated{
		Meta:    envelope.Meta{EventID: uuid.NewString(), OccurredAt: time.Now().UTC().Truncate(time.Second)},
		UserID:  userID,
		Version: version,
	}
	event.Sign(adminE2ESecret)
	return event
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return body
}

func newAdminConsumer(db *sql.DB) *events.AdminConsumer {
	return newAdminConsumerFor("amqp://unused", db)
}

func newAdminConsumerFor(rabbitMQURL string, db *sql.DB) *events.AdminConsumer {
	return events.NewAdminConsumer(rabbitMQURL, adminE2ESecret, repository.NewUserRepository(db), audit.NewRecorder(db))
}

// auditStatuses devuelve los status de consumed_events para una routing key,
// del más viejo al más nuevo.
func auditStatuses(t *testing.T, db *sql.DB, routingKey string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT status FROM consumed_events WHERE routing_key = $1 ORDER BY received_at, id`, routingKey)
	if err != nil {
		t.Fatalf("query consumed_events: %v", err)
	}
	defer rows.Close()
	var statuses []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		statuses = append(statuses, s)
	}
	return statuses
}

// Con la base real y la auditoría real (sin broker): una suspensión firmada
// se aplica y se audita; una reactivación posterior la levanta.
func TestAdminConsumer_AppliesSuspensionAndReactivation(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	user := registerAndLoginSession(t, router, "Quinn", "quinn@example.com", "Passw0rd!")
	consumer := newAdminConsumer(db)
	ctx := context.Background()

	suspend := suspendedEvent(user.UserID, 1)
	if requeue := consumer.Process(ctx, admin.RoutingKeyUserSuspended, mustMarshal(t, suspend)); requeue {
		t.Fatal("Process(suspend) = true, want false")
	}
	suspendedAt, version := suspensionState(t, db, user.UserID)
	if !suspendedAt.Valid || !suspendedAt.Time.Equal(suspend.OccurredAt) || version != 1 {
		t.Errorf("state = %v (valid %v), version %d; want suspended at %v, version 1", suspendedAt.Time, suspendedAt.Valid, version, suspend.OccurredAt)
	}
	if n := activeRefreshTokens(t, db, user.UserID); n != 0 {
		t.Errorf("active refresh tokens = %d, want 0", n)
	}
	assertConsumedEventStatus(t, db, suspend.EventID, "processed")

	reactivate := reactivatedEvent(user.UserID, 2)
	if requeue := consumer.Process(ctx, admin.RoutingKeyUserReactivated, mustMarshal(t, reactivate)); requeue {
		t.Fatal("Process(reactivate) = true, want false")
	}
	suspendedAt, version = suspensionState(t, db, user.UserID)
	if suspendedAt.Valid || version != 2 {
		t.Errorf("state = suspended_at valid %v, version %d; want active at version 2", suspendedAt.Valid, version)
	}
	assertConsumedEventStatus(t, db, reactivate.EventID, "processed")
}

// Firma ausente, falsa o de otra routing key: no cambia ningún estado, no
// toca los refresh tokens y queda auditado como discarded_invalid_signature.
func TestAdminConsumer_InvalidSignatureChangesNothingAndIsAudited(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	user := registerAndLoginSession(t, router, "Rita", "rita@example.com", "Passw0rd!")
	consumer := newAdminConsumer(db)
	ctx := context.Background()

	forged := suspendedEvent(user.UserID, 1)
	forged.Signature = "00" + forged.Signature[2:]
	unsigned := suspendedEvent(user.UserID, 2)
	unsigned.Signature = ""
	replayed := suspendedEvent(user.UserID, 3) // firma de user_suspended enviada como user_reactivated

	for _, tc := range []struct {
		routingKey string
		body       []byte
	}{
		{admin.RoutingKeyUserSuspended, mustMarshal(t, forged)},
		{admin.RoutingKeyUserSuspended, mustMarshal(t, unsigned)},
		{admin.RoutingKeyUserReactivated, mustMarshal(t, replayed)},
	} {
		if requeue := consumer.Process(ctx, tc.routingKey, tc.body); requeue {
			t.Fatalf("Process(%s) = true, want false (ack sin reintento)", tc.routingKey)
		}
	}

	suspendedAt, version := suspensionState(t, db, user.UserID)
	if suspendedAt.Valid || version != 0 {
		t.Errorf("state = suspended_at valid %v, version %d; want untouched", suspendedAt.Valid, version)
	}
	if n := activeRefreshTokens(t, db, user.UserID); n == 0 {
		t.Error("refresh tokens were revoked by an unauthenticated event")
	}
	got := append(auditStatuses(t, db, admin.RoutingKeyUserSuspended), auditStatuses(t, db, admin.RoutingKeyUserReactivated)...)
	if len(got) != 3 {
		t.Fatalf("audit rows = %v, want 3", got)
	}
	for _, status := range got {
		if status != "discarded_invalid_signature" {
			t.Errorf("audit status = %q, want discarded_invalid_signature", status)
		}
	}
}

// Un event_id falsificado no puede pisar la auditoría de un evento legítimo
// ya procesado.
func TestAdminConsumer_ForgedEventIDDoesNotOverwriteTheAudit(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	user := registerAndLoginSession(t, router, "Sara", "sara@example.com", "Passw0rd!")
	consumer := newAdminConsumer(db)
	ctx := context.Background()

	real := suspendedEvent(user.UserID, 1)
	consumer.Process(ctx, admin.RoutingKeyUserSuspended, mustMarshal(t, real))

	forged := real
	forged.Version = 9
	consumer.Process(ctx, admin.RoutingKeyUserSuspended, mustMarshal(t, forged))

	assertConsumedEventStatus(t, db, real.EventID, "processed")
	if _, version := suspensionState(t, db, user.UserID); version != 1 {
		t.Errorf("suspension_version = %d, want 1", version)
	}
}

// Duplicado y evento viejo: no-op confirmado. Usuario inexistente: se audita
// y no se crea nada.
func TestAdminConsumer_StaleDuplicateAndUnknownUser(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	user := registerAndLoginSession(t, router, "Tina", "tina@example.com", "Passw0rd!")
	consumer := newAdminConsumer(db)
	ctx := context.Background()

	suspend := suspendedEvent(user.UserID, 5)
	body := mustMarshal(t, suspend)
	consumer.Process(ctx, admin.RoutingKeyUserSuspended, body)
	consumer.Process(ctx, admin.RoutingKeyUserSuspended, body) // duplicado exacto
	old := reactivatedEvent(user.UserID, 4)                    // llega tarde
	if requeue := consumer.Process(ctx, admin.RoutingKeyUserReactivated, mustMarshal(t, old)); requeue {
		t.Fatal("Process(stale) = true, want false")
	}
	suspendedAt, version := suspensionState(t, db, user.UserID)
	if !suspendedAt.Valid || version != 5 {
		t.Errorf("state = suspended_at valid %v, version %d; want still suspended at version 5", suspendedAt.Valid, version)
	}

	ghost := suspendedEvent(uuid.NewString(), 1)
	if requeue := consumer.Process(ctx, admin.RoutingKeyUserSuspended, mustMarshal(t, ghost)); requeue {
		t.Fatal("Process(unknown user) = true, want false")
	}
	assertConsumedEventStatus(t, db, ghost.EventID, "discarded_unknown_user")
	var users int
	if err := db.QueryRow(`SELECT count(*) FROM users`).Scan(&users); err != nil || users != 1 {
		t.Errorf("users = %d (err %v), want 1 (nothing created)", users, err)
	}
}

// Mensaje roto: descarte permanente. Falla de base: se reencola (la tabla
// users no existe mientras dura el test).
func TestAdminConsumer_BrokenMessageIsDiscardedAndDatabaseFailureRequeues(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	user := registerAndLoginSession(t, router, "Uma", "uma@example.com", "Passw0rd!")
	consumer := newAdminConsumer(db)
	ctx := context.Background()

	if requeue := consumer.Process(ctx, admin.RoutingKeyUserSuspended, []byte(`not-json`)); requeue {
		t.Error("Process(not json) = true, want false")
	}
	badUser := suspendedEvent("not-a-uuid", 1)
	if requeue := consumer.Process(ctx, admin.RoutingKeyUserSuspended, mustMarshal(t, badUser)); requeue {
		t.Error("Process(non-uuid user_id) = true, want false")
	}
	assertConsumedEventStatus(t, db, badUser.EventID, "discarded_invalid_payload")

	if _, err := db.Exec(`ALTER TABLE users RENAME TO users_off`); err != nil {
		t.Fatalf("rename users: %v", err)
	}
	restored := false
	restore := func() {
		if !restored {
			restored = true
			if _, err := db.Exec(`ALTER TABLE users_off RENAME TO users`); err != nil {
				t.Errorf("restore users: %v", err)
			}
		}
	}
	t.Cleanup(restore)

	transient := suspendedEvent(user.UserID, 1)
	requeue := consumer.Process(ctx, admin.RoutingKeyUserSuspended, mustMarshal(t, transient))
	restore()
	if !requeue {
		t.Fatal("Process() = false on a database failure, want true (nack requeue)")
	}
	assertConsumedEventStatus(t, db, transient.EventID, "pending")
	if _, version := suspensionState(t, db, user.UserID); version != 0 {
		t.Errorf("suspension_version = %d, want 0 after the failed apply", version)
	}
}

func publishRaw(t *testing.T, rabbitMQURL, routingKey string, body []byte) {
	t.Helper()
	conn, err := amqp.Dial(rabbitMQURL)
	if err != nil {
		t.Fatalf("amqp.Dial() error = %v", err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("conn.Channel() error = %v", err)
	}
	defer ch.Close()
	if err := ch.ExchangeDeclare(broker.ExchangeName, broker.ExchangeType, true, false, false, false, nil); err != nil {
		t.Fatalf("ExchangeDeclare() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ch.PublishWithContext(ctx, broker.ExchangeName, routingKey, false, false, amqp.Publishing{ContentType: "application/json", Body: body}); err != nil {
		t.Fatalf("PublishWithContext() error = %v", err)
	}
}

// declareAdminQueue declara y bindea una cola propia del test (el RabbitMQ
// puede ser compartido) antes de publicar: un topic exchange descarta lo
// publicado si todavía no hay cola bindeada. La borra al terminar.
func declareAdminQueue(t *testing.T, rabbitMQURL, queueName string) {
	t.Helper()
	conn, err := amqp.Dial(rabbitMQURL)
	if err != nil {
		t.Fatalf("amqp.Dial() error = %v", err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("conn.Channel() error = %v", err)
	}
	defer ch.Close()
	if err := ch.ExchangeDeclare(broker.ExchangeName, broker.ExchangeType, true, false, false, false, nil); err != nil {
		t.Fatalf("ExchangeDeclare() error = %v", err)
	}
	if _, err := ch.QueueDeclare(queueName, true, false, false, false, nil); err != nil {
		t.Fatalf("QueueDeclare() error = %v", err)
	}
	for _, key := range []string{admin.RoutingKeyUserSuspended, admin.RoutingKeyUserReactivated} {
		if err := ch.QueueBind(queueName, key, broker.ExchangeName, false, nil); err != nil {
			t.Fatalf("QueueBind() error = %v", err)
		}
	}
	t.Cleanup(func() {
		cleanupConn, err := amqp.Dial(rabbitMQURL)
		if err != nil {
			return
		}
		defer cleanupConn.Close()
		if cleanupCh, err := cleanupConn.Channel(); err == nil {
			defer cleanupCh.Close()
			_, _ = cleanupCh.QueueDelete(queueName, false, false, false)
		}
	})
}

// Pipeline real: eventos publicados al exchange discordia.events llegan al
// AdminConsumer real vía RabbitMQ y cambian (o no) el estado en la base. Se
// saltea sin RABBITMQ_URL.
func TestAdminConsumer_RabbitMQPipeline(t *testing.T) {
	rabbitMQURL := os.Getenv("RABBITMQ_URL")
	if rabbitMQURL == "" {
		t.Skip("RABBITMQ_URL no está seteado: este test e2e requiere un RabbitMQ real")
	}
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	user := registerAndLoginSession(t, router, "Vera", "vera@example.com", "Passw0rd!")

	queueName := "identify.admin_events.e2e." + uuid.NewString()
	declareAdminQueue(t, rabbitMQURL, queueName)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	consumer := newAdminConsumerFor(rabbitMQURL, db)
	consumer.SetQueueName(queueName)
	go consumer.Start(ctx)

	// Un evento falsificado primero: no debe cambiar nada.
	forged := suspendedEvent(user.UserID, 1)
	forged.Signature = "00" + forged.Signature[2:]
	publishRaw(t, rabbitMQURL, admin.RoutingKeyUserSuspended, mustMarshal(t, forged))

	suspend := suspendedEvent(user.UserID, 2)
	publishRaw(t, rabbitMQURL, admin.RoutingKeyUserSuspended, mustMarshal(t, suspend))
	waitForCondition(t, 10*time.Second, func() bool {
		suspendedAt, version := suspensionStateQuiet(db, user.UserID)
		return suspendedAt.Valid && version == 2
	}, "el consumer no aplicó la suspensión publicada")
	assertConsumedEventStatus(t, db, suspend.EventID, "processed")
	if n := activeRefreshTokens(t, db, user.UserID); n != 0 {
		t.Errorf("active refresh tokens = %d, want 0", n)
	}
	if statuses := auditStatuses(t, db, admin.RoutingKeyUserSuspended); len(statuses) != 2 || statuses[0] != "discarded_invalid_signature" {
		t.Errorf("audit statuses = %v, want [discarded_invalid_signature processed]", statuses)
	}

	reactivate := reactivatedEvent(user.UserID, 3)
	publishRaw(t, rabbitMQURL, admin.RoutingKeyUserReactivated, mustMarshal(t, reactivate))
	waitForCondition(t, 10*time.Second, func() bool {
		suspendedAt, version := suspensionStateQuiet(db, user.UserID)
		return !suspendedAt.Valid && version == 3
	}, "el consumer no aplicó la reactivación publicada")
}

func suspensionStateQuiet(db *sql.DB, userID string) (suspendedAt sql.NullTime, version int64) {
	_ = db.QueryRow(`SELECT suspended_at, suspension_version FROM users WHERE id = $1`, userID).Scan(&suspendedAt, &version)
	return suspendedAt, version
}
