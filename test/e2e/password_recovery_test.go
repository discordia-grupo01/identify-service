package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/discordia-grupo01/identify-service/internal/model"
)

func TestPasswordRecoveryEndToEnd(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	registerAndLoginSession(t, router, "Dana", "dana@example.com", "OldPassw0rd!")

	recoverBody, _ := json.Marshal(model.PasswordRecoveryRequest{Email: "dana@example.com"})
	recoverRecorder := doRequest(router, http.MethodPost, "/v1/password-recovery", recoverBody, "")
	if recoverRecorder.Code != http.StatusAccepted {
		t.Fatalf("recover status = %d, body = %s", recoverRecorder.Code, recoverRecorder.Body.String())
	}

	sent, ok := passwordRecoveryMailerFor(t, router).latest("dana@example.com")
	if !ok {
		t.Fatal("password recovery request did not send an email")
	}
	token := resetToken(t, sent)

	resetBody, _ := json.Marshal(model.PasswordResetRequest{
		Token:           token,
		NewPassword:     "NewPassw0rd!",
		ConfirmPassword: "NewPassw0rd!",
	})
	resetRecorder := doRequest(router, http.MethodPost, "/v1/password-reset", resetBody, "")
	if resetRecorder.Code != http.StatusOK {
		t.Fatalf("reset status = %d, body = %s", resetRecorder.Code, resetRecorder.Body.String())
	}

	// La contraseña vieja ya no sirve.
	oldLoginBody, _ := json.Marshal(model.LoginRequest{Email: "dana@example.com", Password: "OldPassw0rd!"})
	oldLoginRecorder := doRequest(router, http.MethodPost, "/v1/login", oldLoginBody, "")
	if oldLoginRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("login with old password status = %d, want %d", oldLoginRecorder.Code, http.StatusUnauthorized)
	}

	// La nueva contraseña anda.
	newLoginBody, _ := json.Marshal(model.LoginRequest{Email: "dana@example.com", Password: "NewPassw0rd!"})
	newLoginRecorder := doRequest(router, http.MethodPost, "/v1/login", newLoginBody, "")
	if newLoginRecorder.Code != http.StatusOK {
		t.Fatalf("login with new password status = %d, body = %s", newLoginRecorder.Code, newLoginRecorder.Body.String())
	}
}

func TestPasswordRecovery_UnknownEmail_StillAccepted(t *testing.T) {
	// El endpoint no debe filtrar qué emails existen: un email nunca
	// registrado responde igual que uno real, solo que no se manda nada.
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	recoverBody, _ := json.Marshal(model.PasswordRecoveryRequest{Email: "nobody@example.com"})
	recoverRecorder := doRequest(router, http.MethodPost, "/v1/password-recovery", recoverBody, "")
	if recoverRecorder.Code != http.StatusAccepted {
		t.Fatalf("recover status = %d, want %d", recoverRecorder.Code, http.StatusAccepted)
	}

	if _, ok := passwordRecoveryMailerFor(t, router).latest("nobody@example.com"); ok {
		t.Fatal("an email was sent for an email that was never registered")
	}
}

func TestPasswordReset_InvalidToken_BadRequest(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	resetBody, _ := json.Marshal(model.PasswordResetRequest{
		Token:           "not-a-real-token",
		NewPassword:     "NewPassw0rd!",
		ConfirmPassword: "NewPassw0rd!",
	})
	resetRecorder := doRequest(router, http.MethodPost, "/v1/password-reset", resetBody, "")
	if resetRecorder.Code != http.StatusBadRequest {
		t.Fatalf("reset with invalid token status = %d, want %d", resetRecorder.Code, http.StatusBadRequest)
	}
}

func TestPasswordReset_PasswordsDoNotMatch_BadRequest(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	registerAndLoginSession(t, router, "Dana", "dana@example.com", "OldPassw0rd!")
	doRequest(router, http.MethodPost, "/v1/password-recovery", mustJSON(t, model.PasswordRecoveryRequest{Email: "dana@example.com"}), "")
	sent, ok := passwordRecoveryMailerFor(t, router).latest("dana@example.com")
	if !ok {
		t.Fatal("password recovery request did not send an email")
	}
	token := resetToken(t, sent)

	resetRecorder := doRequest(router, http.MethodPost, "/v1/password-reset", mustJSON(t, model.PasswordResetRequest{
		Token:           token,
		NewPassword:     "NewPassw0rd!",
		ConfirmPassword: "Mismatched!",
	}), "")
	if resetRecorder.Code != http.StatusBadRequest {
		t.Fatalf("reset with mismatched passwords status = %d, want %d", resetRecorder.Code, http.StatusBadRequest)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return body
}
