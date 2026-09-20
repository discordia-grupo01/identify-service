package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/utils/email"
	"golang.org/x/crypto/bcrypt"
)

type passwordRecoveryRepositoryStub struct {
	count      int
	countErr   error
	createErr  error
	resetErr   error
	created    *model.PasswordRecovery
	resetHash  string
	newHash    string
	resetCalls int
	countEmail string
	countSince time.Time
}

func (r *passwordRecoveryRepositoryStub) Create(_ context.Context, recovery *model.PasswordRecovery) error {
	if r.createErr != nil {
		return r.createErr
	}
	r.created = recovery
	return nil
}

func (r *passwordRecoveryRepositoryStub) CountRecentByEmail(_ context.Context, email string, since time.Time) (int, error) {
	r.countEmail = email
	r.countSince = since
	return r.count, r.countErr
}

func (r *passwordRecoveryRepositoryStub) ResetPassword(_ context.Context, tokenHash string, passwordHash string) error {
	r.resetCalls++
	r.resetHash = tokenHash
	r.newHash = passwordHash
	return r.resetErr
}

type passwordRecoveryMailerStub struct {
	request email.PasswordResetRequest
	called  bool
	err     error
}

func (m *passwordRecoveryMailerStub) SendPasswordResetEmail(request email.PasswordResetRequest) error {
	m.called = true
	m.request = request
	return m.err
}

func newPasswordRecoveryTestService(repo *passwordRecoveryRepositoryStub, users *userRepositoryStub, mailer *passwordRecoveryMailerStub) *PasswordRecoveryServiceImpl {
	service := NewPasswordRecoveryService(repo, users, mailer).(*PasswordRecoveryServiceImpl)
	service.clock = func() time.Time { return time.Date(2026, time.September, 10, 12, 0, 0, 0, time.UTC) }
	service.resetBaseURL = "https://discordia.test/reset-password"
	service.expiration = time.Hour
	service.recoveryLimit = 3
	return service
}

func TestRecoverPasswordCreatesSingleUseExpiringTokenAndSendsEmail(t *testing.T) {
	repo := &passwordRecoveryRepositoryStub{}
	users := &userRepositoryStub{existingUser: &model.User{ID: "550e8400-e29b-41d4-a716-446655440007", Name: "Ada", Email: "ada@example.com"}}
	mailer := &passwordRecoveryMailerStub{}
	service := newPasswordRecoveryTestService(repo, users, mailer)

	if err := service.RecoverPassword(context.Background(), model.PasswordRecoveryRequest{Email: "ada@example.com"}); err != nil {
		t.Fatalf("RecoverPassword() error = %v", err)
	}
	if repo.created == nil || repo.created.TokenHash == "" {
		t.Fatal("recovery token hash was not persisted")
	}
	if len(repo.created.TokenHash) != 64 {
		t.Fatalf("token hash length = %d, want 64", len(repo.created.TokenHash))
	}
	if repo.created.ExpiresAt.Sub(service.clock()) != time.Hour {
		t.Fatalf("token expiration = %v, want one hour", repo.created.ExpiresAt.Sub(service.clock()))
	}
	if !mailer.called || mailer.request.Email != "ada@example.com" {
		t.Fatalf("unexpected email request: %+v", mailer.request)
	}
	if !strings.Contains(mailer.request.ResetURL, "token=") {
		t.Fatalf("reset URL does not contain token: %s", mailer.request.ResetURL)
	}
	if repo.countEmail != "ada@example.com" || !repo.countSince.Equal(service.clock().Add(-time.Hour)) {
		t.Fatalf("unexpected rate-limit query: email=%q since=%v", repo.countEmail, repo.countSince)
	}
}

func TestRecoverPasswordDoesNotRevealUnknownEmail(t *testing.T) {
	repo := &passwordRecoveryRepositoryStub{}
	users := &userRepositoryStub{}
	mailer := &passwordRecoveryMailerStub{}
	service := newPasswordRecoveryTestService(repo, users, mailer)

	if err := service.RecoverPassword(context.Background(), model.PasswordRecoveryRequest{Email: "unknown@example.com"}); err != nil {
		t.Fatalf("RecoverPassword() error = %v, want nil", err)
	}
	if repo.created != nil || mailer.called {
		t.Fatal("unknown email must not create a token or send an email")
	}
}

func TestRecoverPasswordLimitsRecentRequests(t *testing.T) {
	repo := &passwordRecoveryRepositoryStub{count: 3}
	users := &userRepositoryStub{existingUser: &model.User{Email: "ada@example.com"}}
	mailer := &passwordRecoveryMailerStub{}
	service := newPasswordRecoveryTestService(repo, users, mailer)

	if err := service.RecoverPassword(context.Background(), model.PasswordRecoveryRequest{Email: "ada@example.com"}); !errors.Is(err, ErrRecoveryRateLimited) {
		t.Fatalf("RecoverPassword() error = %v, want ErrRecoveryRateLimited", err)
	}
	if mailer.called {
		t.Fatal("rate-limited request must not send an email")
	}
}

func TestRecoverPasswordPropagatesCountErrorWithoutSideEffects(t *testing.T) {
	repo := &passwordRecoveryRepositoryStub{countErr: errors.New("database unavailable")}
	users := &userRepositoryStub{existingUser: &model.User{Email: "ada@example.com"}}
	mailer := &passwordRecoveryMailerStub{}
	service := newPasswordRecoveryTestService(repo, users, mailer)

	err := service.RecoverPassword(context.Background(), model.PasswordRecoveryRequest{Email: "ada@example.com"})
	if err == nil || !strings.Contains(err.Error(), "count password recovery requests") {
		t.Fatalf("error = %v, want wrapped count error", err)
	}
	if repo.created != nil || mailer.called {
		t.Fatal("count failure must not create a token or send an email")
	}
}

func TestRecoverPasswordPropagatesUserLookupError(t *testing.T) {
	repo := &passwordRecoveryRepositoryStub{}
	users := &userRepositoryStub{getErr: errors.New("database unavailable")}
	service := newPasswordRecoveryTestService(repo, users, &passwordRecoveryMailerStub{})

	err := service.RecoverPassword(context.Background(), model.PasswordRecoveryRequest{Email: "ada@example.com"})
	if err == nil || !strings.Contains(err.Error(), "find user by email") {
		t.Fatalf("error = %v, want wrapped user lookup error", err)
	}
}

func TestRecoverPasswordPropagatesCreateErrorWithoutSendingEmail(t *testing.T) {
	repo := &passwordRecoveryRepositoryStub{createErr: errors.New("insert failed")}
	users := &userRepositoryStub{existingUser: &model.User{ID: "550e8400-e29b-41d4-a716-446655440007", Email: "ada@example.com"}}
	mailer := &passwordRecoveryMailerStub{}
	service := newPasswordRecoveryTestService(repo, users, mailer)

	err := service.RecoverPassword(context.Background(), model.PasswordRecoveryRequest{Email: "ada@example.com"})
	if err == nil || !strings.Contains(err.Error(), "create password recovery request") {
		t.Fatalf("error = %v, want wrapped create error", err)
	}
	if mailer.called {
		t.Fatal("create failure must not send an email")
	}
}

func TestRecoverPasswordPropagatesMailerError(t *testing.T) {
	repo := &passwordRecoveryRepositoryStub{}
	users := &userRepositoryStub{existingUser: &model.User{ID: "550e8400-e29b-41d4-a716-446655440007", Email: "ada@example.com"}}
	mailer := &passwordRecoveryMailerStub{err: errors.New("SMTP unavailable")}
	service := newPasswordRecoveryTestService(repo, users, mailer)

	err := service.RecoverPassword(context.Background(), model.PasswordRecoveryRequest{Email: "ada@example.com"})
	if err == nil || !strings.Contains(err.Error(), "send password reset email") {
		t.Fatalf("error = %v, want wrapped mailer error", err)
	}
	if repo.created == nil {
		t.Fatal("token should be persisted before sending the email")
	}
}

func TestRecoverPasswordRejectsInvalidResetBaseURL(t *testing.T) {
	repo := &passwordRecoveryRepositoryStub{}
	users := &userRepositoryStub{existingUser: &model.User{ID: "550e8400-e29b-41d4-a716-446655440007", Email: "ada@example.com"}}
	mailer := &passwordRecoveryMailerStub{}
	service := newPasswordRecoveryTestService(repo, users, mailer)
	service.resetBaseURL = "://invalid"

	err := service.RecoverPassword(context.Background(), model.PasswordRecoveryRequest{Email: "ada@example.com"})
	if err == nil || !strings.Contains(err.Error(), "build password reset URL") {
		t.Fatalf("error = %v, want invalid URL error", err)
	}
	if mailer.called {
		t.Fatal("invalid URL must not send an email")
	}
}

func TestResetPasswordUpdatesPasswordAndHashesSubmittedToken(t *testing.T) {
	repo := &passwordRecoveryRepositoryStub{}
	service := newPasswordRecoveryTestService(repo, &userRepositoryStub{}, &passwordRecoveryMailerStub{})

	if err := service.ResetPassword(context.Background(), model.PasswordResetRequest{Token: "plain-token", NewPassword: "NewSecure123", ConfirmPassword: "NewSecure123"}); err != nil {
		t.Fatalf("ResetPassword() error = %v", err)
	}
	if repo.resetHash != HashResetToken("plain-token") {
		t.Fatalf("token hash = %q, want hash of submitted token", repo.resetHash)
	}
	if bcrypt.CompareHashAndPassword([]byte(repo.newHash), []byte("NewSecure123")) != nil {
		t.Fatal("new password was not stored as a valid bcrypt hash")
	}
}

func TestResetPasswordRejectsExpiredOrUsedToken(t *testing.T) {
	repo := &passwordRecoveryRepositoryStub{resetErr: repository.ErrPasswordRecoveryNotFound}
	service := newPasswordRecoveryTestService(repo, &userRepositoryStub{}, &passwordRecoveryMailerStub{})

	err := service.ResetPassword(context.Background(), model.PasswordResetRequest{Token: "expired", NewPassword: "NewSecure123", ConfirmPassword: "NewSecure123"})
	if !errors.Is(err, ErrInvalidResetToken) {
		t.Fatalf("ResetPassword() error = %v, want ErrInvalidResetToken", err)
	}
}

func TestResetPasswordRejectsMismatchedPasswordsBeforeRepository(t *testing.T) {
	repo := &passwordRecoveryRepositoryStub{}
	service := newPasswordRecoveryTestService(repo, &userRepositoryStub{}, &passwordRecoveryMailerStub{})

	err := service.ResetPassword(context.Background(), model.PasswordResetRequest{
		Token:           "token",
		NewPassword:     "NewSecure123",
		ConfirmPassword: "OtherSecure123",
	})
	if !errors.Is(err, ErrPasswordsDoNotMatch) {
		t.Fatalf("error = %v, want ErrPasswordsDoNotMatch", err)
	}
	if repo.resetCalls != 0 {
		t.Fatal("repository must not be called when passwords do not match")
	}
}

func TestResetPasswordRejectsInsecurePasswordBeforeRepository(t *testing.T) {
	repo := &passwordRecoveryRepositoryStub{}
	service := newPasswordRecoveryTestService(repo, &userRepositoryStub{}, &passwordRecoveryMailerStub{})

	err := service.ResetPassword(context.Background(), model.PasswordResetRequest{
		Token:           "token",
		NewPassword:     "weak",
		ConfirmPassword: "weak",
	})
	if !errors.Is(err, ErrPasswordInsecure) {
		t.Fatalf("error = %v, want ErrPasswordInsecure", err)
	}
	if repo.resetCalls != 0 {
		t.Fatal("repository must not be called for an insecure password")
	}
}

func TestResetPasswordRejectsEmptyTokenBeforeHashing(t *testing.T) {
	repo := &passwordRecoveryRepositoryStub{}
	service := newPasswordRecoveryTestService(repo, &userRepositoryStub{}, &passwordRecoveryMailerStub{})

	err := service.ResetPassword(context.Background(), model.PasswordResetRequest{
		NewPassword:     "NewSecure123",
		ConfirmPassword: "NewSecure123",
	})
	if !errors.Is(err, ErrInvalidResetToken) {
		t.Fatalf("error = %v, want ErrInvalidResetToken", err)
	}
	if repo.resetCalls != 0 {
		t.Fatal("repository must not be called for an empty token")
	}
}

func TestResetPasswordWrapsUnexpectedRepositoryError(t *testing.T) {
	repo := &passwordRecoveryRepositoryStub{resetErr: errors.New("database unavailable")}
	service := newPasswordRecoveryTestService(repo, &userRepositoryStub{}, &passwordRecoveryMailerStub{})

	err := service.ResetPassword(context.Background(), model.PasswordResetRequest{
		Token:           "token",
		NewPassword:     "NewSecure123",
		ConfirmPassword: "NewSecure123",
	})
	if err == nil || !strings.Contains(err.Error(), "reset password") {
		t.Fatalf("error = %v, want wrapped repository error", err)
	}
}

func TestGenerateResetTokenIsRandomAndHashIsStable(t *testing.T) {
	first, err := GenerateResetToken()
	if err != nil {
		t.Fatalf("GenerateResetToken() error = %v", err)
	}
	second, err := GenerateResetToken()
	if err != nil {
		t.Fatalf("GenerateResetToken() second error = %v", err)
	}
	if len(first) != 64 || len(second) != 64 {
		t.Fatalf("token lengths = %d and %d, want 64", len(first), len(second))
	}
	if first == second {
		t.Fatal("two generated reset tokens must not be equal")
	}
	if HashResetToken(first) != HashResetToken(first) {
		t.Fatal("hashing the same token must be stable")
	}
	if HashResetToken(first) == HashResetToken(second) {
		t.Fatal("different tokens must not have the same hash")
	}
}
