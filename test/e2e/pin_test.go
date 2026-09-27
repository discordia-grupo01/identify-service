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
