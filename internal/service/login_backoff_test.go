package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
)

// The delay doubles from the third failed attempt on and stops at the cap.
func TestLoginBackoff(t *testing.T) {
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{0, 0},
		{loginBackoffAfter - 1, 0},
		{loginBackoffAfter, loginBackoffBase},
		{loginBackoffAfter + 1, 2 * loginBackoffBase},
		{loginBackoffAfter + 2, 4 * loginBackoffBase},
		{loginBackoffAfter + 3, loginBackoffMax},
		{loginBackoffAfter + 20, loginBackoffMax},
	}
	for _, tc := range cases {
		if got := loginBackoff(tc.failures); got != tc.want {
			t.Errorf("loginBackoff(%d) = %v, want %v", tc.failures, got, tc.want)
		}
	}
}

// With failures behind it, a login waits before checking the password. If
// the client gives up during that wait, the login ends there.
func TestLoginWaitsTheBackoffBeforeCheckingThePassword(t *testing.T) {
	attempts := newLoginAttemptRepositoryStub()
	attempts.failures[loginAttemptKey("ada@example.com", "203.0.113.1")] = loginBackoffAfter
	service := NewAuthService(&userRepositoryStub{}, newRefreshTokenRepositoryStub(), WithLoginAttempts(attempts))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.Login(ctx, model.LoginRequest{Email: "ada@example.com", Password: "wrong", IP: "203.0.113.1"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Login() error = %v, want context.Canceled from the backoff wait", err)
	}
}

func TestLoginCountsFailuresInsideTheirWindows(t *testing.T) {
	attempts := newLoginAttemptRepositoryStub()
	service := NewAuthService(&userRepositoryStub{}, newRefreshTokenRepositoryStub(), WithLoginAttempts(attempts))

	before := time.Now().UTC()
	_, _ = service.Login(context.Background(), model.LoginRequest{Email: "ada@example.com", Password: "wrong", IP: "203.0.113.1"})
	after := time.Now().UTC()

	checkWindow := func(name string, since time.Time, window time.Duration) {
		t.Helper()
		if since.Before(before.Add(-window)) || since.After(after.Add(-window)) {
			t.Errorf("%s window starts at %v, want %v before now", name, since, window)
		}
	}
	checkWindow("email+ip", attempts.pairSince, loginPairWindow)
	checkWindow("email", attempts.emailSince, loginEmailWindow)
}
