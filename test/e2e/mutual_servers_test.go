package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/discordia-grupo01/events-contract/broker"
	"github.com/discordia-grupo01/events-contract/envelope"
	"github.com/discordia-grupo01/events-contract/membership"
	"github.com/discordia-grupo01/identify-service/internal/audit"
	"github.com/discordia-grupo01/identify-service/internal/events"
	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	serverAliceAndBob = "11111111-1111-1111-1111-111111111111"
	serverOnlyAlice   = "22222222-2222-2222-2222-222222222222"
)

// TestServerMembershipRepositoryEdgeCases prueba el Upsert (last-write-wins
// por occurred_at) y ListMutualServerIDs directamente contra Postgres real,
// sin pasar por RabbitMQ ni por el endpoint HTTP.
func TestServerMembershipRepositoryEdgeCases(t *testing.T) {
	db := setupTestDB(t)
	repo := repository.NewServerMembershipRepository(db)
	ctx := context.Background()

	baseTime := time.Date(2026, time.September, 13, 10, 0, 0, 0, time.UTC)

	t.Run("un join solo de un lado no genera servidor en común", func(t *testing.T) {
		if err := repo.Upsert(ctx, "user-alice", serverOnlyAlice, true, baseTime); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}

		mutualIDs, err := repo.ListMutualServerIDs(ctx, "user-alice", "user-bob")
		if err != nil {
			t.Fatalf("ListMutualServerIDs() error = %v", err)
		}
		if len(mutualIDs) != 0 {
			t.Fatalf("mutualIDs = %v, want empty", mutualIDs)
		}
	})

	t.Run("join de ambos lados genera intersección", func(t *testing.T) {
		if err := repo.Upsert(ctx, "user-alice", serverAliceAndBob, true, baseTime); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}
		if err := repo.Upsert(ctx, "user-bob", serverAliceAndBob, true, baseTime); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}

		mutualIDs, err := repo.ListMutualServerIDs(ctx, "user-alice", "user-bob")
		if err != nil {
			t.Fatalf("ListMutualServerIDs() error = %v", err)
		}
		if len(mutualIDs) != 1 || mutualIDs[0] != serverAliceAndBob {
			t.Fatalf("mutualIDs = %v, want [%s]", mutualIDs, serverAliceAndBob)
		}
	})

	t.Run("join duplicado (mismo evento reentregado) es idempotente", func(t *testing.T) {
		serverID := "33333333-3333-3333-3333-333333333333"
		if err := repo.Upsert(ctx, "user-carol", serverID, true, baseTime); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}
		if err := repo.Upsert(ctx, "user-carol", serverID, true, baseTime); err != nil {
			t.Fatalf("Upsert() (duplicado) error = %v", err)
		}
		if err := repo.Upsert(ctx, "user-dave", serverID, true, baseTime); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}

		mutualIDs, err := repo.ListMutualServerIDs(ctx, "user-carol", "user-dave")
		if err != nil {
			t.Fatalf("ListMutualServerIDs() error = %v", err)
		}
		if len(mutualIDs) != 1 {
			t.Fatalf("mutualIDs = %v, want exactamente 1 (sin duplicar)", mutualIDs)
		}
	})

	t.Run("un evento desordenado (más viejo) no revierte un estado más nuevo", func(t *testing.T) {
		serverID := "44444444-4444-4444-4444-444444444444"
		newer := baseTime.Add(time.Hour)
		older := baseTime

		// El join "nuevo" ya se procesó; ahora llega tarde un leave "viejo"
		// (occurred_at anterior al estado ya guardado) - debe descartarse.
		if err := repo.Upsert(ctx, "user-erin", serverID, true, newer); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}
		if err := repo.Upsert(ctx, "user-erin", serverID, false, older); err != nil {
			t.Fatalf("Upsert() (evento viejo) error = %v", err)
		}
		if err := repo.Upsert(ctx, "user-frank", serverID, true, baseTime); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}

		mutualIDs, err := repo.ListMutualServerIDs(ctx, "user-erin", "user-frank")
		if err != nil {
			t.Fatalf("ListMutualServerIDs() error = %v", err)
		}
		if len(mutualIDs) != 1 {
			t.Fatalf("mutualIDs = %v, want [%s] (el leave viejo no debió aplicarse)", mutualIDs, serverID)
		}
	})

	t.Run("leave sin join previo no rompe, solo no genera membership activa", func(t *testing.T) {
		serverID := "55555555-5555-5555-5555-555555555555"
		if err := repo.Upsert(ctx, "user-grace", serverID, false, baseTime); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}
		if err := repo.Upsert(ctx, "user-heidi", serverID, true, baseTime); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}

		mutualIDs, err := repo.ListMutualServerIDs(ctx, "user-grace", "user-heidi")
		if err != nil {
			t.Fatalf("ListMutualServerIDs() error = %v", err)
		}
		if len(mutualIDs) != 0 {
			t.Fatalf("mutualIDs = %v, want empty (grace nunca estuvo activo)", mutualIDs)
		}
	})

	t.Run("un leave posterior revierte un join anterior", func(t *testing.T) {
		serverID := "66666666-6666-6666-6666-666666666666"
		joinedAt := baseTime
		leftAt := baseTime.Add(time.Minute)

		if err := repo.Upsert(ctx, "user-ivan", serverID, true, joinedAt); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}
		if err := repo.Upsert(ctx, "user-judy", serverID, true, joinedAt); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}
		if err := repo.Upsert(ctx, "user-ivan", serverID, false, leftAt); err != nil {
			t.Fatalf("Upsert() (leave) error = %v", err)
		}

		mutualIDs, err := repo.ListMutualServerIDs(ctx, "user-ivan", "user-judy")
		if err != nil {
			t.Fatalf("ListMutualServerIDs() error = %v", err)
		}
		if len(mutualIDs) != 0 {
			t.Fatalf("mutualIDs = %v, want empty (ivan abandonó el servidor)", mutualIDs)
		}
	})
}

// TestMutualServersEndToEnd cubre el resto de HU 36 (servidores en común) vía
// el endpoint GET /v1/users/{id}, insertando membresías directo con el
// repositorio (simulando que discordia-servers ya publicó y este servicio ya
// los procesó), sin depender de que RabbitMQ esté levantado.
func TestMutualServersEndToEnd(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	membershipRepo := repository.NewServerMembershipRepository(db)
	ctx := context.Background()
	now := time.Now().UTC()

	aliceID, aliceToken := registerAndLogin(t, router, "Alice", "alice@example.com", "Passw0rd!")
	bobID, bobToken := registerAndLogin(t, router, "Bob", "bob@example.com", "Passw0rd!")

	t.Run("devuelve los servidores en común entre dos usuarios", func(t *testing.T) {
		if err := membershipRepo.Upsert(ctx, aliceID, serverAliceAndBob, true, now); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}
		if err := membershipRepo.Upsert(ctx, bobID, serverAliceAndBob, true, now); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}
		if err := membershipRepo.Upsert(ctx, aliceID, serverOnlyAlice, true, now); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}

		recorder := doRequest(router, http.MethodGet, "/v1/users/"+bobID, nil, aliceToken)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		var publicProfile model.PublicUserResponse
		decodeJSON(t, recorder, &publicProfile)
		if len(publicProfile.MutualServerIDs) != 1 || publicProfile.MutualServerIDs[0] != serverAliceAndBob {
			t.Fatalf("MutualServerIDs = %v, want [%s]", publicProfile.MutualServerIDs, serverAliceAndBob)
		}
	})

	t.Run("devuelve array vacío sin servidores en común", func(t *testing.T) {
		_, carolToken := registerAndLogin(t, router, "Carol", "carol@example.com", "Passw0rd!")

		recorder := doRequest(router, http.MethodGet, "/v1/users/"+bobID, nil, carolToken)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
		}

		var rawBody map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &rawBody); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		mutualIDs, ok := rawBody["mutual_server_ids"].([]any)
		if !ok {
			t.Fatalf("mutual_server_ids ausente o no es un array: %v", rawBody["mutual_server_ids"])
		}
		if len(mutualIDs) != 0 {
			t.Fatalf("mutual_server_ids = %v, want []", mutualIDs)
		}
	})

	t.Run("devuelve array vacío cuando viewer == target", func(t *testing.T) {
		recorder := doRequest(router, http.MethodGet, "/v1/users/"+bobID, nil, bobToken)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		var publicProfile model.PublicUserResponse
		decodeJSON(t, recorder, &publicProfile)
		if len(publicProfile.MutualServerIDs) != 0 {
			t.Fatalf("MutualServerIDs = %v, want [] cuando viewer == target", publicProfile.MutualServerIDs)
		}
	})
}

// TestMutualServersRabbitMQPipeline publica eventos directo al exchange
// discordia.events (simulando a discordia-servers, sin que ese repo esté
// corriendo) y verifica que el Consumer real de este servicio los procese y
// se reflejen en el endpoint. Se saltea si no hay RABBITMQ_URL, mismo
// criterio que DATABASE_HOST para los demás tests e2e.
func TestMutualServersRabbitMQPipeline(t *testing.T) {
	rabbitMQURL := os.Getenv("RABBITMQ_URL")
	if rabbitMQURL == "" {
		t.Skip("RABBITMQ_URL no está seteado: este test e2e requiere un RabbitMQ real")
	}

	db := setupTestDB(t)
	router := newTestRouter(t, db)
	membershipRepo := repository.NewServerMembershipRepository(db)

	aliceID, aliceToken := registerAndLogin(t, router, "Alice", "alice@example.com", "Passw0rd!")
	bobID, _ := registerAndLogin(t, router, "Bob", "bob@example.com", "Passw0rd!")

	// Declara la topología (y purga la cola) antes de publicar: un topic
	// exchange descarta silenciosamente lo publicado si todavía no hay una
	// cola bindeada, así que publicar antes de que el consumer llegue a
	// declararla perdería el mensaje para siempre.
	declareAndPurgeMembershipQueue(t, rabbitMQURL)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	consumer := events.NewConsumer(rabbitMQURL, events.NewMembershipHandler(membershipRepo), audit.NewRecorder(db))
	go consumer.Start(ctx)

	publishMembershipEvent(t, rabbitMQURL, membership.RoutingKeyMemberJoined, membership.MemberJoined{
		Meta: envelope.Meta{EventID: "77777777-7777-7777-7777-777777777777", OccurredAt: time.Now().UTC()}, ServerID: serverAliceAndBob, UserID: aliceID,
	})
	publishMembershipEvent(t, rabbitMQURL, membership.RoutingKeyMemberJoined, membership.MemberJoined{
		Meta: envelope.Meta{EventID: "88888888-8888-8888-8888-888888888888", OccurredAt: time.Now().UTC()}, ServerID: serverAliceAndBob, UserID: bobID,
	})

	waitForCondition(t, 10*time.Second, func() bool {
		recorder := doRequest(router, http.MethodGet, "/v1/users/"+bobID, nil, aliceToken)
		if recorder.Code != http.StatusOK {
			return false
		}
		var publicProfile model.PublicUserResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &publicProfile); err != nil {
			return false
		}
		return len(publicProfile.MutualServerIDs) == 1 && publicProfile.MutualServerIDs[0] == serverAliceAndBob
	}, "el consumer no reflejó los eventos publicados en el endpoint")

	// La auditoría (consumed_events) debe reflejar ambos MemberJoined como
	// procesados -- es lo que permite comparar, del otro lado, contra
	// outbox_events de servers y detectar un evento que nunca llegó.
	assertConsumedEventStatus(t, db, "77777777-7777-7777-7777-777777777777", "processed")
	assertConsumedEventStatus(t, db, "88888888-8888-8888-8888-888888888888", "processed")

	publishMembershipEvent(t, rabbitMQURL, membership.RoutingKeyMemberLeft, membership.MemberJoined{
		Meta: envelope.Meta{EventID: "99999999-9999-9999-9999-999999999999", OccurredAt: time.Now().UTC()}, ServerID: serverAliceAndBob, UserID: aliceID,
	})

	waitForCondition(t, 10*time.Second, func() bool {
		recorder := doRequest(router, http.MethodGet, "/v1/users/"+bobID, nil, aliceToken)
		if recorder.Code != http.StatusOK {
			return false
		}
		var publicProfile model.PublicUserResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &publicProfile); err != nil {
			return false
		}
		return len(publicProfile.MutualServerIDs) == 0
	}, "el consumer no reflejó el member_left en el endpoint")
}

// TestConsumerReconnectsAfterConnectionDrop fuerza el cierre de la conexión
// AMQP real del consumer (vía la API de management de RabbitMQ, simulando
// una caída de red o un restart del broker) y verifica que Consumer.Start
// se reconecta solo y retoma el consumo, sin necesitar reiniciar el proceso.
// Se saltea si no hay RABBITMQ_URL, mismo criterio que los demás tests e2e
// que dependen de un RabbitMQ real.
func TestConsumerReconnectsAfterConnectionDrop(t *testing.T) {
	rabbitMQURL := os.Getenv("RABBITMQ_URL")
	if rabbitMQURL == "" {
		t.Skip("RABBITMQ_URL no está seteado: este test e2e requiere un RabbitMQ real")
	}

	db := setupTestDB(t)
	membershipRepo := repository.NewServerMembershipRepository(db)
	managementURL := managementAPIBaseURL(t, rabbitMQURL)

	declareAndPurgeMembershipQueue(t, rabbitMQURL)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	consumer := events.NewConsumer(rabbitMQURL, events.NewMembershipHandler(membershipRepo), audit.NewRecorder(db))
	go consumer.Start(ctx)

	serverID := "66666666-1111-2222-3333-444444444444"

	publishMembershipEvent(t, rabbitMQURL, membership.RoutingKeyMemberJoined, membership.MemberJoined{
		Meta: envelope.Meta{EventID: "aaaaaaaa-1111-1111-1111-111111111111", OccurredAt: time.Now().UTC()}, ServerID: serverID, UserID: "user-before-drop",
	})
	waitForCondition(t, 10*time.Second, func() bool {
		return membershipIsActive(t, membershipRepo, "user-before-drop", serverID)
	}, "el consumer no procesó el evento previo a la caída de conexión")

	closeConsumerConnection(t, managementURL, connectionNameForTest)

	// Publicar inmediatamente después de cerrar la conexión: si Start() no
	// reconectara, este evento se quedaría en la cola sin consumir para
	// siempre y el waitForCondition de abajo fallaría por timeout.
	publishMembershipEvent(t, rabbitMQURL, membership.RoutingKeyMemberJoined, membership.MemberJoined{
		Meta: envelope.Meta{EventID: "bbbbbbbb-2222-2222-2222-222222222222", OccurredAt: time.Now().UTC()}, ServerID: serverID, UserID: "user-after-drop",
	})
	waitForCondition(t, 15*time.Second, func() bool {
		return membershipIsActive(t, membershipRepo, "user-after-drop", serverID)
	}, "el consumer no se reconectó (o no retomó el consumo) tras la caída de conexión")
}

func membershipIsActive(t *testing.T, membershipRepo repository.ServerMembershipRepository, userID string, serverID string) bool {
	t.Helper()
	mutualIDs, err := membershipRepo.ListMutualServerIDs(context.Background(), userID, userID)
	if err != nil {
		t.Fatalf("ListMutualServerIDs() error = %v", err)
	}
	// ListMutualServerIDs compara dos usuarios; contra sí mismo funciona
	// igual (es solo un JOIN por server_id) y sirve para chequear "¿está
	// userID activo en serverID?" sin agregar un método nuevo al repo solo
	// para este test.
	for _, id := range mutualIDs {
		if id == serverID {
			return true
		}
	}
	return false
}

// assertConsumedEventStatus verifica que el consumer haya dejado un registro
// de auditoría para eventID con el status esperado -- consumed_events es la
// contraparte, del lado consumidor, de outbox_events en servers.
func assertConsumedEventStatus(t *testing.T, db *sql.DB, eventID, wantStatus string) {
	t.Helper()
	var gotStatus string
	if err := db.QueryRow(`SELECT status FROM consumed_events WHERE event_id = $1`, eventID).Scan(&gotStatus); err != nil {
		t.Fatalf("consumed_events row for event_id=%s: %v", eventID, err)
	}
	if gotStatus != wantStatus {
		t.Errorf("consumed_events.status for event_id=%s = %q, want %q", eventID, gotStatus, wantStatus)
	}
}

// connectionNameForTest debe coincidir con el nombre de conexión que
// Consumer usa internamente (ver internal/events/consumer.go), para poder
// encontrarla en la API de management y cerrarla a propósito.
const connectionNameForTest = "discordia-identify-server-memberships"

func managementAPIBaseURL(t *testing.T, rabbitMQURL string) string {
	t.Helper()
	parsed, err := url.Parse(rabbitMQURL)
	if err != nil {
		t.Fatalf("url.Parse(%q) error = %v", rabbitMQURL, err)
	}
	host := parsed.Hostname()
	user := "guest"
	pass := "guest"
	if parsed.User != nil {
		user = parsed.User.Username()
		if p, ok := parsed.User.Password(); ok {
			pass = p
		}
	}
	return fmt.Sprintf("http://%s:%s@%s:15672", user, pass, host)
}

// closeConsumerConnection busca, en la API de management de RabbitMQ, la
// conexión AMQP abierta con el nombre dado y la cierra a la fuerza (DELETE),
// simulando una caída de red o un restart del broker sin tener que tirar
// abajo el contenedor entero.
func closeConsumerConnection(t *testing.T, managementURL string, connectionName string) {
	t.Helper()

	var connectionID string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		connections := listManagementConnections(t, managementURL)
		for _, conn := range connections {
			clientProperties, _ := conn["client_properties"].(map[string]any)
			if name, _ := clientProperties["connection_name"].(string); name == connectionName {
				connectionID, _ = conn["name"].(string)
				break
			}
		}
		if connectionID != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if connectionID == "" {
		t.Fatalf("no se encontró en RabbitMQ management una conexión abierta llamada %q", connectionName)
	}

	request, err := http.NewRequest(http.MethodDelete, managementURL+"/api/connections/"+url.PathEscape(connectionID), nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("cerrar conexión %q: %v", connectionID, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /api/connections/%s status = %d, want %d", connectionID, response.StatusCode, http.StatusNoContent)
	}
}

func listManagementConnections(t *testing.T, managementURL string) []map[string]any {
	t.Helper()
	response, err := http.Get(managementURL + "/api/connections")
	if err != nil {
		t.Fatalf("GET /api/connections error = %v", err)
	}
	defer response.Body.Close()

	var connections []map[string]any
	if err := json.NewDecoder(response.Body).Decode(&connections); err != nil {
		t.Fatalf("decode /api/connections: %v", err)
	}
	return connections
}

func declareAndPurgeMembershipQueue(t *testing.T, rabbitMQURL string) {
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
	queue, err := ch.QueueDeclare(events.QueueName, true, false, false, false, nil)
	if err != nil {
		t.Fatalf("QueueDeclare() error = %v", err)
	}
	for _, routingKey := range []string{membership.RoutingKeyMemberJoined, membership.RoutingKeyMemberLeft} {
		if err := ch.QueueBind(queue.Name, routingKey, broker.ExchangeName, false, nil); err != nil {
			t.Fatalf("QueueBind() error = %v", err)
		}
	}
	if _, err := ch.QueuePurge(queue.Name, false); err != nil {
		t.Fatalf("QueuePurge() error = %v", err)
	}
}

func publishMembershipEvent(t *testing.T, rabbitMQURL string, routingKey string, payload membership.MemberJoined) {
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

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	publishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ch.PublishWithContext(publishCtx, broker.ExchangeName, routingKey, false, false, amqp.Publishing{
		ContentType: "application/json",
		Body:        body,
	}); err != nil {
		t.Fatalf("PublishWithContext() error = %v", err)
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, condition func() bool, failureMessage string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal(failureMessage)
}
