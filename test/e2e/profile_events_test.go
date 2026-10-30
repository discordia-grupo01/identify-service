package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/discordia-grupo01/events-contract/users"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/discordia-grupo01/identify-service/internal/events"
	"github.com/discordia-grupo01/identify-service/internal/outbox"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/utils/googleoauth"
)

// profileEvents devuelve los users.profile_updated de userID que quedaron en
// la outbox, del más viejo al más nuevo, y además el payload crudo de cada uno
// (para verificar las claves exactas).
func profileEvents(t *testing.T, db *sql.DB, userID string) ([]users.ProfileUpdated, []map[string]any) {
	t.Helper()
	rows, err := db.Query(`
		SELECT payload FROM outbox_events
		 WHERE routing_key = $1 AND payload->>'user_id' = $2
		 ORDER BY created_at, (payload->>'version')::bigint`, users.RoutingKeyProfileUpdated, userID)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	defer rows.Close()

	var evts []users.ProfileUpdated
	var raws []map[string]any
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			t.Fatalf("scan outbox: %v", err)
		}
		var evt users.ProfileUpdated
		var raw map[string]any
		if err := json.Unmarshal(body, &evt); err != nil {
			t.Fatalf("decode event %s: %v", body, err)
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			t.Fatalf("decode event %s: %v", body, err)
		}
		evts = append(evts, evt)
		raws = append(raws, raw)
	}
	return evts, raws
}

func profileVersion(t *testing.T, db *sql.DB, userID string) int64 {
	t.Helper()
	var v int64
	if err := db.QueryRow(`SELECT profile_version FROM users WHERE id = $1`, userID).Scan(&v); err != nil {
		t.Fatalf("read profile_version: %v", err)
	}
	return v
}

func lastEvent(t *testing.T, db *sql.DB, userID string) users.ProfileUpdated {
	t.Helper()
	evts, _ := profileEvents(t, db, userID)
	if len(evts) == 0 {
		t.Fatalf("no users.profile_updated for %s", userID)
	}
	return evts[len(evts)-1]
}

func patchProfileForm(router *gin.Engine, fields map[string]string, token string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for k, v := range fields {
		_ = form.WriteField(k, v)
	}
	_ = form.Close()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/v1/me/profile", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(recorder, request)
	return recorder
}

var profileEventKeys = []string{"event_id", "occurred_at", "user_id", "version", "name", "avatar_url", "description", "status_text", "status_emoji"}

// Cada escritura del perfil público deja en la outbox, en la misma
// transacción, la foto completa con una versión creciente; confirmar el email
// no toca el perfil público y no genera evento.
func TestProfileEvents_EveryProfileWriteLeavesAVersionedSnapshot(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	// Registro + confirmación de email + login.
	userID, token := registerAndLogin(t, router, "Alice", "alice@example.com", "Passw0rd!")

	evts, raws := profileEvents(t, db, userID)
	if len(evts) != 1 {
		t.Fatalf("after register: %d events, want 1 (email confirmation must not publish)", len(evts))
	}
	if evts[0].Version != 1 || evts[0].Name != "Alice" || evts[0].UserID != userID {
		t.Errorf("register event = %+v, want version 1 for Alice", evts[0])
	}
	if _, err := uuid.Parse(evts[0].EventID); err != nil || evts[0].OccurredAt.IsZero() {
		t.Errorf("register event meta = %+v, want uuid event_id and occurred_at", evts[0].Meta)
	}
	if len(raws[0]) != len(profileEventKeys) {
		t.Errorf("event keys = %v, want exactly %v", raws[0], profileEventKeys)
	}
	for _, k := range profileEventKeys {
		if _, ok := raws[0][k]; !ok {
			t.Errorf("event missing key %q", k)
		}
	}
	if _, ok := raws[0]["email"]; ok {
		t.Error("the event must never carry the email")
	}

	t.Run("PATCH /me/profile", func(t *testing.T) {
		recorder := patchProfileForm(router, map[string]string{"name": "Alice Nueva", "description": "Hola"}, token)
		if recorder.Code != http.StatusOK {
			t.Fatalf("PATCH status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		got := lastEvent(t, db, userID)
		if got.Version != 2 || got.Name != "Alice Nueva" || got.Description != "Hola" {
			t.Errorf("event = %+v, want version 2 with the new name and description", got)
		}
	})

	t.Run("PUT /me/status", func(t *testing.T) {
		recorder := doRequest(router, http.MethodPut, "/v1/me/status", []byte(`{"status_text":"Jugando","status_emoji":"🎮"}`), token)
		if recorder.Code != http.StatusOK {
			t.Fatalf("PUT status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		got := lastEvent(t, db, userID)
		if got.Version != 3 || got.StatusText != "Jugando" || got.StatusEmoji != "🎮" || got.Name != "Alice Nueva" {
			t.Errorf("event = %+v, want version 3, full snapshot with the status", got)
		}
	})

	t.Run("DELETE /me/status", func(t *testing.T) {
		recorder := doRequest(router, http.MethodDelete, "/v1/me/status", nil, token)
		if recorder.Code != http.StatusNoContent && recorder.Code != http.StatusOK {
			t.Fatalf("DELETE status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		got := lastEvent(t, db, userID)
		if got.Version != 4 || got.StatusText != "" || got.StatusEmoji != "" || got.Description != "Hola" {
			t.Errorf("event = %+v, want version 4 with the status cleared", got)
		}
	})

	if v := profileVersion(t, db, userID); v != 4 {
		t.Errorf("profile_version = %d, want 4", v)
	}
}

// Setear contraseña y verificar el email no son cambios del perfil público:
// no suben profile_version ni generan evento.
func TestProfileEvents_NonProfileWritesDoNotPublish(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	userID, _ := registerAndLogin(t, router, "Bob", "bob@example.com", "Passw0rd!")
	repo := repository.NewUserRepository(db)
	ctx := context.Background()

	if err := repo.UpdatePasswordHash(ctx, userID, "hash"); err != nil {
		t.Fatalf("UpdatePasswordHash: %v", err)
	}
	if err := repo.UpdateEmailVerification(ctx, userID, true); err != nil {
		t.Fatalf("UpdateEmailVerification: %v", err)
	}

	if evts, _ := profileEvents(t, db, userID); len(evts) != 1 {
		t.Errorf("events = %d, want only the register one", len(evts))
	}
	if v := profileVersion(t, db, userID); v != 1 {
		t.Errorf("profile_version = %d, want 1", v)
	}
}

// Editar un usuario que no existe no escribe ni publica nada.
func TestProfileEvents_UnknownUserPublishesNothing(t *testing.T) {
	db := setupTestDB(t)
	repo := repository.NewUserRepository(db)
	ctx := context.Background()
	ghost := uuid.NewString()

	if err := repo.UpdateProfile(ctx, ghost, "x", "", ""); err != nil {
		t.Fatalf("UpdateProfile(unknown) error = %v, want nil", err)
	}
	empty := ""
	if u, err := repo.UpdateStatus(ctx, ghost, &empty, &empty); err != nil || u != nil {
		t.Fatalf("UpdateStatus(unknown) = %v, %v; want nil, nil", u, err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM outbox_events`).Scan(&n); err != nil || n != 0 {
		t.Errorf("outbox rows = %d (err %v), want 0", n, err)
	}
}

// El alta por Google publica su primer evento con el avatar de Google (antes
// el INSERT no guardaba avatar_url y se perdía).
func TestProfileEvents_GoogleSignupKeepsAvatar(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	googleVerifierFor(t, router).claimsByToken["dave-token"] = &googleoauth.Claims{
		Subject:       "google-subject-dave",
		Email:         "dave@example.com",
		EmailVerified: true,
		Name:          "Dave",
		Picture:       "https://lh3.googleusercontent.com/dave.png",
	}

	recorder := doRequest(router, http.MethodPost, "/v1/oauth/google", googleLoginRequestBody("dave-token"), "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("oauth status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var userID string
	if err := db.QueryRow(`SELECT id FROM users WHERE email = 'dave@example.com'`).Scan(&userID); err != nil {
		t.Fatalf("read user: %v", err)
	}

	evts, _ := profileEvents(t, db, userID)
	if len(evts) != 1 || evts[0].Version != 1 || evts[0].Name != "Dave" || evts[0].AvatarURL != "https://lh3.googleusercontent.com/dave.png" {
		t.Fatalf("events = %+v, want one version-1 event with Google's avatar", evts)
	}

	profile := doRequest(router, http.MethodGet, "/v1/users/"+userID, nil, loginToken(t, db, router))
	if !strings.Contains(profile.Body.String(), "dave.png") {
		t.Errorf("public profile = %s, want Google's avatar stored", profile.Body.String())
	}
}

// loginToken registra un usuario auxiliar y devuelve su token, para mirar
// perfiles públicos de otros.
func loginToken(t *testing.T, _ *sql.DB, router *gin.Engine) string {
	t.Helper()
	_, token := registerAndLogin(t, router, "Viewer", "viewer-"+uuid.NewString()[:8]+"@example.com", "Passw0rd!")
	return token
}

// Si falla el insert en la outbox, la escritura entera hace rollback: el
// usuario no queda creado sin su evento.
func TestProfileEvents_OutboxFailureRollsBackTheWrite(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	if _, err := db.Exec(`ALTER TABLE outbox_events RENAME TO outbox_events_off`); err != nil {
		t.Fatalf("rename outbox: %v", err)
	}
	restored := false
	restore := func() {
		if !restored {
			restored = true
			if _, err := db.Exec(`ALTER TABLE outbox_events_off RENAME TO outbox_events`); err != nil {
				t.Errorf("restore outbox: %v", err)
			}
		}
	}
	t.Cleanup(restore)

	body, _ := json.Marshal(map[string]string{"name": "Eve", "email": "eve@example.com", "password": "Passw0rd!"})
	recorder := doRequest(router, http.MethodPost, "/v1/users", body, "")
	restore()

	if recorder.Code == http.StatusCreated {
		t.Fatalf("register status = 201, want a failure when the outbox insert fails")
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM users WHERE email = 'eve@example.com'`).Scan(&n); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if n != 0 {
		t.Errorf("users with eve's email = %d, want 0 (rollback)", n)
	}
}

type fakePublisher struct {
	err       error
	published []string
}

func (f *fakePublisher) Publish(_ context.Context, routingKey string, _ any) error {
	if f.err != nil {
		return f.err
	}
	f.published = append(f.published, routingKey)
	return nil
}

// El poller marca publicado lo que llegó al broker y deja pendiente, con el
// intento y el error, lo que falló.
func TestOutboxPoller_PublishesAndRetries(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	registerAndLogin(t, router, "Frank", "frank@example.com", "Passw0rd!")
	ctx := context.Background()

	failing := &fakePublisher{err: errors.New("broker down")}
	outbox.NewPoller(db, failing).Drain(ctx)
	// El alta deja tres eventos (profile_updated, account_registered y, al
	// confirmar el email, email_verified). El poller corta el lote en el
	// primer fallo: ese queda con el intento y el error, y los tres siguen
	// pendientes.
	var failed, pendingBefore int
	if err := db.QueryRow(`SELECT count(*) FILTER (WHERE attempts = 1 AND last_error = 'broker down'), count(*) FILTER (WHERE published_at IS NULL) FROM outbox_events`).Scan(&failed, &pendingBefore); err != nil || failed != 1 || pendingBefore != 3 {
		t.Errorf("after failure: %d events marked failed, %d pending (err %v); want 1 and 3", failed, pendingBefore, err)
	}

	ok := &fakePublisher{}
	outbox.NewPoller(db, ok).Drain(ctx)
	sort.Strings(ok.published)
	if want := []string{users.RoutingKeyAccountRegistered, users.RoutingKeyEmailVerified, users.RoutingKeyProfileUpdated}; !reflect.DeepEqual(ok.published, want) {
		t.Errorf("published = %v, want %v", ok.published, want)
	}
	var pending int
	if err := db.QueryRow(`SELECT count(*) FROM outbox_events WHERE published_at IS NULL`).Scan(&pending); err != nil || pending != 0 {
		t.Errorf("pending = %d (err %v), want 0", pending, err)
	}
}

// Poller + publisher real: el evento del registro llega a RabbitMQ con la
// forma del contrato.
func TestOutboxPoller_DeliversToRabbitMQ(t *testing.T) {
	rmqURL := os.Getenv("RABBITMQ_URL")
	if rmqURL == "" {
		t.Skip("RABBITMQ_URL no está seteado: este test necesita un RabbitMQ real")
	}
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	conn, err := amqp.Dial(rmqURL)
	if err != nil {
		t.Fatalf("amqp.Dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("conn.Channel: %v", err)
	}
	if err := ch.ExchangeDeclare("discordia.events", "topic", true, false, false, false, nil); err != nil {
		t.Fatalf("ExchangeDeclare: %v", err)
	}
	q, err := ch.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		t.Fatalf("QueueDeclare: %v", err)
	}
	if err := ch.QueueBind(q.Name, users.RoutingKeyProfileUpdated, "discordia.events", false, nil); err != nil {
		t.Fatalf("QueueBind: %v", err)
	}
	deliveries, err := ch.Consume(q.Name, "", true, true, false, false, nil)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}

	userID, _ := registerAndLogin(t, router, "Grace", "grace@example.com", "Passw0rd!")
	outbox.NewPoller(db, events.NewAMQPPublisher(rmqURL)).Drain(context.Background())

	select {
	case msg := <-deliveries:
		var got users.ProfileUpdated
		if err := json.Unmarshal(msg.Body, &got); err != nil {
			t.Fatalf("decode delivery: %v", err)
		}
		if msg.RoutingKey != users.RoutingKeyProfileUpdated || got.UserID != userID || got.Version != 1 || got.Name != "Grace" {
			t.Errorf("delivery %q = %+v, want Grace's version-1 profile", msg.RoutingKey, got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for users.profile_updated on RabbitMQ")
	}
}

// La migración de backfill deja exactamente un evento por usuario existente,
// con su profile_version actual y la forma del contrato.
func TestBackfillMigration_OneEventPerUserWithItsVersion(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	aliceID, aliceToken := registerAndLogin(t, router, "Alice", "alice@example.com", "Passw0rd!")
	bobID, _ := registerAndLogin(t, router, "Bob", "bob@example.com", "Passw0rd!")
	if rec := doRequest(router, http.MethodPut, "/v1/me/status", []byte(`{"status_text":"Estudiando"}`), aliceToken); rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d", rec.Code)
	}
	if _, err := db.Exec(`TRUNCATE outbox_events`); err != nil {
		t.Fatalf("truncate outbox: %v", err)
	}

	backfill, err := os.ReadFile("../../migrations/000015_backfill_profile_events.up.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := db.Exec(string(backfill)); err != nil {
		t.Fatalf("run backfill: %v", err)
	}

	var total int
	if err := db.QueryRow(`SELECT count(*) FROM outbox_events`).Scan(&total); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	var usersCount int
	if err := db.QueryRow(`SELECT count(*) FROM users`).Scan(&usersCount); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if total != usersCount {
		t.Errorf("backfill rows = %d, want one per user (%d)", total, usersCount)
	}

	alice, raws := profileEvents(t, db, aliceID)
	if len(alice) != 1 || alice[0].Version != 2 || alice[0].StatusText != "Estudiando" || alice[0].Name != "Alice" {
		t.Errorf("alice backfill = %+v, want one version-2 snapshot with her status", alice)
	}
	if len(raws) == 1 && len(raws[0]) != len(profileEventKeys) {
		t.Errorf("backfill event keys = %v, want exactly %v", raws[0], profileEventKeys)
	}
	if _, err := uuid.Parse(alice[0].EventID); err != nil || alice[0].OccurredAt.IsZero() {
		t.Errorf("backfill meta = %+v, want uuid event_id and a parseable occurred_at", alice[0].Meta)
	}
	if bob, _ := profileEvents(t, db, bobID); len(bob) != 1 || bob[0].Version != 1 {
		t.Errorf("bob backfill = %+v, want one version-1 snapshot", bob)
	}
}
