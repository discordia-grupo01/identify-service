package e2e

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/discordia-grupo01/events-contract/users"
	"github.com/google/uuid"

	"github.com/discordia-grupo01/identify-service/internal/utils/googleoauth"
)

var accountRegisteredKeys = []string{"event_id", "occurred_at", "user_id", "email", "name", "created_at", "email_verified"}

// accountRegisteredEvents devuelve los users.account_registered de userID que
// quedaron en la outbox, con el payload crudo de cada uno.
func accountRegisteredEvents(t *testing.T, db *sql.DB, userID string) ([]users.AccountRegistered, []map[string]any) {
	t.Helper()
	rows, err := db.Query(`
		SELECT payload FROM outbox_events
		 WHERE routing_key = $1 AND payload->>'user_id' = $2
		 ORDER BY created_at`, users.RoutingKeyAccountRegistered, userID)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	defer rows.Close()

	var evts []users.AccountRegistered
	var raws []map[string]any
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			t.Fatalf("scan outbox: %v", err)
		}
		var evt users.AccountRegistered
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

func assertAccountRegistered(t *testing.T, db *sql.DB, userID, wantEmail, wantName string, wantVerified bool) {
	t.Helper()
	evts, raws := accountRegisteredEvents(t, db, userID)
	if len(evts) != 1 {
		t.Fatalf("users.account_registered = %d, want 1", len(evts))
	}
	evt := evts[0]
	if evt.UserID != userID || evt.Email != wantEmail || evt.Name != wantName || evt.EmailVerified != wantVerified {
		t.Errorf("event = %+v, want user %s, email %s, name %s, email_verified %v", evt, userID, wantEmail, wantName, wantVerified)
	}
	if _, err := uuid.Parse(evt.EventID); err != nil || evt.OccurredAt.IsZero() {
		t.Errorf("event meta = %+v, want uuid event_id and occurred_at", evt.Meta)
	}
	var createdAt time.Time
	if err := db.QueryRow(`SELECT created_at FROM users WHERE id = $1`, userID).Scan(&createdAt); err != nil {
		t.Fatalf("read created_at: %v", err)
	}
	if !evt.CreatedAt.Equal(createdAt) {
		t.Errorf("event created_at = %v, want the user's created_at %v", evt.CreatedAt, createdAt)
	}
	if len(raws[0]) != len(accountRegisteredKeys) {
		t.Errorf("event keys = %v, want exactly %v", raws[0], accountRegisteredKeys)
	}
	for _, k := range accountRegisteredKeys {
		if _, ok := raws[0][k]; !ok {
			t.Errorf("event missing key %q", k)
		}
	}
	if raw, ok := raws[0]["created_at"].(string); !ok || raw[len(raw)-1] != 'Z' {
		t.Errorf("created_at = %v, want an RFC 3339 UTC timestamp", raws[0]["created_at"])
	}
}

// El registro con contraseña deja un users.account_registered con el email,
// además del users.profile_updated.
func TestAccountRegistered_PasswordSignup(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	userID, _ := registerAndLogin(t, router, "Grace", "grace@example.com", "Passw0rd!")

	assertAccountRegistered(t, db, userID, "grace@example.com", "Grace", false)
	if evts, _ := profileEvents(t, db, userID); len(evts) != 1 {
		t.Errorf("users.profile_updated = %d, want 1 alongside account_registered", len(evts))
	}
}

// El alta por Google también lo publica, una sola vez: volver a loguearse no
// lo repite.
func TestAccountRegistered_GoogleSignupPublishesOnce(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)
	googleVerifierFor(t, router).claimsByToken["heidi-token"] = &googleoauth.Claims{
		Subject:       "google-subject-heidi",
		Email:         "heidi@example.com",
		EmailVerified: true,
		Name:          "Heidi",
	}

	for i := 0; i < 2; i++ {
		recorder := doRequest(router, http.MethodPost, "/v1/oauth/google", googleLoginRequestBody("heidi-token"), "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("oauth status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
	}
	var userID string
	if err := db.QueryRow(`SELECT id FROM users WHERE email = 'heidi@example.com'`).Scan(&userID); err != nil {
		t.Fatalf("read user: %v", err)
	}

	assertAccountRegistered(t, db, userID, "heidi@example.com", "Heidi", true)
}

// Si falla solo el insert de users.account_registered, el alta entera hace
// rollback: no queda un usuario sin su aviso (ni su profile_updated).
func TestAccountRegistered_InsertFailureRollsBackTheSignup(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	if _, err := db.Exec(`
		CREATE OR REPLACE FUNCTION reject_account_registered() RETURNS trigger AS $$
		BEGIN RAISE EXCEPTION 'account_registered rechazado por el test'; END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER reject_account_registered BEFORE INSERT ON outbox_events
		FOR EACH ROW WHEN (NEW.routing_key = 'users.account_registered')
		EXECUTE FUNCTION reject_account_registered()`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP TRIGGER IF EXISTS reject_account_registered ON outbox_events; DROP FUNCTION IF EXISTS reject_account_registered()`)
	})

	body, _ := json.Marshal(map[string]string{"name": "Ivan", "email": "ivan@example.com", "password": "Passw0rd!"})
	recorder := doRequest(router, http.MethodPost, "/v1/users", body, "")
	if recorder.Code == http.StatusCreated {
		t.Fatalf("register status = 201, want a failure when the account_registered insert fails")
	}

	var users, events int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM outbox_events)`).Scan(&users, &events); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if users != 0 || events != 0 {
		t.Errorf("users = %d, outbox events = %d, want 0 and 0 (rollback)", users, events)
	}
}
