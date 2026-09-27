package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/utils/googleoauth"
)

func googleLoginRequestBody(idToken string) []byte {
	body, _ := json.Marshal(map[string]string{"id_token": idToken})
	return body
}

func TestGoogleOAuth_NewAccount_CreatesAndLogsIn(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	googleVerifierFor(t, router).claimsByToken["valid-token"] = &googleoauth.Claims{
		Subject:       "google-subject-1",
		Email:         "bob@example.com",
		EmailVerified: true,
		Name:          "Bob",
	}

	recorder := doRequest(router, http.MethodPost, "/v1/oauth/google", googleLoginRequestBody("valid-token"), "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("oauth login status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var auth model.AuthResponse
	decodeJSON(t, recorder, &auth)
	if auth.Token == "" {
		t.Fatal("oauth login response did not contain an access token")
	}
	if auth.User.Email != "bob@example.com" {
		t.Errorf("auth.User.Email = %q, want bob@example.com", auth.User.Email)
	}
	if !auth.User.EmailVerified {
		t.Error("a Google-created account should start email-verified")
	}
}

// TestGoogleOAuth_ExistingEmail_LinksAccount covers CA2: a user who already
// registered with email/password logs in with Google using the same email,
// and it resolves to the SAME account instead of erroring or duplicating it.
func TestGoogleOAuth_ExistingEmail_LinksAccount(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	session := registerAndLoginSession(t, router, "Carol", "carol@example.com", "Passw0rd!")

	googleVerifierFor(t, router).claimsByToken["carol-token"] = &googleoauth.Claims{
		Subject:       "google-subject-carol",
		Email:         "carol@example.com",
		EmailVerified: true,
		Name:          "Carol",
	}

	recorder := doRequest(router, http.MethodPost, "/v1/oauth/google", googleLoginRequestBody("carol-token"), "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("oauth login status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var auth model.AuthResponse
	decodeJSON(t, recorder, &auth)
	if auth.User.ID != session.UserID {
		t.Errorf("auth.User.ID = %q, want the same account %q", auth.User.ID, session.UserID)
	}
}

func TestGoogleOAuth_InvalidToken_Unauthorized(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	recorder := doRequest(router, http.MethodPost, "/v1/oauth/google", googleLoginRequestBody("never-registered"), "")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestGoogleOAuth_MissingIDToken_BadRequest(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	recorder := doRequest(router, http.MethodPost, "/v1/oauth/google", []byte(`{}`), "")
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("missing id_token status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}
