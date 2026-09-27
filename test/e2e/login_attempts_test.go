package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/gin-gonic/gin"
)

// loginFromIP posts to /v1/login pretending the request came from ip, via
// X-Forwarded-For -- Gin's default (no SetTrustedProxies call, see the
// "trusted all proxies" warning at boot) honors it, and it's the only way to
// simulate two different clients within a single in-process test.
func loginFromIP(router *gin.Engine, email, password, ip string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(model.LoginRequest{Email: email, Password: password})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-For", ip)
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestLogin_BlocksAfterTooManyFailedAttemptsFromSameIP(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	registerAndLoginSession(t, router, "Ada", "ada-attempts@example.com", "Passw0rd!")

	const attackerIP = "203.0.113.10"
	const maxAttempts = 5

	for i := 0; i < maxAttempts; i++ {
		recorder := loginFromIP(router, "ada-attempts@example.com", "wrong-password", attackerIP)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want %d", i+1, recorder.Code, http.StatusUnauthorized)
		}
	}

	// El limite ya se alcanzo: ni siquiera con la contrasena correcta entra,
	// porque el chequeo de intentos corre antes de comparar el hash.
	blockedRecorder := loginFromIP(router, "ada-attempts@example.com", "Passw0rd!", attackerIP)
	if blockedRecorder.Code != http.StatusTooManyRequests {
		t.Fatalf("login after exceeding attempts, status = %d, want %d, body = %s",
			blockedRecorder.Code, http.StatusTooManyRequests, blockedRecorder.Body.String())
	}

	// La dueña real, desde otra ip, entra sin problema: el bloqueo es por
	// (email, ip), no por cuenta -- nadie puede sacarla de su propia cuenta
	// solo por saber su email.
	ownerRecorder := loginFromIP(router, "ada-attempts@example.com", "Passw0rd!", "198.51.100.20")
	if ownerRecorder.Code != http.StatusOK {
		t.Fatalf("owner login from a different IP, status = %d, want %d, body = %s",
			ownerRecorder.Code, http.StatusOK, ownerRecorder.Body.String())
	}
}

func TestLogin_SuccessfulLoginResetsTheFailureCounter(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	registerAndLoginSession(t, router, "Bea", "bea-attempts@example.com", "Passw0rd!")

	const ip = "203.0.113.20"
	const maxAttempts = 5

	for i := 0; i < maxAttempts-1; i++ {
		loginFromIP(router, "bea-attempts@example.com", "wrong-password", ip)
	}

	successRecorder := loginFromIP(router, "bea-attempts@example.com", "Passw0rd!", ip)
	if successRecorder.Code != http.StatusOK {
		t.Fatalf("login with correct password, status = %d, want %d, body = %s",
			successRecorder.Code, http.StatusOK, successRecorder.Body.String())
	}

	// Si el contador no se hubiera reiniciado, este intento (el (maxAttempts)-esimo
	// contando los de antes) ya estaria bloqueado.
	afterSuccessRecorder := loginFromIP(router, "bea-attempts@example.com", "wrong-password", ip)
	if afterSuccessRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("attempt right after a successful login, status = %d, want %d (counter should have reset)",
			afterSuccessRecorder.Code, http.StatusUnauthorized)
	}
}
