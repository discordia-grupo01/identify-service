package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/service"
	"github.com/gin-gonic/gin"
)

type emailConfirmationServiceStub struct {
	requestErr error
	confirmErr error

	requestCalled bool
	confirmCalled bool

	lastRequest model.EmailConfirmationRequest
	lastToken   string
}

func (s *emailConfirmationServiceStub) RequestEmailConfirmation(
	_ context.Context,
	request model.EmailConfirmationRequest,
) error {
	s.requestCalled = true
	s.lastRequest = request
	return s.requestErr
}

func (s *emailConfirmationServiceStub) ConfirmEmail(
	_ context.Context,
	token string,
) error {
	s.confirmCalled = true
	s.lastToken = token
	return s.confirmErr
}

func setupEmailConfirmationRouter(
	stub *emailConfirmationServiceStub,
) *gin.Engine {
	gin.SetMode(gin.TestMode)

	router := gin.New()

	handler := NewEmailConfirmationHandler(stub)

	router.POST("/email-confirmation/request", handler.Request)
	router.POST("/email-confirmation/confirm", handler.Confirm)

	return router
}

func TestEmailConfirmationHandlerRequestSuccess(t *testing.T) {
	stub := &emailConfirmationServiceStub{}
	router := setupEmailConfirmationRouter(stub)

	body := `{"email":"ada@example.com"}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/email-confirmation/request",
		bytes.NewBufferString(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf(
			"status = %d, want %d",
			rec.Code,
			http.StatusAccepted,
		)
	}

	if !stub.requestCalled {
		t.Fatal("RequestEmailConfirmation() was not called")
	}

	if stub.lastRequest.Email != "ada@example.com" {
		t.Fatalf(
			"email = %q, want %q",
			stub.lastRequest.Email,
			"ada@example.com",
		)
	}

	var response map[string]string

	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}

	expected := "Si el correo está registrado y aún no fue confirmado, recibirás un enlace de confirmación"

	if response["message"] != expected {
		t.Fatalf(
			"message = %q, want %q",
			response["message"],
			expected,
		)
	}
}

func TestEmailConfirmationHandlerRequestInvalidJSON(t *testing.T) {
	stub := &emailConfirmationServiceStub{}
	router := setupEmailConfirmationRouter(stub)

	req := httptest.NewRequest(
		http.MethodPost,
		"/email-confirmation/request",
		bytes.NewBufferString(`{"email":`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf(
			"status = %d, want %d",
			rec.Code,
			http.StatusBadRequest,
		)
	}

	if stub.requestCalled {
		t.Fatal("service must not be called for invalid JSON")
	}
}

func TestEmailConfirmationHandlerRequestRateLimited(t *testing.T) {
	stub := &emailConfirmationServiceStub{
		requestErr: service.ErrEmailConfirmationRateLimited,
	}

	router := setupEmailConfirmationRouter(stub)

	req := httptest.NewRequest(
		http.MethodPost,
		"/email-confirmation/request",
		bytes.NewBufferString(`{"email":"ada@example.com"}`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf(
			"status = %d, want %d",
			rec.Code,
			http.StatusTooManyRequests,
		)
	}

	var response model.ErrorResponse

	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}

	if response.Error.Code != "EMAIL_CONFIRMATION_RATE_LIMITED" {
		t.Fatalf(
			"error code = %q, want EMAIL_CONFIRMATION_RATE_LIMITED",
			response.Error.Code,
		)
	}
}

func TestEmailConfirmationHandlerConfirmSuccess(t *testing.T) {
	stub := &emailConfirmationServiceStub{}
	router := setupEmailConfirmationRouter(stub)

	req := httptest.NewRequest(
		http.MethodPost,
		"/email-confirmation/confirm",
		bytes.NewBufferString(`{"token":"abc123"}`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, want %d",
			rec.Code,
			http.StatusOK,
		)
	}

	if !stub.confirmCalled {
		t.Fatal("ConfirmEmail() was not called")
	}

	if stub.lastToken != "abc123" {
		t.Fatalf(
			"token = %q, want %q",
			stub.lastToken,
			"abc123",
		)
	}

	var response map[string]string

	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}

	expected := "El correo electrónico fue confirmado correctamente"

	if response["message"] != expected {
		t.Fatalf(
			"message = %q, want %q",
			response["message"],
			expected,
		)
	}
}

func TestEmailConfirmationHandlerConfirmInvalidJSON(t *testing.T) {
	stub := &emailConfirmationServiceStub{}
	router := setupEmailConfirmationRouter(stub)

	req := httptest.NewRequest(
		http.MethodPost,
		"/email-confirmation/confirm",
		bytes.NewBufferString(`{"token":`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf(
			"status = %d, want %d",
			rec.Code,
			http.StatusBadRequest,
		)
	}

	if stub.confirmCalled {
		t.Fatal("service must not be called for invalid JSON")
	}
}

func TestEmailConfirmationHandlerConfirmInvalidToken(t *testing.T) {
	stub := &emailConfirmationServiceStub{
		confirmErr: service.ErrEmailConfirmationInvalid,
	}

	router := setupEmailConfirmationRouter(stub)

	req := httptest.NewRequest(
		http.MethodPost,
		"/email-confirmation/confirm",
		bytes.NewBufferString(`{"token":"invalid"}`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf(
			"status = %d, want %d",
			rec.Code,
			http.StatusBadRequest,
		)
	}

	var response model.ErrorResponse

	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}

	if response.Error.Code != "INVALID_EMAIL_CONFIRMATION" {
		t.Fatalf(
			"error code = %q, want INVALID_EMAIL_CONFIRMATION",
			response.Error.Code,
		)
	}
}

func TestEmailConfirmationHandlerConfirmAlreadyUsed(t *testing.T) {
	stub := &emailConfirmationServiceStub{
		confirmErr: service.ErrEmailConfirmationAlreadyUsed,
	}

	router := setupEmailConfirmationRouter(stub)

	req := httptest.NewRequest(
		http.MethodPost,
		"/email-confirmation/confirm",
		bytes.NewBufferString(`{"token":"already-used"}`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf(
			"status = %d, want %d",
			rec.Code,
			http.StatusBadRequest,
		)
	}

	var response model.ErrorResponse

	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}

	if response.Error.Code != "INVALID_EMAIL_CONFIRMATION" {
		t.Fatalf(
			"error code = %q, want INVALID_EMAIL_CONFIRMATION",
			response.Error.Code,
		)
	}
}
