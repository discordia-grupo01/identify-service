package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

type sessionRepositoryStub struct {
	active map[string]bool
	err    error
}

func (r *sessionRepositoryStub) Create(_ context.Context, tokenID string, _ int64, _ time.Time) error {
	if r.active == nil {
		r.active = make(map[string]bool)
	}
	r.active[tokenID] = true
	return nil
}

func (r *sessionRepositoryStub) IsActive(_ context.Context, tokenID string) (bool, error) {
	if r.err != nil {
		return false, r.err
	}
	return r.active[tokenID], nil
}

func (r *sessionRepositoryStub) Revoke(_ context.Context, tokenID string) error {
	if r.active == nil || !r.active[tokenID] {
		return repository.ErrSessionNotFound
	}
	r.active[tokenID] = false
	return nil
}

var _ repository.SessionRepository = (*sessionRepositoryStub)(nil)

func TestRequireAuthAcceptsActiveToken(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	sessionRepo := &sessionRepositoryStub{active: map[string]bool{"active-token": true}}
	request := httptest.NewRequest(http.MethodGet, "/v1/servers/7", nil)
	request.Header.Set("Authorization", "Bearer "+signedToken(t, "active-token", time.Now().Add(time.Hour)))
	recorder := httptest.NewRecorder()

	router := gin.New()
	router.GET("/v1/servers/7", RequireAuth(sessionRepo), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}

func TestRequireAuthRejectsExpiredTokenAndPreservesPendingAction(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	sessionRepo := &sessionRepositoryStub{active: map[string]bool{"expired-token": true}}
	request := httptest.NewRequest(http.MethodPost, "/v1/messages?draft=1", nil)
	request.Header.Set("Authorization", "Bearer "+signedToken(t, "expired-token", time.Now().Add(-time.Minute)))
	recorder := httptest.NewRecorder()

	router := gin.New()
	router.POST("/v1/messages", RequireAuth(sessionRepo), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	var response model.ErrorResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error.Code != "SESSION_EXPIRED" {
		t.Fatalf("error code = %q, want SESSION_EXPIRED", response.Error.Code)
	}
	if response.Error.Details["redirect_to"] != "/v1/login" {
		t.Fatalf("redirect_to = %v, want /v1/login", response.Error.Details["redirect_to"])
	}
	pendingAction, ok := response.Error.Details["pending_action"].(map[string]any)
	if !ok {
		t.Fatalf("pending_action = %T, want object", response.Error.Details["pending_action"])
	}
	if pendingAction["method"] != http.MethodPost || pendingAction["path"] != "/v1/messages?draft=1" {
		t.Fatalf("pending_action = %+v", pendingAction)
	}
}

func TestRequireAuthRejectsAbsentAndRevokedTokens(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	tests := []struct {
		name      string
		tokenID   string
		available map[string]bool
	}{
		{name: "absent token", tokenID: "absent-token", available: map[string]bool{}},
		{name: "revoked token", tokenID: "revoked-token", available: map[string]bool{"revoked-token": false}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sessionRepo := &sessionRepositoryStub{active: test.available}
			request := httptest.NewRequest(http.MethodGet, "/v1/private", nil)
			request.Header.Set("Authorization", "Bearer "+signedToken(t, test.tokenID, time.Now().Add(time.Hour)))
			recorder := httptest.NewRecorder()

			router := gin.New()
			router.GET("/v1/private", RequireAuth(sessionRepo), func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})
			router.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestRequireAuthRejectsMalformedTokens(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")

	request := httptest.NewRequest(http.MethodGet, "/v1/private", nil)
	request.Header.Set("Authorization", "Bearer invalid-token")
	recorder := httptest.NewRecorder()

	router := gin.New()
	router.GET("/v1/private", RequireAuth(&sessionRepositoryStub{}), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestRequireAuthRejectsMissingAuthorizationHeader(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")

	request := httptest.NewRequest(http.MethodGet, "/v1/private", nil)
	recorder := httptest.NewRecorder()

	router := gin.New()
	router.GET("/v1/private", RequireAuth(&sessionRepositoryStub{}), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestRequireAuthRejectsInvalidSignature(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")

	request := httptest.NewRequest(http.MethodGet, "/v1/private", nil)
	request.Header.Set("Authorization", "Bearer invalid-token")
	recorder := httptest.NewRecorder()

	router := gin.New()
	router.GET("/v1/private", RequireAuth(&sessionRepositoryStub{}), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestRequireAuthRejectsWrongSigningMethod(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	token := jwt.NewWithClaims(jwt.SigningMethodHS512, jwt.MapClaims{
		"sub": int64(42),
		"jti": "wrong-method-token",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	signed, err := token.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("SignedString() error: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/private", nil)
	request.Header.Set("Authorization", "Bearer "+signed)
	recorder := httptest.NewRecorder()

	router := gin.New()
	router.GET("/v1/private", RequireAuth(&sessionRepositoryStub{}), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func signedToken(t *testing.T, tokenID string, expiresAt time.Time) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": int64(42),
		"jti": tokenID,
		"exp": expiresAt.Unix(),
	})
	signed, err := token.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("SignedString() error: %v", err)
	}
	return signed
}
