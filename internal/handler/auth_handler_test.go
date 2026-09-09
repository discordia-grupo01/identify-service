package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/service"
	"github.com/gin-gonic/gin"
)

type authServiceStub struct {
	response     *model.AuthResponse
	err          error
	request      model.RegisterRequest
	loginRequest model.LoginRequest
	called       bool
}

var _ service.AuthService = (*authServiceStub)(nil)

func (s *authServiceStub) Register(_ context.Context, request model.RegisterRequest) (*model.AuthResponse, error) {
	s.called = true
	s.request = request
	return s.response, s.err
}

func (s *authServiceStub) Login(_ context.Context, request model.LoginRequest) (*model.AuthResponse, error) {
	s.called = true
	s.loginRequest = request
	return s.response, s.err
}

func TestAuthHandlerRegisterReturnsCreatedUser(t *testing.T) {
	serviceStub := &authServiceStub{
		response: &model.AuthResponse{
			User: model.UserResponse{
				ID:        42,
				Name:      "Ada Lovelace",
				Email:     "ada@example.com",
				CreatedAt: time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC),
			},
			Token: "signed-token",
		},
	}
	recorder := performRegisterRequest(t, serviceStub, `{"name":"Ada Lovelace","email":"ada@example.com","password":"Secure123"}`)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusCreated)
	}
	if !serviceStub.called {
		t.Fatal("service Register() was not called")
	}
	if serviceStub.request.Email != "ada@example.com" || serviceStub.request.Password != "Secure123" {
		t.Fatalf("service received unexpected request: %+v", serviceStub.request)
	}

	var response model.AuthResponse
	decodeJSON(t, recorder, &response)
	if response.Token != "signed-token" || response.User.ID != 42 {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestAuthHandlerRegisterRejectsInvalidEmailBeforeCallingService(t *testing.T) {
	serviceStub := &authServiceStub{}
	recorder := performRegisterRequest(t, serviceStub, `{"name":"Ada Lovelace","email":"not-an-email","password":"Secure123"}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if serviceStub.called {
		t.Fatal("service must not be called for an invalid email")
	}

	var response model.ErrorResponse
	decodeJSON(t, recorder, &response)
	if response.Error.Code != "INVALID_INPUT" {
		t.Fatalf("error code = %q, want INVALID_INPUT", response.Error.Code)
	}
}

func TestAuthHandlerRegisterRejectsMissingRequiredFields(t *testing.T) {
	serviceStub := &authServiceStub{}
	recorder := performRegisterRequest(t, serviceStub, `{"email":"ada@example.com","password":"Secure123"}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if serviceStub.called {
		t.Fatal("service must not be called when required fields are missing")
	}
}

func TestAuthHandlerRegisterMapsBusinessErrors(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		wantStatus    int
		wantErrorCode string
	}{
		{name: "email already exists", err: service.ErrEmailInUse, wantStatus: http.StatusConflict, wantErrorCode: "EMAIL_ALREADY_EXISTS"},
		{name: "insecure password", err: service.ErrPasswordInsecure, wantStatus: http.StatusBadRequest, wantErrorCode: "INSECURE_PASSWORD"},
		{name: "unexpected error", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantErrorCode: "INTERNAL_SERVER_ERROR"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceStub := &authServiceStub{err: test.err}
			recorder := performRegisterRequest(t, serviceStub, `{"name":"Ada Lovelace","email":"ada@example.com","password":"Secure123"}`)

			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, test.wantStatus)
			}

			var response model.ErrorResponse
			decodeJSON(t, recorder, &response)
			if response.Error.Code != test.wantErrorCode {
				t.Fatalf("error code = %q, want %q", response.Error.Code, test.wantErrorCode)
			}
		})
	}
}

func TestAuthHandlerLoginReturnsToken(t *testing.T) {
	serviceStub := &authServiceStub{response: &model.AuthResponse{Token: "signed-token"}}
	recorder := performLoginRequest(t, serviceStub, `{"email":"ada@example.com","password":"Secure123"}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !serviceStub.called || serviceStub.loginRequest.Email != "ada@example.com" {
		t.Fatalf("service received unexpected login request: %+v", serviceStub.loginRequest)
	}
}

func TestAuthHandlerLoginUsesSameErrorForUnknownEmailAndWrongPassword(t *testing.T) {
	for _, test := range []string{"unknown email", "wrong password"} {
		t.Run(test, func(t *testing.T) {
			serviceStub := &authServiceStub{err: service.ErrInvalidCredentials}
			recorder := performLoginRequest(t, serviceStub, `{"email":"ada@example.com","password":"Wrong123"}`)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
			}
			var response model.ErrorResponse
			decodeJSON(t, recorder, &response)
			if response.Error.Code != "INVALID_CREDENTIALS" {
				t.Fatalf("error code = %q, want INVALID_CREDENTIALS", response.Error.Code)
			}
		})
	}
}

func TestAuthHandlerLoginRejectsInvalidEmailBeforeCallingService(t *testing.T) {
	serviceStub := &authServiceStub{}
	recorder := performLoginRequest(t, serviceStub, `{"email":"not-an-email","password":"Secure123"}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if serviceStub.called {
		t.Fatal("service must not be called for an invalid email")
	}
	var response model.ErrorResponse
	decodeJSON(t, recorder, &response)
	if response.Error.Code != "INVALID_INPUT" {
		t.Fatalf("error code = %q, want INVALID_INPUT", response.Error.Code)
	}
}

func TestAuthHandlerLoginRejectsMissingRequiredFields(t *testing.T) {
	serviceStub := &authServiceStub{}
	recorder := performLoginRequest(t, serviceStub, `{"email":"ada@example.com"}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if serviceStub.called {
		t.Fatal("service must not be called for missing required fields")
	}
	var response model.ErrorResponse
	decodeJSON(t, recorder, &response)
	if response.Error.Code != "INVALID_INPUT" {
		t.Fatalf("error code = %q, want INVALID_INPUT", response.Error.Code)
	}
}


func performRegisterRequest(t *testing.T, authService service.AuthService, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/users", jsonBody(body))
	request.Header.Set("Content-Type", "application/json")

	router := gin.New()
	router.POST("/v1/users", NewAuthHandler(authService).Register)
	router.ServeHTTP(recorder, request)
	return recorder
}

func performLoginRequest(t *testing.T, authService service.AuthService, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/login", jsonBody(body))
	request.Header.Set("Content-Type", "application/json")

	router := gin.New()
	router.POST("/v1/login", NewAuthHandler(authService).Login)
	router.ServeHTTP(recorder, request)
	return recorder
}

func jsonBody(body string) *bytes.Buffer {
	return bytes.NewBufferString(body)
}

func decodeJSON(t *testing.T, recorder *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(recorder.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
