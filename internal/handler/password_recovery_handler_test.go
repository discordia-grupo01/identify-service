package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/service"
	"github.com/gin-gonic/gin"
)

type passwordRecoveryServiceStub struct {
	recoverErr     error
	resetErr       error
	recoverCalled  bool
	resetCalled    bool
	recoverRequest model.PasswordRecoveryRequest
	resetRequest   model.PasswordResetRequest
}

var _ service.PasswordRecoveryService = (*passwordRecoveryServiceStub)(nil)

func (s *passwordRecoveryServiceStub) RecoverPassword(_ context.Context, request model.PasswordRecoveryRequest) error {
	s.recoverCalled = true
	s.recoverRequest = request
	return s.recoverErr
}

func (s *passwordRecoveryServiceStub) ResetPassword(_ context.Context, request model.PasswordResetRequest) error {
	s.resetCalled = true
	s.resetRequest = request
	return s.resetErr
}

func TestPasswordRecoveryHandlerReturnsGenericAcceptedResponse(t *testing.T) {
	serviceStub := &passwordRecoveryServiceStub{}
	recorder := performPasswordRecoveryRequest(t, serviceStub, `{"email":"ada@example.com"}`)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusAccepted)
	}
	if !serviceStub.recoverCalled || serviceStub.recoverRequest.Email != "ada@example.com" {
		t.Fatalf("service received unexpected request: %+v", serviceStub.recoverRequest)
	}

	var response map[string]string
	decodePasswordRecoveryJSON(t, recorder, &response)
	if response["message"] != "Si el correo está registrado, recibirás un enlace para recuperar tu contraseña" {
		t.Fatalf("message = %q, want generic success message", response["message"])
	}
}

func TestPasswordRecoveryHandlerReturnsSameSuccessShapeForUnknownEmail(t *testing.T) {
	serviceStub := &passwordRecoveryServiceStub{}
	recorder := performPasswordRecoveryRequest(t, serviceStub, `{"email":"unknown@example.com"}`)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusAccepted)
	}
	var response map[string]string
	decodePasswordRecoveryJSON(t, recorder, &response)
	if response["message"] != "Si el correo está registrado, recibirás un enlace para recuperar tu contraseña" {
		t.Fatalf("unknown-email response leaked a different message: %q", response["message"])
	}
}

func TestPasswordRecoveryHandlerRejectsInvalidRequestsBeforeService(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed json", body: `{"email":`},
		{name: "missing email", body: `{}`},
		{name: "invalid email", body: `{"email":"not-an-email"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceStub := &passwordRecoveryServiceStub{}
			recorder := performPasswordRecoveryRequest(t, serviceStub, test.body)

			assertErrorResponse(t, recorder, http.StatusBadRequest, "INVALID_INPUT")
			if serviceStub.recoverCalled {
				t.Fatal("service must not be called for invalid input")
			}
		})
	}
}

func TestPasswordRecoveryHandlerMapsBusinessAndUnexpectedErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "rate limited", err: service.ErrRecoveryRateLimited, wantStatus: http.StatusTooManyRequests, wantCode: "RECOVERY_RATE_LIMITED"},
		{name: "unexpected error", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL_SERVER_ERROR"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceStub := &passwordRecoveryServiceStub{recoverErr: test.err}
			recorder := performPasswordRecoveryRequest(t, serviceStub, `{"email":"ada@example.com"}`)
			assertErrorResponse(t, recorder, test.wantStatus, test.wantCode)
		})
	}
}

func TestPasswordResetHandlerReturnsSuccessAndForwardsRequest(t *testing.T) {
	serviceStub := &passwordRecoveryServiceStub{}
	recorder := performPasswordResetRequest(t, serviceStub, `{"token":"plain-token","new_password":"NewSecure123","confirm_password":"NewSecure123"}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !serviceStub.resetCalled {
		t.Fatal("service ResetPassword() was not called")
	}
	want := model.PasswordResetRequest{Token: "plain-token", NewPassword: "NewSecure123", ConfirmPassword: "NewSecure123"}
	if serviceStub.resetRequest != want {
		t.Fatalf("service received %+v, want %+v", serviceStub.resetRequest, want)
	}

	var response map[string]string
	decodePasswordRecoveryJSON(t, recorder, &response)
	if response["message"] != "La contraseña fue actualizada correctamente" {
		t.Fatalf("message = %q, want success message", response["message"])
	}
}

func TestPasswordResetHandlerRejectsInvalidRequestsBeforeService(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed json", body: `{"token":`},
		{name: "missing token", body: `{"new_password":"NewSecure123","confirm_password":"NewSecure123"}`},
		{name: "missing confirmation", body: `{"token":"token","new_password":"NewSecure123"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceStub := &passwordRecoveryServiceStub{}
			recorder := performPasswordResetRequest(t, serviceStub, test.body)

			assertErrorResponse(t, recorder, http.StatusBadRequest, "INVALID_INPUT")
			if serviceStub.resetCalled {
				t.Fatal("service must not be called for invalid input")
			}
		})
	}
}

func TestPasswordResetHandlerMapsAllBusinessErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "passwords do not match", err: service.ErrPasswordsDoNotMatch, wantStatus: http.StatusBadRequest, wantCode: "PASSWORDS_DO_NOT_MATCH"},
		{name: "insecure password", err: service.ErrPasswordInsecure, wantStatus: http.StatusBadRequest, wantCode: "INSECURE_PASSWORD"},
		{name: "invalid token", err: service.ErrInvalidResetToken, wantStatus: http.StatusBadRequest, wantCode: "INVALID_RESET_TOKEN"},
		{name: "unexpected error", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL_SERVER_ERROR"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceStub := &passwordRecoveryServiceStub{resetErr: test.err}
			recorder := performPasswordResetRequest(t, serviceStub, `{"token":"token","new_password":"NewSecure123","confirm_password":"NewSecure123"}`)
			assertErrorResponse(t, recorder, test.wantStatus, test.wantCode)
		})
	}
}

func performPasswordRecoveryRequest(t *testing.T, passwordRecoveryService service.PasswordRecoveryService, body string) *httptest.ResponseRecorder {
	t.Helper()
	return performPasswordRecoveryHandlerRequest(t, passwordRecoveryService, "/v1/password-recovery", body, NewPasswordRecoveryHandler(passwordRecoveryService).RecoverPassword)
}

func performPasswordResetRequest(t *testing.T, passwordRecoveryService service.PasswordRecoveryService, body string) *httptest.ResponseRecorder {
	t.Helper()
	return performPasswordRecoveryHandlerRequest(t, passwordRecoveryService, "/v1/password-reset", body, NewPasswordRecoveryHandler(passwordRecoveryService).ResetPassword)
}

func performPasswordRecoveryHandlerRequest(t *testing.T, passwordRecoveryService service.PasswordRecoveryService, path string, body string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")

	router := gin.New()
	router.POST(path, handler)
	router.ServeHTTP(recorder, request)
	return recorder
}

func assertErrorResponse(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("status = %d, want %d", recorder.Code, wantStatus)
	}
	var response model.ErrorResponse
	decodePasswordRecoveryJSON(t, recorder, &response)
	if response.Error.Code != wantCode {
		t.Fatalf("error code = %q, want %q", response.Error.Code, wantCode)
	}
	if response.Error.Message == "" {
		t.Fatal("error message must not be empty")
	}
}

func decodePasswordRecoveryJSON(t *testing.T, recorder *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(recorder.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
