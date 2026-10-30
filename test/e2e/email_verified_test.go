package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/discordia-grupo01/events-contract/users"
	"github.com/google/uuid"

	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/utils/googleoauth"
)

var emailVerifiedKeys = []string{"event_id", "occurred_at", "user_id", "verified_at"}

// emailVerifiedEvents devuelve los users.email_verified de userID que quedaron
// en la outbox, con el payload crudo de cada uno.
func emailVerifiedEvents(t *testing.T, db *sql.DB, userID string) ([]users.EmailVerified, []map[string]any) {
	t.Helper()
	rows, err := db.Query(`
		SELECT payload FROM outbox_events
		 WHERE routing_key = $1 AND payload->>'user_id' = $2
		 ORDER BY created_at`, users.RoutingKeyEmailVerified, userID)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	defer rows.Close()

	var evts []users.EmailVerified
	var raws []map[string]any
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			t.Fatalf("scan outbox: %v", err)
		}
		var evt users.EmailVerified
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

func assertEmailVerifiedEvent(t *testing.T, db *sql.DB, userID string, evt users.EmailVerified, raw map[string]any) {
	t.Helper()
	if evt.UserID != userID {
		t.Errorf("event user = %s, want %s", evt.UserID, userID)
	}
	if _, err := uuid.Parse(evt.EventID); err != nil || evt.OccurredAt.IsZero() {
		t.Errorf("event meta = %+v, want uuid event_id and occurred_at", evt.Meta)
	}
	var verifiedAt time.Time
	if err := db.QueryRow(`SELECT verified_at FROM users WHERE id = $1`, userID).Scan(&verifiedAt); err != nil {
		t.Fatalf("read verified_at: %v", err)
	}
	if !evt.VerifiedAt.Equal(verifiedAt) {
		t.Errorf("event verified_at = %v, want the user's verified_at %v", evt.VerifiedAt, verifiedAt)
	}
	if len(raw) != len(emailVerifiedKeys) {
		t.Errorf("event keys = %v, want exactly %v", raw, emailVerifiedKeys)
	}
	for _, k := range emailVerifiedKeys {
		if _, ok := raw[k]; !ok {
			t.Errorf("event missing key %q", k)
		}
	}
}

// Confirmar el email (registerAndLogin pasa por /email-confirmation/confirm)
// deja un users.email_verified; volver a marcarlo no publica otro.
func TestEmailVerified_PublishedOnceOnConfirmation(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	userID, _ := registerAndLogin(t, router, "Judy", "judy@example.com", "Passw0rd!")

	repo := repository.NewUserRepository(db)
	if err := repo.UpdateEmailVerification(context.Background(), userID, true); err != nil {
		t.Fatalf("UpdateEmailVerification: %v", err)
	}

	evts, raws := emailVerifiedEvents(t, db, userID)
	if len(evts) != 1 {
		t.Fatalf("users.email_verified = %d, want 1", len(evts))
	}
	assertEmailVerifiedEvent(t, db, userID, evts[0], raws[0])
}

// El alta por Google nace verificada: lo dice users.account_registered y no
// publica users.email_verified.
func TestEmailVerified_GoogleSignupDoesNotPublish(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	googleVerifierFor(t, router).claimsByToken["ken-token"] = &googleoauth.Claims{
		Subject:       "google-subject-ken",
		Email:         "ken@example.com",
		EmailVerified: true,
		Name:          "Ken",
	}
	recorder := doRequest(router, http.MethodPost, "/v1/oauth/google", googleLoginRequestBody("ken-token"), "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("oauth status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var userID string
	if err := db.QueryRow(`SELECT id FROM users WHERE email = 'ken@example.com'`).Scan(&userID); err != nil {
		t.Fatalf("read user: %v", err)
	}

	if evts, _ := emailVerifiedEvents(t, db, userID); len(evts) != 0 {
		t.Errorf("users.email_verified = %d, want 0 for an OAuth signup", len(evts))
	}
}

// Si falla el insert del evento, la verificación hace rollback: el email
// sigue sin verificar y se puede volver a intentar.
func TestEmailVerified_InsertFailureRollsBackTheVerification(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	userID, _ := registerAndLogin(t, router, "Leo", "leo@example.com", "Passw0rd!")
	if _, err := db.Exec(`UPDATE users SET email_verified = FALSE, verified_at = NULL WHERE id = $1`, userID); err != nil {
		t.Fatalf("reset verification: %v", err)
	}
	if _, err := db.Exec(`
		CREATE OR REPLACE FUNCTION reject_email_verified() RETURNS trigger AS $$
		BEGIN RAISE EXCEPTION 'email_verified rechazado por el test'; END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER reject_email_verified BEFORE INSERT ON outbox_events
		FOR EACH ROW WHEN (NEW.routing_key = 'users.email_verified')
		EXECUTE FUNCTION reject_email_verified()`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP TRIGGER IF EXISTS reject_email_verified ON outbox_events; DROP FUNCTION IF EXISTS reject_email_verified()`)
	})

	repo := repository.NewUserRepository(db)
	if err := repo.UpdateEmailVerification(context.Background(), userID, true); err == nil {
		t.Fatal("UpdateEmailVerification error = nil, want the outbox failure")
	}

	var verified bool
	if err := db.QueryRow(`SELECT email_verified FROM users WHERE id = $1`, userID).Scan(&verified); err != nil {
		t.Fatalf("read email_verified: %v", err)
	}
	if verified {
		t.Error("email_verified = true, want false (rollback)")
	}
}

// La migración de backfill deja un users.email_verified por cada usuario
// verificado y ninguno para los pendientes.
func TestBackfillMigration_EmailVerifiedOnlyForVerifiedUsers(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	verifiedID, _ := registerAndLogin(t, router, "Mallory", "mallory@example.com", "Passw0rd!")
	pendingID, _ := registerAndLogin(t, router, "Niaj", "niaj@example.com", "Passw0rd!")
	if _, err := db.Exec(`
		UPDATE users SET email_verified = TRUE, verified_at = '2026-01-02T03:04:05Z' WHERE id = $1;
	`, verifiedID); err != nil {
		t.Fatalf("verify user: %v", err)
	}
	if _, err := db.Exec(`UPDATE users SET email_verified = FALSE, verified_at = NULL WHERE id = $1`, pendingID); err != nil {
		t.Fatalf("unverify user: %v", err)
	}
	if _, err := db.Exec(`TRUNCATE outbox_events`); err != nil {
		t.Fatalf("truncate outbox: %v", err)
	}

	backfill, err := os.ReadFile("../../migrations/000019_backfill_email_verified.up.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := db.Exec(string(backfill)); err != nil {
		t.Fatalf("run backfill: %v", err)
	}

	evts, raws := emailVerifiedEvents(t, db, verifiedID)
	if len(evts) != 1 {
		t.Fatalf("verified user backfill = %d events, want 1", len(evts))
	}
	assertEmailVerifiedEvent(t, db, verifiedID, evts[0], raws[0])
	if pending, _ := emailVerifiedEvents(t, db, pendingID); len(pending) != 0 {
		t.Errorf("pending user backfill = %d events, want 0", len(pending))
	}
}
