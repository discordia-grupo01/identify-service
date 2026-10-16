package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func TestRequireAuthAcceptsValidToken(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	request := httptest.NewRequest(http.MethodGet, "/v1/servers/7", nil)
	request.Header.Set("Authorization", "Bearer "+signedToken(t, "550e8400-e29b-41d4-a716-446655440000", time.Now().Add(time.Hour)))
	recorder := httptest.NewRecorder()

	router := gin.New()
	router.GET("/v1/servers/7", RequireAuth(nil), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}

func TestRequireAuthSetsUserIDInContext(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	request := httptest.NewRequest(http.MethodGet, "/v1/private", nil)
	request.Header.Set("Authorization", "Bearer "+signedToken(t, "550e8400-e29b-41d4-a716-446655440000", time.Now().Add(time.Hour)))
	recorder := httptest.NewRecorder()

	var capturedUserID any
	router := gin.New()
	router.GET("/v1/private", RequireAuth(nil), func(c *gin.Context) {
		capturedUserID, _ = c.Get(UserIDKey)
		c.Status(http.StatusNoContent)
	})
	router.ServeHTTP(recorder, request)

	if capturedUserID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("UserIDKey in context = %v, want the token subject", capturedUserID)
	}
}

func TestRequireAuthRejectsExpiredTokenAndPreservesPendingAction(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	request := httptest.NewRequest(http.MethodPost, "/v1/messages?draft=1", nil)
	request.Header.Set("Authorization", "Bearer "+signedToken(t, "550e8400-e29b-41d4-a716-446655440000", time.Now().Add(-time.Minute)))
	recorder := httptest.NewRecorder()

	router := gin.New()
	router.POST("/v1/messages", RequireAuth(nil), func(c *gin.Context) {
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

func TestRequireAuthRejectsTokenWithoutSubjectClaim(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
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
	router.GET("/v1/private", RequireAuth(nil), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestRequireAuthRejectsMalformedToken(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")

	request := httptest.NewRequest(http.MethodGet, "/v1/private", nil)
	request.Header.Set("Authorization", "Bearer invalid-token")
	recorder := httptest.NewRecorder()

	router := gin.New()
	router.GET("/v1/private", RequireAuth(nil), func(c *gin.Context) {
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
	router.GET("/v1/private", RequireAuth(nil), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestRequireAuthRejectsInvalidSignature(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "550e8400-e29b-41d4-a716-446655440000",
		"jti": "some-token",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	signed, err := token.SignedString([]byte("wrong-secret"))
	if err != nil {
		t.Fatalf("SignedString() error: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/private", nil)
	request.Header.Set("Authorization", "Bearer "+signed)
	recorder := httptest.NewRecorder()

	router := gin.New()
	router.GET("/v1/private", RequireAuth(nil), func(c *gin.Context) {
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
		"sub": "550e8400-e29b-41d4-a716-446655440000",
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
	router.GET("/v1/private", RequireAuth(nil), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func signedToken(t *testing.T, userID string, expiresAt time.Time) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID,
		"jti": "token-id",
		"exp": expiresAt.Unix(),
	})
	signed, err := token.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("SignedString() error: %v", err)
	}
	return signed
}
