package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/service"
	"github.com/gin-gonic/gin"
)

type oauthServiceStub struct {
	response *model.AuthResponse
	err      error
	called   bool
	idToken  string
}

var _ service.OAuthService = (*oauthServiceStub)(nil)

func (s *oauthServiceStub) LoginWithGoogle(_ context.Context, idToken string) (*model.AuthResponse, error) {
	s.called = true
	s.idToken = idToken
	return s.response, s.err
}

func performGoogleLoginRequest(t *testing.T, oauthService service.OAuthService, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/oauth/google", jsonBody(body))
	request.Header.Set("Content-Type", "application/json")

	router := gin.New()
	router.POST("/v1/oauth/google", NewOAuthHandler(oauthService).LoginWithGoogle)
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestOAuthHandlerLoginWithGoogleReturnsSessionOnSuccess(t *testing.T) {
	serviceStub := &oauthServiceStub{response: &model.AuthResponse{
		User:         model.UserResponse{ID: "550e8400-e29b-41d4-a716-446655440000"},
		Token:        "signed-access-token",
		RefreshToken: "plain-refresh-token",
	}}

	recorder := performGoogleLoginRequest(t, serviceStub, `{"id_token":"raw-google-id-token"}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !serviceStub.called || serviceStub.idToken != "raw-google-id-token" {
		t.Fatalf("service received unexpected id_token: %q", serviceStub.idToken)
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

func TestOAuthHandlerLoginWithGoogleRejectsMissingIDToken(t *testing.T) {
	serviceStub := &oauthServiceStub{}

	recorder := performGoogleLoginRequest(t, serviceStub, `{}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if serviceStub.called {
		t.Fatal("service must not be called without an id_token")
	}
}

func TestOAuthHandlerLoginWithGoogleMapsProviderUnavailableToServiceUnavailable(t *testing.T) {
	serviceStub := &oauthServiceStub{err: service.ErrOAuthProviderUnavailable}

	recorder := performGoogleLoginRequest(t, serviceStub, `{"id_token":"raw-google-id-token"}`)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d (CA3: el cliente debe ofrecer login con email/contrasena)", recorder.Code, http.StatusServiceUnavailable)
	}
	var response model.ErrorResponse
	decodeJSON(t, recorder, &response)
	if response.Error.Code != "OAUTH_PROVIDER_UNAVAILABLE" {
		t.Fatalf("error code = %q, want OAUTH_PROVIDER_UNAVAILABLE", response.Error.Code)
	}
}

func TestOAuthHandlerLoginWithGoogleMapsInvalidTokenToUnauthorized(t *testing.T) {
	serviceStub := &oauthServiceStub{err: service.ErrOAuthTokenInvalid}

	recorder := performGoogleLoginRequest(t, serviceStub, `{"id_token":"raw-google-id-token"}`)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	var response model.ErrorResponse
	decodeJSON(t, recorder, &response)
	if response.Error.Code != "OAUTH_TOKEN_INVALID" {
		t.Fatalf("error code = %q, want OAUTH_TOKEN_INVALID", response.Error.Code)
	}
}
