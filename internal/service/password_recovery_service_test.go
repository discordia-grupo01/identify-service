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
	count     int
	countErr  error
	createErr error
	resetErr  error
	created   *model.PasswordRecovery
	resetHash string
	newHash   string
}

func (r *passwordRecoveryRepositoryStub) Create(_ context.Context, recovery *model.PasswordRecovery) error {
	if r.createErr != nil {
		return r.createErr
	}
	r.created = recovery
	return nil
}

func (r *passwordRecoveryRepositoryStub) CountRecentByEmail(_ context.Context, _ string, _ time.Time) (int, error) {
	return r.count, r.countErr
}

func (r *passwordRecoveryRepositoryStub) ResetPassword(_ context.Context, tokenHash string, passwordHash string) error {
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
	users := &userRepositoryStub{existingUser: &model.User{ID: 7, Name: "Ada", Email: "ada@example.com"}}
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
