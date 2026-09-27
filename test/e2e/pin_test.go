package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/discordia-grupo01/identify-service/internal/model"
)

func TestDevicePin_ConfigureThenLoginEndToEnd(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	session := registerAndLoginSession(t, router, "Alice", "alice@example.com", "Passw0rd!")

	configureBody, _ := json.Marshal(model.ConfigurePinRequest{Pin: "123456", DeviceID: "device-1"})
	configureRecorder := doRequest(router, http.MethodPost, "/v1/pin", configureBody, session.AccessToken)
	if configureRecorder.Code != http.StatusNoContent {
		t.Fatalf("configure status = %d, body = %s", configureRecorder.Code, configureRecorder.Body.String())
	}

	loginBody, _ := json.Marshal(model.PinLoginRequest{Pin: "123456", DeviceID: "device-1"})
	loginRecorder := doRequest(router, http.MethodPost, "/v1/pin/login", loginBody, "")
	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("pin login status = %d, body = %s", loginRecorder.Code, loginRecorder.Body.String())
	}

	var auth model.AuthResponse
	decodeJSON(t, loginRecorder, &auth)
	if auth.Token == "" {
		t.Fatal("pin login response did not contain an access token")
	}
	if auth.User.ID != session.UserID {
		t.Errorf("auth.User.ID = %q, want %q", auth.User.ID, session.UserID)
	}
}

func TestDevicePin_Login_WrongPin_Unauthorized(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	session := registerAndLoginSession(t, router, "Alice", "alice@example.com", "Passw0rd!")

	configureBody, _ := json.Marshal(model.ConfigurePinRequest{Pin: "123456", DeviceID: "device-1"})
	doRequest(router, http.MethodPost, "/v1/pin", configureBody, session.AccessToken)

	loginBody, _ := json.Marshal(model.PinLoginRequest{Pin: "000000", DeviceID: "device-1"})
	loginRecorder := doRequest(router, http.MethodPost, "/v1/pin/login", loginBody, "")
	if loginRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-pin login status = %d, want %d", loginRecorder.Code, http.StatusUnauthorized)
	}
}

func TestDevicePin_Login_UnknownDevice_Unauthorized(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	loginBody, _ := json.Marshal(model.PinLoginRequest{Pin: "123456", DeviceID: "never-configured"})
	loginRecorder := doRequest(router, http.MethodPost, "/v1/pin/login", loginBody, "")
	if loginRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unknown-device login status = %d, want %d", loginRecorder.Code, http.StatusUnauthorized)
	}
}

func TestDevicePin_Configure_InvalidFormat_BadRequest(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	session := registerAndLoginSession(t, router, "Alice", "alice@example.com", "Passw0rd!")

	configureBody, _ := json.Marshal(model.ConfigurePinRequest{Pin: "12", DeviceID: "device-1"})
	configureRecorder := doRequest(router, http.MethodPost, "/v1/pin", configureBody, session.AccessToken)
	if configureRecorder.Code != http.StatusBadRequest {
		t.Fatalf("configure with short pin status = %d, want %d", configureRecorder.Code, http.StatusBadRequest)
	}
}

// --- CA3: bloqueo tras intentos fallidos ---

func TestDevicePin_LocksAfterTooManyFailedAttempts(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	session := registerAndLoginSession(t, router, "Ada", "ada-pin@example.com", "Passw0rd!")
	configureBody, _ := json.Marshal(model.ConfigurePinRequest{Pin: "123456", DeviceID: "device-1"})
	doRequest(router, http.MethodPost, "/v1/pin", configureBody, session.AccessToken)

	const maxAttempts = 5
	wrongBody, _ := json.Marshal(model.PinLoginRequest{Pin: "000000", DeviceID: "device-1"})
	for i := 0; i < maxAttempts-1; i++ {
		recorder := doRequest(router, http.MethodPost, "/v1/pin/login", wrongBody, "")
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want %d", i+1, recorder.Code, http.StatusUnauthorized)
		}
	}

	// El intento que llega al umbral bloquea (423), no solo rechaza (401).
	lockedRecorder := doRequest(router, http.MethodPost, "/v1/pin/login", wrongBody, "")
	if lockedRecorder.Code != http.StatusLocked {
		t.Fatalf("attempt %d (threshold): status = %d, want %d, body = %s",
			maxAttempts, lockedRecorder.Code, http.StatusLocked, lockedRecorder.Body.String())
	}
}

func TestDevicePin_Login_RejectsEvenTheCorrectPinWhileLocked(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	session := registerAndLoginSession(t, router, "Bea", "bea-pin@example.com", "Passw0rd!")
	configureBody, _ := json.Marshal(model.ConfigurePinRequest{Pin: "123456", DeviceID: "device-1"})
	doRequest(router, http.MethodPost, "/v1/pin", configureBody, session.AccessToken)

	const maxAttempts = 5
	wrongBody, _ := json.Marshal(model.PinLoginRequest{Pin: "000000", DeviceID: "device-1"})
	for i := 0; i < maxAttempts; i++ {
		doRequest(router, http.MethodPost, "/v1/pin/login", wrongBody, "")
	}

	correctBody, _ := json.Marshal(model.PinLoginRequest{Pin: "123456", DeviceID: "device-1"})
	recorder := doRequest(router, http.MethodPost, "/v1/pin/login", correctBody, "")
	if recorder.Code != http.StatusLocked {
		t.Fatalf("correct PIN while locked, status = %d, want %d, body = %s",
			recorder.Code, http.StatusLocked, recorder.Body.String())
	}

	loginBody, _ := json.Marshal(model.LoginRequest{Email: "bea-pin@example.com", Password: "Passw0rd!"})
	loginRecorder := doRequest(router, http.MethodPost, "/v1/login", loginBody, "")
	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("email+password login while PIN is locked, status = %d, want %d, body = %s",
			loginRecorder.Code, http.StatusOK, loginRecorder.Body.String())
	}
}

func TestDevicePin_SuccessfulLoginResetsTheFailureCounter(t *testing.T) {
	db := setupTestDB(t)
	router := newTestRouter(t, db)

	session := registerAndLoginSession(t, router, "Caro", "caro-pin@example.com", "Passw0rd!")
	configureBody, _ := json.Marshal(model.ConfigurePinRequest{Pin: "123456", DeviceID: "device-1"})
	doRequest(router, http.MethodPost, "/v1/pin", configureBody, session.AccessToken)

	const maxAttempts = 5
	wrongBody, _ := json.Marshal(model.PinLoginRequest{Pin: "000000", DeviceID: "device-1"})
	for i := 0; i < maxAttempts-1; i++ {
		doRequest(router, http.MethodPost, "/v1/pin/login", wrongBody, "")
	}

	correctBody, _ := json.Marshal(model.PinLoginRequest{Pin: "123456", DeviceID: "device-1"})
	successRecorder := doRequest(router, http.MethodPost, "/v1/pin/login", correctBody, "")
	if successRecorder.Code != http.StatusOK {
		t.Fatalf("login with correct PIN, status = %d, want %d, body = %s",
			successRecorder.Code, http.StatusOK, successRecorder.Body.String())
	}

	afterSuccessRecorder := doRequest(router, http.MethodPost, "/v1/pin/login", wrongBody, "")
	if afterSuccessRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("attempt right after a successful login, status = %d, want %d (counter should have reset)",
			afterSuccessRecorder.Code, http.StatusUnauthorized)
	}
}
