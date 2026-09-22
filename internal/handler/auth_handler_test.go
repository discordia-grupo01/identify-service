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
	response      *model.AuthResponse
	registerUser  *model.UserResponse
	err           error
	request       model.RegisterRequest
	loginRequest  model.LoginRequest
	called        bool
	logoutCalled  bool
	logoutToken   string
	refreshCalled bool
	refreshToken  string
}

var _ service.AuthService = (*authServiceStub)(nil)

func (s *authServiceStub) Register(_ context.Context, request model.RegisterRequest) (*model.UserResponse, error) {
	s.called = true
	s.request = request
	return s.registerUser, s.err
}

func (s *authServiceStub) Login(_ context.Context, request model.LoginRequest) (*model.AuthResponse, error) {
	s.called = true
	s.loginRequest = request
	return s.response, s.err
}

func (s *authServiceStub) Refresh(_ context.Context, refreshToken string) (*model.AuthResponse, error) {
	s.refreshCalled = true
	s.refreshToken = refreshToken
	return s.response, s.err
}

func (s *authServiceStub) Logout(_ context.Context, refreshToken string) error {
	s.logoutCalled = true
	s.logoutToken = refreshToken
	return s.err
}

func TestAuthHandlerRegisterReturnsCreatedUser(t *testing.T) {
	serviceStub := &authServiceStub{
		registerUser: &model.UserResponse{
			ID:        "550e8400-e29b-41d4-a716-446655440000",
			Name:      "Ada Lovelace",
			Email:     "ada@example.com",
			CreatedAt: time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC),
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

	var response model.UserResponse
	decodeJSON(t, recorder, &response)
	if response.ID != "550e8400-e29b-41d4-a716-446655440000" {
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

func TestAuthHandlerLoginSetsRefreshCookieAndReturnsToken(t *testing.T) {
	serviceStub := &authServiceStub{response: &model.AuthResponse{
		Token:        "signed-access-token",
		RefreshToken: "plain-refresh-token",
	}}
	recorder := performLoginRequest(t, serviceStub, `{"email":"ada@example.com","password":"Secure123"}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !serviceStub.called || serviceStub.loginRequest.Email != "ada@example.com" {
		t.Fatalf("service received unexpected login request: %+v", serviceStub.loginRequest)
	}

	cookie := findCookie(t, recorder, "refresh_token")
	if cookie.Value != "plain-refresh-token" {
		t.Fatalf("refresh_token cookie = %q, want plain-refresh-token", cookie.Value)
	}
	if !cookie.HttpOnly || !cookie.Secure {
		t.Fatal("refresh_token cookie must be HttpOnly and Secure")
	}

	var response model.AuthResponse
	decodeJSON(t, recorder, &response)
	if response.RefreshToken != "" {
		t.Fatal("refresh token must never be returned in the response body")
	}
	if response.Token != "signed-access-token" {
		t.Fatalf("access token = %q, want signed-access-token", response.Token)
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

func TestAuthHandlerLoginMapsEmailNotVerifiedSeparately(t *testing.T) {
	serviceStub := &authServiceStub{err: service.ErrEmailNotVerified}
	recorder := performLoginRequest(t, serviceStub, `{"email":"ada@example.com","password":"Secure123"}`)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
	var response model.ErrorResponse
	decodeJSON(t, recorder, &response)
	if response.Error.Code != "EMAIL_NOT_VERIFIED" {
		t.Fatalf("error code = %q, want EMAIL_NOT_VERIFIED", response.Error.Code)
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

func TestAuthHandlerRefreshRotatesRefreshCookie(t *testing.T) {
	serviceStub := &authServiceStub{response: &model.AuthResponse{
		Token:        "new-access-token",
		RefreshToken: "new-refresh-token",
	}}
	recorder := performRefreshRequest(t, serviceStub, "old-refresh-token")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !serviceStub.refreshCalled || serviceStub.refreshToken != "old-refresh-token" {
		t.Fatalf("service received unexpected refresh token: %+v", serviceStub)
	}

	cookie := findCookie(t, recorder, "refresh_token")
	if cookie.Value != "new-refresh-token" {
		t.Fatalf("refresh_token cookie = %q, want new-refresh-token", cookie.Value)
	}

	var response model.AuthResponse
	decodeJSON(t, recorder, &response)
	if response.RefreshToken != "" {
		t.Fatal("refresh token must never be returned in the response body")
	}
	if response.Token != "new-access-token" {
		t.Fatalf("access token = %q, want new-access-token", response.Token)
	}
}

func TestAuthHandlerRefreshRejectsMissingCookie(t *testing.T) {
	serviceStub := &authServiceStub{}
	recorder := performRefreshRequest(t, serviceStub, "")

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	if serviceStub.refreshCalled {
		t.Fatal("service Refresh() must not be called without a refresh token")
	}
	var response model.ErrorResponse
	decodeJSON(t, recorder, &response)
	if response.Error.Code != "SESSION_EXPIRED" {
		t.Fatalf("error code = %q, want SESSION_EXPIRED", response.Error.Code)
	}
}

func TestAuthHandlerRefreshMapsInvalidOrReusedToken(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "invalid token", err: service.ErrInvalidRefreshToken},
		{name: "reused token", err: service.ErrRefreshReused},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceStub := &authServiceStub{err: test.err}
			recorder := performRefreshRequest(t, serviceStub, "stale-refresh-token")

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
			}
			var response model.ErrorResponse
			decodeJSON(t, recorder, &response)
			if response.Error.Code != "SESSION_EXPIRED" {
				t.Fatalf("error code = %q, want SESSION_EXPIRED", response.Error.Code)
			}

			cookie := findCookie(t, recorder, "refresh_token")
			if cookie.Value != "" || cookie.MaxAge >= 0 {
				t.Fatalf("refresh_token cookie must be cleared, got %+v", cookie)
			}
		})
	}
}

func TestAuthHandlerRefreshMapsUnexpectedError(t *testing.T) {
	serviceStub := &authServiceStub{err: errors.New("database unavailable")}
	recorder := performRefreshRequest(t, serviceStub, "stale-refresh-token")

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	var response model.ErrorResponse
	decodeJSON(t, recorder, &response)
	if response.Error.Code != "INTERNAL_SERVER_ERROR" {
		t.Fatalf("error code = %q, want INTERNAL_SERVER_ERROR", response.Error.Code)
	}
}

func TestAuthHandlerLogoutRevokesRefreshTokenAndClearsCookie(t *testing.T) {
	serviceStub := &authServiceStub{}
	recorder := performLogoutRequest(t, serviceStub, "plain-refresh-token")

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if !serviceStub.logoutCalled {
		t.Fatal("service Logout() was not called")
	}
	if serviceStub.logoutToken != "plain-refresh-token" {
		t.Fatalf("Logout() token = %q, want plain-refresh-token", serviceStub.logoutToken)
	}

	cookie := findCookie(t, recorder, "refresh_token")
	if cookie.Value != "" || cookie.MaxAge >= 0 {
		t.Fatalf("refresh_token cookie must be cleared, got %+v", cookie)
	}
}

func TestAuthHandlerLogoutRejectsMissingCookie(t *testing.T) {
	serviceStub := &authServiceStub{}
	recorder := performLogoutRequest(t, serviceStub, "")

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	var response model.ErrorResponse
	decodeJSON(t, recorder, &response)
	if response.Error.Code != "UNAUTHORIZED" {
		t.Fatalf("error code = %q, want UNAUTHORIZED", response.Error.Code)
	}
	if serviceStub.logoutCalled {
		t.Fatal("service Logout() must not be called without a refresh token")
	}
}

func TestAuthHandlerLogoutMapsExpiredOrRevokedSession(t *testing.T) {
	serviceStub := &authServiceStub{err: service.ErrInexistentSession}
	recorder := performLogoutRequest(t, serviceStub, "expired-token")

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	var response model.ErrorResponse
	decodeJSON(t, recorder, &response)
	if response.Error.Code != "SESSION_EXPIRED" {
		t.Fatalf("error code = %q, want SESSION_EXPIRED", response.Error.Code)
	}
}

func TestAuthHandlerLogoutMapsUnexpectedServiceError(t *testing.T) {
	serviceStub := &authServiceStub{err: errors.New("database unavailable")}
	recorder := performLogoutRequest(t, serviceStub, "plain-refresh-token")

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	var response model.ErrorResponse
	decodeJSON(t, recorder, &response)
	if response.Error.Code != "INTERNAL_SERVER_ERROR" {
		t.Fatalf("error code = %q, want INTERNAL_SERVER_ERROR", response.Error.Code)
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

func performRefreshRequest(t *testing.T, authService service.AuthService, refreshToken string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh", nil)
	if refreshToken != "" {
		request.AddCookie(&http.Cookie{Name: "refresh_token", Value: refreshToken})
	}

	router := gin.New()
	router.POST("/v1/auth/refresh", NewAuthHandler(authService).Refresh)
	router.ServeHTTP(recorder, request)
	return recorder
}

func performLogoutRequest(t *testing.T, authService service.AuthService, refreshToken string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/logout", nil)
	if refreshToken != "" {
		request.AddCookie(&http.Cookie{Name: "refresh_token", Value: refreshToken})
	}

	router := gin.New()
	router.POST("/v1/logout", NewAuthHandler(authService).Logout)
	router.ServeHTTP(recorder, request)
	return recorder
}

func findCookie(t *testing.T, recorder *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("cookie %q not set", name)
	return nil
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
