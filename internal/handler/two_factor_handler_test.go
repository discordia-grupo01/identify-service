package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/discordia-grupo01/identify-service/internal/middleware"
	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/service"

	"github.com/gin-gonic/gin"
)

type twoFactorServiceStub struct {
	status        *model.TwoFactorStatusResponse
	setup         *model.TwoFactorSetupResponse
	recoveryCodes []string
	verifyResult  *model.TwoFactorVerifyResponse
	err           error

	activateCode  string
	verifyRequest model.TwoFactorVerifyRequest
	password      string
	userID        string
	called        bool
}

var _ service.TwoFactorService = (*twoFactorServiceStub)(nil)

func (s *twoFactorServiceStub) Status(_ context.Context, userID string) (*model.TwoFactorStatusResponse, error) {
	s.called, s.userID = true, userID
	return s.status, s.err
}

func (s *twoFactorServiceStub) StartSetup(_ context.Context, userID string) (*model.TwoFactorSetupResponse, error) {
	s.called, s.userID = true, userID
	return s.setup, s.err
}

func (s *twoFactorServiceStub) ConfirmSetup(_ context.Context, userID string, code string) ([]string, error) {
	s.called, s.userID, s.activateCode = true, userID, code
	return s.recoveryCodes, s.err
}

func (s *twoFactorServiceStub) DisableWithPassword(_ context.Context, userID string, password string) error {
	s.called, s.userID, s.password = true, userID, password
	return s.err
}

func (s *twoFactorServiceStub) RegenerateRecoveryCodes(_ context.Context, userID string, password string) ([]string, error) {
	s.called, s.userID, s.password = true, userID, password
	return s.recoveryCodes, s.err
}

func (s *twoFactorServiceStub) IsEnabled(_ context.Context, _ string) (bool, error) {
	return s.status != nil && s.status.Enabled, s.err
}

func (s *twoFactorServiceStub) StartChallenge(_ context.Context, _ string) (*model.TwoFactorChallengeResponse, error) {
	return nil, s.err
}

func (s *twoFactorServiceStub) VerifyLogin(_ context.Context, req model.TwoFactorVerifyRequest) (*model.TwoFactorVerifyResponse, error) {
	s.called, s.verifyRequest = true, req
	return s.verifyResult, s.err
}

func TestAuthHandlerLoginReturnsTheChallengeWithoutACookie(t *testing.T) {
	serviceStub := &authServiceStub{challenge: &model.TwoFactorChallengeResponse{
		TwoFactorRequired: true,
		ChallengeToken:    "challenge-token",
		ExpiresIn:         300,
	}}

	recorder := performLoginRequest(t, serviceStub, `{"email":"ada@example.com","password":"Secure123"}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var response model.TwoFactorChallengeResponse
	decodeJSON(t, recorder, &response)
	if !response.TwoFactorRequired || response.ChallengeToken != "challenge-token" {
		t.Fatalf("unexpected response: %+v", response)
	}

	// La sesión todavía no existe: mandar la cookie acá sería otorgarla.
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == refreshCookieName && cookie.Value != "" {
			t.Fatal("a two factor challenge must not set the refresh cookie")
		}
	}
}

func TestTwoFactorHandlerActivateReturnsRecoveryCodes(t *testing.T) {
	serviceStub := &twoFactorServiceStub{recoveryCodes: []string{"A1B2C-D3E4F", "G5H6J-K7M8N"}}

	recorder := performTwoFactorRequest(t, serviceStub, http.MethodPost, "/v1/me/2fa/activate", `{"code":"123456"}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if serviceStub.activateCode != "123456" {
		t.Fatalf("service received code %q, want 123456", serviceStub.activateCode)
	}
	if serviceStub.userID != twoFactorTestUserID {
		t.Fatalf("service received user %q, want the one from the JWT", serviceStub.userID)
	}

	var response model.TwoFactorRecoveryCodesResponse
	decodeJSON(t, recorder, &response)
	if len(response.RecoveryCodes) != 2 {
		t.Fatalf("recovery codes = %+v, want 2", response.RecoveryCodes)
	}
}

func TestTwoFactorHandlerActivateMapsBusinessErrors(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		wantStatus    int
		wantErrorCode string
	}{
		{
			name: "código incorrecto", err: service.ErrTwoFactorCodeInvalid,
			// 400 y no 401: la sesión sigue siendo válida, lo que está mal es
			// el código. Un 401 mandaría al usuario al login.
			wantStatus: http.StatusBadRequest, wantErrorCode: "INVALID_TWO_FACTOR_CODE",
		},
		{
			name: "sin escanear el QR", err: service.ErrTwoFactorSetupMissing,
			wantStatus: http.StatusConflict, wantErrorCode: "TWO_FACTOR_SETUP_MISSING",
		},
		{
			name: "ya estaba activo", err: service.ErrTwoFactorAlreadyEnabled,
			wantStatus: http.StatusConflict, wantErrorCode: "TWO_FACTOR_ALREADY_ENABLED",
		},
		{
			name: "error inesperado", err: errors.New("boom"),
			wantStatus: http.StatusInternalServerError, wantErrorCode: "INTERNAL_SERVER_ERROR",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := performTwoFactorRequest(t, &twoFactorServiceStub{err: test.err},
				http.MethodPost, "/v1/me/2fa/activate", `{"code":"123456"}`)

			assertAPIError(t, recorder, test.wantStatus, test.wantErrorCode)
		})
	}
}

func TestTwoFactorHandlerSetupReportsAnAlreadyActiveAccount(t *testing.T) {
	recorder := performTwoFactorRequest(t, &twoFactorServiceStub{err: service.ErrTwoFactorAlreadyEnabled},
		http.MethodPost, "/v1/me/2fa/setup", "")

	assertAPIError(t, recorder, http.StatusConflict, "TWO_FACTOR_ALREADY_ENABLED")
}

func TestTwoFactorHandlerSetupReturnsTheOtpauthURL(t *testing.T) {
	serviceStub := &twoFactorServiceStub{setup: &model.TwoFactorSetupResponse{
		Secret:     "JBSWY3DPEHPK3PXP",
		OtpauthURL: "otpauth://totp/Discordia:ada@example.com?secret=JBSWY3DPEHPK3PXP",
		Issuer:     "Discordia",
		Account:    "ada@example.com",
	}}

	recorder := performTwoFactorRequest(t, serviceStub, http.MethodPost, "/v1/me/2fa/setup", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var response model.TwoFactorSetupResponse
	decodeJSON(t, recorder, &response)
	if response.OtpauthURL == "" || response.Secret == "" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestTwoFactorHandlerStatusReturnsTheAccountState(t *testing.T) {
	serviceStub := &twoFactorServiceStub{status: &model.TwoFactorStatusResponse{
		Enabled:                true,
		RecoveryCodesRemaining: 7,
	}}

	recorder := performTwoFactorRequest(t, serviceStub, http.MethodGet, "/v1/me/2fa", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var response model.TwoFactorStatusResponse
	decodeJSON(t, recorder, &response)
	if !response.Enabled || response.RecoveryCodesRemaining != 7 {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestTwoFactorHandlerVerifyLoginSetsTheRefreshCookie(t *testing.T) {
	serviceStub := &twoFactorServiceStub{verifyResult: &model.TwoFactorVerifyResponse{
		AuthResponse: model.AuthResponse{
			User:         model.UserResponse{ID: twoFactorTestUserID, Email: "ada@example.com"},
			Token:        "access-token",
			RefreshToken: "refresh-token",
		},
	}}

	recorder := performPublicRequest(t, serviceStub, "/v1/login/2fa", `{"challenge_token":"abc","code":"123456"}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if serviceStub.verifyRequest.ChallengeToken != "abc" || serviceStub.verifyRequest.Code != "123456" {
		t.Fatalf("service received %+v", serviceStub.verifyRequest)
	}

	var response model.TwoFactorVerifyResponse
	decodeJSON(t, recorder, &response)
	if response.Token != "access-token" {
		t.Fatalf("token = %q, want the access token", response.Token)
	}
	// El refresh token va sólo en la cookie httpOnly, nunca en el cuerpo.
	if response.RefreshToken != "" {
		t.Fatal("the refresh token must not be echoed in the body")
	}

	if !hasRefreshCookie(recorder) {
		t.Fatal("a completed login must set the refresh cookie")
	}
}

func TestTwoFactorHandlerVerifyLoginReportsAConsumedRecoveryCode(t *testing.T) {
	serviceStub := &twoFactorServiceStub{verifyResult: &model.TwoFactorVerifyResponse{
		AuthResponse: model.AuthResponse{
			User:         model.UserResponse{ID: twoFactorTestUserID},
			Token:        "access-token",
			RefreshToken: "refresh-token",
		},
		RecoveryCodeUsed:       true,
		RecoveryCodesRemaining: 9,
	}}

	recorder := performPublicRequest(t, serviceStub, "/v1/login/2fa", `{"challenge_token":"abc","code":"A1B2C-D3E4F"}`)

	var response model.TwoFactorVerifyResponse
	decodeJSON(t, recorder, &response)
	if !response.RecoveryCodeUsed || response.RecoveryCodesRemaining != 9 {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestTwoFactorHandlerVerifyLoginMapsBusinessErrors(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		wantStatus    int
		wantErrorCode string
	}{
		{
			name: "código incorrecto o vencido", err: service.ErrTwoFactorCodeInvalid,
			wantStatus: http.StatusUnauthorized, wantErrorCode: "INVALID_TWO_FACTOR_CODE",
		},
		{
			name: "desafío vencido", err: service.ErrTwoFactorChallengeInvalid,
			wantStatus: http.StatusUnauthorized, wantErrorCode: "TWO_FACTOR_CHALLENGE_EXPIRED",
		},
		{
			name: "demasiados intentos", err: service.ErrTwoFactorTooManyAttempts,
			wantStatus: http.StatusTooManyRequests, wantErrorCode: "TWO_FACTOR_TOO_MANY_ATTEMPTS",
		},
		{
			name: "error inesperado", err: errors.New("boom"),
			wantStatus: http.StatusInternalServerError, wantErrorCode: "INTERNAL_SERVER_ERROR",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := performPublicRequest(t, &twoFactorServiceStub{err: test.err},
				"/v1/login/2fa", `{"challenge_token":"abc","code":"000000"}`)

			assertAPIError(t, recorder, test.wantStatus, test.wantErrorCode)

			if hasRefreshCookie(recorder) {
				t.Fatal("a rejected code must not set the refresh cookie")
			}
		})
	}
}

func TestTwoFactorHandlerVerifyLoginRejectsAMissingCode(t *testing.T) {
	serviceStub := &twoFactorServiceStub{}

	recorder := performPublicRequest(t, serviceStub, "/v1/login/2fa", `{"challenge_token":"abc"}`)

	assertAPIError(t, recorder, http.StatusBadRequest, "INVALID_INPUT")
	if serviceStub.called {
		t.Fatal("the service must not be called when the code is missing")
	}
}

func TestTwoFactorHandlerDisableReturnsNoContent(t *testing.T) {
	serviceStub := &twoFactorServiceStub{}

	recorder := performTwoFactorRequest(t, serviceStub, http.MethodPost, "/v1/me/2fa/disable", `{"password":"Secure123"}`)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if serviceStub.password != "Secure123" {
		t.Fatalf("service received password %q", serviceStub.password)
	}
}

func TestTwoFactorHandlerPasswordProtectedEndpointsMapBusinessErrors(t *testing.T) {
	paths := []string{"/v1/me/2fa/disable", "/v1/me/2fa/recovery-codes"}
	tests := []struct {
		name          string
		err           error
		wantStatus    int
		wantErrorCode string
	}{
		{
			name: "contraseña incorrecta", err: service.ErrPasswordMismatch,
			// 403 y no 401 por lo mismo que en Activate: el 401 está
			// reservado para "la sesión venció".
			wantStatus: http.StatusForbidden, wantErrorCode: "INVALID_PASSWORD",
		},
		{
			name: "2FA no activo", err: service.ErrTwoFactorNotEnabled,
			wantStatus: http.StatusConflict, wantErrorCode: "TWO_FACTOR_NOT_ENABLED",
		},
		{
			name: "error inesperado", err: errors.New("boom"),
			wantStatus: http.StatusInternalServerError, wantErrorCode: "INTERNAL_SERVER_ERROR",
		},
	}

	for _, path := range paths {
		for _, test := range tests {
			t.Run(path+" "+test.name, func(t *testing.T) {
				recorder := performTwoFactorRequest(t, &twoFactorServiceStub{err: test.err},
					http.MethodPost, path, `{"password":"Secure123"}`)

				assertAPIError(t, recorder, test.wantStatus, test.wantErrorCode)
			})
		}
	}
}

func TestTwoFactorHandlerDisableRejectsAMissingPassword(t *testing.T) {
	serviceStub := &twoFactorServiceStub{}

	recorder := performTwoFactorRequest(t, serviceStub, http.MethodPost, "/v1/me/2fa/disable", `{}`)

	assertAPIError(t, recorder, http.StatusBadRequest, "INVALID_INPUT")
	if serviceStub.called {
		t.Fatal("the service must not be called without a password")
	}
}

func TestTwoFactorHandlerRegenerateReturnsTheNewList(t *testing.T) {
	serviceStub := &twoFactorServiceStub{recoveryCodes: []string{"A1B2C-D3E4F"}}

	recorder := performTwoFactorRequest(t, serviceStub, http.MethodPost, "/v1/me/2fa/recovery-codes", `{"password":"Secure123"}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var response model.TwoFactorRecoveryCodesResponse
	decodeJSON(t, recorder, &response)
	if len(response.RecoveryCodes) != 1 {
		t.Fatalf("recovery codes = %+v, want 1", response.RecoveryCodes)
	}
}

// Helpers 

const twoFactorTestUserID = "550e8400-e29b-41d4-a716-446655440000"

// performTwoFactorRequest llama a un endpoint autenticado salteando el
// middleware real: acá se prueba el handler, no la validación del JWT (que
// tiene sus propios tests en internal/middleware).
func performTwoFactorRequest(t *testing.T, twoFactorService service.TwoFactorService, method string, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, jsonBody(body))
	request.Header.Set("Content-Type", "application/json")

	handler := NewTwoFactorHandler(twoFactorService)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(middleware.UserIDKey, twoFactorTestUserID)
		c.Next()
	})
	router.GET("/v1/me/2fa", handler.Status)
	router.POST("/v1/me/2fa/setup", handler.Setup)
	router.POST("/v1/me/2fa/activate", handler.Activate)
	router.POST("/v1/me/2fa/disable", handler.Disable)
	router.POST("/v1/me/2fa/recovery-codes", handler.RegenerateRecoveryCodes)
	router.ServeHTTP(recorder, request)
	return recorder
}

func performPublicRequest(t *testing.T, twoFactorService service.TwoFactorService, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, jsonBody(body))
	request.Header.Set("Content-Type", "application/json")

	router := gin.New()
	router.POST("/v1/login/2fa", NewTwoFactorHandler(twoFactorService).VerifyLogin)
	router.ServeHTTP(recorder, request)
	return recorder
}

func assertAPIError(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantErrorCode string) {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("status = %d, want %d (body = %s)", recorder.Code, wantStatus, recorder.Body.String())
	}

	var response model.ErrorResponse
	decodeJSON(t, recorder, &response)
	if response.Error.Code != wantErrorCode {
		t.Fatalf("error code = %q, want %q", response.Error.Code, wantErrorCode)
	}
}

func hasRefreshCookie(recorder *httptest.ResponseRecorder) bool {
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == refreshCookieName && cookie.Value != "" {
			return true
		}
	}
	return false
}
