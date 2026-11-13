package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/service"

	"github.com/gin-gonic/gin"
)

type accountRecoveryServiceStub struct {
	response *model.AccountRecoveryCreateResponse
	err      error
	request  model.AccountRecoveryCreateRequest
	called   bool
}

var _ service.AccountRecoveryService = (*accountRecoveryServiceStub)(nil)

func (s *accountRecoveryServiceStub) Request(_ context.Context, request model.AccountRecoveryCreateRequest) (*model.AccountRecoveryCreateResponse, error) {
	s.called, s.request = true, request
	return s.response, s.err
}

func (s *accountRecoveryServiceStub) Resolve(_ context.Context, _, _ string, _ bool) (repository.RecoveryResolution, error) {
	return repository.RecoveryResolved, nil
}

func performAccountRecoveryRequest(t *testing.T, stub *accountRecoveryServiceStub, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/v1/login/2fa/recovery-request", NewAccountRecoveryHandler(stub).Create)

	request := httptest.NewRequest(http.MethodPost, "/v1/login/2fa/recovery-request", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestAccountRecoveryHandlerCreatesTheRequest(t *testing.T) {
	stub := &accountRecoveryServiceStub{response: &model.AccountRecoveryCreateResponse{
		RequestID: "request-1", Status: model.AccountRecoveryPending, EmailHint: "a***a@gmail.com",
	}}

	recorder := performAccountRecoveryRequest(t, stub, `{"challenge_token":"challenge","message":"perdí el celular"}`)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	if stub.request.ChallengeToken != "challenge" || stub.request.Message != "perdí el celular" {
		t.Errorf("request = %+v", stub.request)
	}
	var body model.AccountRecoveryCreateResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if body != *stub.response {
		t.Errorf("body = %+v, want %+v", body, *stub.response)
	}
}

func TestAccountRecoveryHandlerRequiresTheChallenge(t *testing.T) {
	stub := &accountRecoveryServiceStub{}

	recorder := performAccountRecoveryRequest(t, stub, `{"message":"hola"}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if stub.called {
		t.Error("no debería llamar al servicio sin challenge_token")
	}
}

func TestAccountRecoveryHandlerMapsErrors(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
		code   string
	}{
		"mensaje largo":   {service.ErrAccountRecoveryMessageTooLong, http.StatusBadRequest, "ACCOUNT_RECOVERY_MESSAGE_TOO_LONG"},
		"desafío vencido": {service.ErrTwoFactorChallengeInvalid, http.StatusUnauthorized, "TWO_FACTOR_CHALLENGE_EXPIRED"},
		"suspendida":      {service.ErrAccountSuspended, http.StatusForbidden, "ACCOUNT_SUSPENDED"},
		"ya pendiente":    {service.ErrAccountRecoveryAlreadyPending, http.StatusConflict, "ACCOUNT_RECOVERY_ALREADY_PENDING"},
		"falla interna":   {errors.New("db caída"), http.StatusInternalServerError, "INTERNAL_SERVER_ERROR"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			recorder := performAccountRecoveryRequest(t, &accountRecoveryServiceStub{err: tc.err}, `{"challenge_token":"challenge"}`)

			if recorder.Code != tc.status {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.status)
			}
			var body model.ErrorResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if body.Error.Code != tc.code {
				t.Errorf("code = %q, want %q", body.Error.Code, tc.code)
			}
		})
	}
}
