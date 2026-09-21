package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/discordia-grupo01/identify-service/internal/middleware"
	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/service"
	"github.com/gin-gonic/gin"
)

type pinServiceStub struct {
	configureErr   error
	configured     bool
	configuredArgs struct {
		userID   string
		deviceID string
		pin      string
	}

	loginResponse *model.AuthResponse
	loginErr      error
	loginArgs     struct {
		deviceID string
		pin      string
	}
}

var _ service.PinService = (*pinServiceStub)(nil)

func (s *pinServiceStub) ConfigurePin(_ context.Context, userID string, deviceID string, pin string) error {
	s.configured = true
	s.configuredArgs.userID = userID
	s.configuredArgs.deviceID = deviceID
	s.configuredArgs.pin = pin
	return s.configureErr
}

func (s *pinServiceStub) LoginWithPin(_ context.Context, deviceID string, pin string) (*model.AuthResponse, error) {
	s.loginArgs.deviceID = deviceID
	s.loginArgs.pin = pin
	return s.loginResponse, s.loginErr
}

func performConfigurePinRequest(t *testing.T, pinService service.PinService, userID string, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/pin", jsonBody(body))
	request.Header.Set("Content-Type", "application/json")

	router := gin.New()
	router.POST("/v1/pin", func(c *gin.Context) {
		if userID != "" {
			c.Set(middleware.UserIDKey, userID)
		}
		NewPinHandler(pinService).Configure(c)
	})
	router.ServeHTTP(recorder, request)
	return recorder
}

func performPinLoginRequest(t *testing.T, pinService service.PinService, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/pin/login", jsonBody(body))
	request.Header.Set("Content-Type", "application/json")

	router := gin.New()
	router.POST("/v1/pin/login", NewPinHandler(pinService).Login)
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestPinHandlerConfigureActivatesThePinForTheAuthenticatedUser(t *testing.T) {
	serviceStub := &pinServiceStub{}

	recorder := performConfigurePinRequest(t, serviceStub, "550e8400-e29b-41d4-a716-446655440000", `{"pin":"123456","device_id":"device-1"}`)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if !serviceStub.configured {
		t.Fatal("expected the service to be called")
	}
	if serviceStub.configuredArgs.userID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("userID = %q, want the authenticated user's id (from the JWT, not the body)", serviceStub.configuredArgs.userID)
	}
	if serviceStub.configuredArgs.deviceID != "device-1" || serviceStub.configuredArgs.pin != "123456" {
		t.Fatalf("unexpected args: %+v", serviceStub.configuredArgs)
	}
}

func TestPinHandlerConfigureRejectsMissingFields(t *testing.T) {
	serviceStub := &pinServiceStub{}

	recorder := performConfigurePinRequest(t, serviceStub, "550e8400-e29b-41d4-a716-446655440000", `{}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if serviceStub.configured {
		t.Fatal("service must not be called without pin/device_id")
	}
}

func TestPinHandlerConfigureMapsInvalidFormatToBadRequest(t *testing.T) {
	serviceStub := &pinServiceStub{configureErr: service.ErrPinInvalidFormat}

	recorder := performConfigurePinRequest(t, serviceStub, "550e8400-e29b-41d4-a716-446655440000", `{"pin":"12","device_id":"device-1"}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	var response model.ErrorResponse
	decodeJSON(t, recorder, &response)
	if response.Error.Code != "INVALID_PIN" {
		t.Fatalf("error code = %q, want INVALID_PIN", response.Error.Code)
	}
}

func TestPinHandlerLoginReturnsSessionOnSuccessWithoutRequiringEmail(t *testing.T) {
	serviceStub := &pinServiceStub{loginResponse: &model.AuthResponse{
		User:         model.UserResponse{ID: "550e8400-e29b-41d4-a716-446655440000"},
		Token:        "signed-access-token",
		RefreshToken: "plain-refresh-token",
	}}

	recorder := performPinLoginRequest(t, serviceStub, `{"pin":"123456","device_id":"device-1"}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if serviceStub.loginArgs.deviceID != "device-1" || serviceStub.loginArgs.pin != "123456" {
		t.Fatalf("unexpected args: %+v", serviceStub.loginArgs)
	}

	cookie := findCookie(t, recorder, "refresh_token")
	if cookie.Value != "plain-refresh-token" {
		t.Fatalf("refresh_token cookie = %q, want plain-refresh-token", cookie.Value)
	}

	var response model.AuthResponse
	decodeJSON(t, recorder, &response)
	if response.RefreshToken != "" {
		t.Fatal("refresh token must never be returned in the response body")
	}
}

func TestPinHandlerLoginMapsUnknownDeviceToUnauthorized(t *testing.T) {
	serviceStub := &pinServiceStub{loginErr: service.ErrPinNotConfigured}

	recorder := performPinLoginRequest(t, serviceStub, `{"pin":"123456","device_id":"device-never-configured"}`)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (CA4: atado a este dispositivo)", recorder.Code, http.StatusUnauthorized)
	}
	var response model.ErrorResponse
	decodeJSON(t, recorder, &response)
	if response.Error.Code != "INVALID_PIN" {
		t.Fatalf("error code = %q, want INVALID_PIN", response.Error.Code)
	}
}

func TestPinHandlerLoginMapsWrongPinToUnauthorized(t *testing.T) {
	serviceStub := &pinServiceStub{loginErr: service.ErrPinIncorrect}

	recorder := performPinLoginRequest(t, serviceStub, `{"pin":"000000","device_id":"device-1"}`)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}
