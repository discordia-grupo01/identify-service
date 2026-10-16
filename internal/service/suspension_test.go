package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
)

func TestIssueTokenPairRejectsASuspendedUserWithoutPersistingAToken(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	refreshRepo := newRefreshTokenRepositoryStub()
	suspendedAt := time.Now().UTC()
	user := &model.User{ID: "550e8400-e29b-41d4-a716-446655440000", SuspendedAt: &suspendedAt}

	auth, err := issueTokenPair(context.Background(), refreshRepo, user, "family-1")

	if !errors.Is(err, ErrAccountSuspended) {
		t.Fatalf("issueTokenPair() error = %v, want ErrAccountSuspended", err)
	}
	if auth != nil || len(refreshRepo.tokens) != 0 {
		t.Fatalf("issueTokenPair() = %+v with %d stored tokens, want nothing issued or stored", auth, len(refreshRepo.tokens))
	}
}

func TestAuthServiceLoginRejectsASuspendedUser(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	refreshRepo := newRefreshTokenRepositoryStub()
	repo, _ := loginTestUser(t, refreshRepo)
	suspendedAt := time.Now().UTC()
	repo.existingUser.SuspendedAt = &suspendedAt

	result, err := NewAuthService(repo, refreshRepo).Login(context.Background(), model.LoginRequest{
		Email:    "ada@example.com",
		Password: "Secure123",
	})

	if !errors.Is(err, ErrAccountSuspended) {
		t.Fatalf("Login() error = %v, want ErrAccountSuspended", err)
	}
	if result != nil {
		t.Fatalf("Login() result = %+v, want nil", result)
	}
}

// Con la contraseña equivocada no se revela que la cuenta está suspendida.
func TestAuthServiceLoginWithAWrongPasswordDoesNotRevealSuspension(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	refreshRepo := newRefreshTokenRepositoryStub()
	repo, _ := loginTestUser(t, refreshRepo)
	suspendedAt := time.Now().UTC()
	repo.existingUser.SuspendedAt = &suspendedAt

	_, err := NewAuthService(repo, refreshRepo).Login(context.Background(), model.LoginRequest{
		Email:    "ada@example.com",
		Password: "WrongPass1",
	})

	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
	}
}

// Al suspender se revocan todos los refresh tokens: el refresh debe responder
// ACCOUNT_SUSPENDED y no "token reutilizado".
func TestAuthServiceRefreshRejectsASuspendedUser(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	refreshRepo := newRefreshTokenRepositoryStub()
	repo, login := loginTestUser(t, refreshRepo)
	suspendedAt := time.Now().UTC()
	repo.existingUser.SuspendedAt = &suspendedAt
	refreshRepo.tokens[hashToken(login.RefreshToken)].revokedAt = &suspendedAt

	_, err := NewAuthService(repo, refreshRepo).Refresh(context.Background(), login.RefreshToken)

	if !errors.Is(err, ErrAccountSuspended) {
		t.Fatalf("Refresh() error = %v, want ErrAccountSuspended (not a reuse error)", err)
	}
}

func TestProfileServiceReturnsNotFoundForASuspendedUsersPublicProfile(t *testing.T) {
	suspendedAt := time.Now().UTC()
	repo := &userRepositoryStub{existingUser: &model.User{ID: "user-id", Name: "Ada", SuspendedAt: &suspendedAt}}

	_, err := NewProfileService(repo, &profileImageStorageStub{}, &serverMembershipRepositoryStub{}).GetPublicProfile(context.Background(), "viewer-id", "user-id")

	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("GetPublicProfile() error = %v, want %v", err, ErrUserNotFound)
	}
}
