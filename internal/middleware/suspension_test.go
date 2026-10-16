package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/discordia-grupo01/identify-service/internal/model"
)

type suspensionCheckerStub struct {
	suspended map[string]bool
	err       error
	calls     int
}

func (s *suspensionCheckerStub) IsSuspended(_ context.Context, userID string) (bool, error) {
	s.calls++
	return s.suspended[userID], s.err
}

const suspensionTestUserID = "550e8400-e29b-41d4-a716-446655440000"

func serveWithChecker(t *testing.T, checker SuspensionChecker) *httptest.ResponseRecorder {
	t.Helper()
	t.Setenv("JWT_SECRET", "test-secret")
	request := httptest.NewRequest(http.MethodGet, "/v1/private", nil)
	request.Header.Set("Authorization", "Bearer "+signedToken(t, suspensionTestUserID, time.Now().Add(time.Hour)))
	recorder := httptest.NewRecorder()

	router := gin.New()
	router.GET("/v1/private", RequireAuth(checker), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestRequireAuthRejectsASuspendedUserWithAValidToken(t *testing.T) {
	recorder := serveWithChecker(t, &suspensionCheckerStub{suspended: map[string]bool{suspensionTestUserID: true}})

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
	var body model.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error.Code != "ACCOUNT_SUSPENDED" {
		t.Fatalf("error code = %q, want ACCOUNT_SUSPENDED", body.Error.Code)
	}
}

func TestRequireAuthLetsAnActiveUserThrough(t *testing.T) {
	recorder := serveWithChecker(t, &suspensionCheckerStub{})

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}

func TestRequireAuthFailsClosedWhenTheStatusCannotBeChecked(t *testing.T) {
	recorder := serveWithChecker(t, &suspensionCheckerStub{err: errors.New("db down")})

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestSuspensionCacheServesRepeatedLookupsWithinTheTTL(t *testing.T) {
	stub := &suspensionCheckerStub{}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	cache := NewSuspensionCache(stub, 5*time.Second)
	cache.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if suspended, err := cache.IsSuspended(context.Background(), suspensionTestUserID); err != nil || suspended {
			t.Fatalf("IsSuspended() = %v, %v; want false, nil", suspended, err)
		}
	}
	if stub.calls != 1 {
		t.Fatalf("checker calls = %d, want 1 within the TTL", stub.calls)
	}

	now = now.Add(6 * time.Second)
	stub.suspended = map[string]bool{suspensionTestUserID: true}
	if suspended, _ := cache.IsSuspended(context.Background(), suspensionTestUserID); !suspended || stub.calls != 2 {
		t.Fatalf("after the TTL: suspended = %v, calls = %d; want a fresh lookup that sees the suspension", suspended, stub.calls)
	}
}

func TestSuspensionCacheInvalidateForcesAFreshLookup(t *testing.T) {
	stub := &suspensionCheckerStub{}
	cache := NewSuspensionCache(stub, time.Hour)
	if suspended, _ := cache.IsSuspended(context.Background(), suspensionTestUserID); suspended {
		t.Fatal("user should start active")
	}

	stub.suspended = map[string]bool{suspensionTestUserID: true}
	if suspended, _ := cache.IsSuspended(context.Background(), suspensionTestUserID); suspended {
		t.Fatal("cached value should still be served before Invalidate")
	}
	cache.Invalidate(suspensionTestUserID)

	if suspended, _ := cache.IsSuspended(context.Background(), suspensionTestUserID); !suspended {
		t.Fatal("after Invalidate the new suspended state must be visible immediately")
	}
}

func TestSuspensionCacheDoesNotCacheErrors(t *testing.T) {
	stub := &suspensionCheckerStub{err: errors.New("db down")}
	cache := NewSuspensionCache(stub, time.Hour)

	if _, err := cache.IsSuspended(context.Background(), suspensionTestUserID); err == nil {
		t.Fatal("IsSuspended() error = nil, want the checker error")
	}
	stub.err = nil
	if suspended, err := cache.IsSuspended(context.Background(), suspensionTestUserID); err != nil || suspended || stub.calls != 2 {
		t.Fatalf("after recovery: suspended = %v, err = %v, calls = %d; want a fresh successful lookup", suspended, err, stub.calls)
	}
}
