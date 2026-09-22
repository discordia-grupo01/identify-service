package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/utils/email"
	"golang.org/x/crypto/bcrypt"
)

type emailConfirmationRepositoryStub struct {
	records       map[string]*model.EmailConfirmation
	createErr     error
	getErr        error
	markUsedErr   error
	count         int
	countErr      error
	invalidated   bool
	invalidateErr error
}

func newEmailConfirmationRepositoryStub() *emailConfirmationRepositoryStub {
	return &emailConfirmationRepositoryStub{
		records: make(map[string]*model.EmailConfirmation),
	}
}

func (r *emailConfirmationRepositoryStub) Create(_ context.Context, confirmation *model.EmailConfirmation) error {
	if r.createErr != nil {
		return r.createErr
	}
	if confirmation == nil {
		return errors.New("nil confirmation")
	}

	if r.records == nil {
		r.records = make(map[string]*model.EmailConfirmation)
	}

	r.records[confirmation.TokenHash] = confirmation
	return nil
}

func (r *emailConfirmationRepositoryStub) CountRecentByEmail(
	_ context.Context,
	_ string,
	_ time.Time,
) (int, error) {
	return r.count, r.countErr
}

func (r *emailConfirmationRepositoryStub) GetByHash(
	_ context.Context,
	tokenHash string,
) (*model.EmailConfirmation, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}

	record, ok := r.records[tokenHash]
	if !ok {
		return nil, repository.ErrEmailConfirmationNotFound
	}

	return record, nil
}

func (r *emailConfirmationRepositoryStub) MarkUsed(
	_ context.Context,
	tokenHash string,
) error {
	if r.markUsedErr != nil {
		return r.markUsedErr
	}

	record, ok := r.records[tokenHash]
	if !ok {
		return repository.ErrEmailConfirmationNotFound
	}

	now := time.Now().UTC()
	record.UsedAt = &now

	return nil
}

func (r *emailConfirmationRepositoryStub) InvalidatePrevious(
	_ context.Context,
	userID string,
	_ string,
) error {
	if r.invalidateErr != nil {
		return r.invalidateErr
	}

	r.invalidated = true

	for _, record := range r.records {
		if record.UserID == userID && record.UsedAt == nil {
			now := time.Now().UTC()
			record.UsedAt = &now
		}
	}

	return nil
}

type emailConfirmationMailerStub struct {
	sent []email.EmailConfirmationRequest
	err  error
}

func (m *emailConfirmationMailerStub) SendEmailConfirmationEmail(
	request email.EmailConfirmationRequest,
) error {
	if m.err != nil {
		return m.err
	}

	m.sent = append(m.sent, request)
	return nil
}
func TestEmailConfirmationServiceConfirmEmailMarksUserVerified(t *testing.T) {
	user := &model.User{ID: "user-1", Email: "ada@example.com", EmailVerified: false, PasswordHash: sql.NullString{String: "hash", Valid: true}}
	repo := newEmailConfirmationRepositoryStub()
	now := time.Now().UTC()
	token := "plain-token"
	record := &model.EmailConfirmation{UserID: user.ID, Email: user.Email, TokenHash: HashEmailConfirmationToken(token), ExpiresAt: now.Add(24 * time.Hour), UsedAt: nil}
	repo.records[record.TokenHash] = record
	userRepo := &userRepositoryStub{existingUser: user}
	mailer := &emailConfirmationMailerStub{}
	service := NewEmailConfirmationService(repo, userRepo, mailer, "https://example.com/confirm")

	if err := service.ConfirmEmail(context.Background(), token); err != nil {
		t.Fatalf("ConfirmEmail() error = %v", err)
	}
	if !userRepo.existingUser.EmailVerified {
		t.Fatal("ConfirmEmail() did not mark user as verified")
	}
	if record.UsedAt == nil {
		t.Fatal("ConfirmEmail() must mark token as used")
	}
}

func TestEmailConfirmationServiceRejectsConsumedTokenAndReturnsGenericError(t *testing.T) {
	user := &model.User{ID: "user-1", Email: "ada@example.com", EmailVerified: false, PasswordHash: sql.NullString{String: "hash", Valid: true}}
	repo := newEmailConfirmationRepositoryStub()
	now := time.Now().UTC()
	token := "plain-token"
	usedAt := now.Add(-time.Hour)
	record := &model.EmailConfirmation{UserID: user.ID, Email: user.Email, TokenHash: HashEmailConfirmationToken(token), ExpiresAt: now.Add(time.Hour), UsedAt: &usedAt}
	repo.records[record.TokenHash] = record
	userRepo := &userRepositoryStub{existingUser: user}
	service := NewEmailConfirmationService(repo, userRepo, &emailConfirmationMailerStub{}, "https://example.com/confirm")

	if err := service.ConfirmEmail(context.Background(), token); !errors.Is(err, ErrEmailConfirmationAlreadyUsed) {
		t.Fatalf("ConfirmEmail() error = %v, want ErrEmailConfirmationAlreadyUsed", err)
	}
}

func TestAuthServiceLoginRejectsUnverifiedEmail(t *testing.T) {
	passwordHash, err := HashPasswordForTest("Secure123")
	if err != nil {
		t.Fatalf("HashPasswordForTest() error = %v", err)
	}
	repo := &userRepositoryStub{existingUser: &model.User{
		ID:            "user-1",
		Email:         "ada@example.com",
		PasswordHash:  sql.NullString{String: string(passwordHash), Valid: true},
		EmailVerified: false,
	}}
	response, err := NewAuthService(repo, nil).Login(context.Background(), model.LoginRequest{Email: "ada@example.com", Password: "Secure123"})
	if !errors.Is(err, ErrEmailNotVerified) {
		t.Fatalf("Login() error = %v, want ErrEmailNotVerified", err)
	}
	if response != nil {
		t.Fatalf("Login() response = %+v, want nil", response)
	}
}

func HashPasswordForTest(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

func TestEmailConfirmationServiceRejectsUnknownToken(t *testing.T) {
	user := &model.User{
		ID:            "user-1",
		Email:         "ada@example.com",
		EmailVerified: false,
	}

	repo := newEmailConfirmationRepositoryStub()
	userRepo := &userRepositoryStub{existingUser: user}

	service := NewEmailConfirmationService(
		repo,
		userRepo,
		&emailConfirmationMailerStub{},
		"https://example.com/confirm",
	)

	err := service.ConfirmEmail(
		context.Background(),
		"token-that-does-not-exist",
	)

	if !errors.Is(err, ErrEmailConfirmationInvalid) {
		t.Fatalf(
			"ConfirmEmail() error = %v, want ErrEmailConfirmationInvalid",
			err,
		)
	}

	if user.EmailVerified {
		t.Fatal("unknown token must not verify the user")
	}
}

func TestEmailConfirmationServiceRejectsExpiredToken(t *testing.T) {
	user := &model.User{
		ID:            "user-1",
		Email:         "ada@example.com",
		EmailVerified: false,
	}

	repo := newEmailConfirmationRepositoryStub()

	token := "expired-token"

	record := &model.EmailConfirmation{
		UserID:    user.ID,
		Email:     user.Email,
		TokenHash: HashEmailConfirmationToken(token),
		ExpiresAt: time.Now().UTC().Add(-time.Hour),
		UsedAt:    nil,
	}

	repo.records[record.TokenHash] = record

	userRepo := &userRepositoryStub{existingUser: user}

	service := NewEmailConfirmationService(
		repo,
		userRepo,
		&emailConfirmationMailerStub{},
		"https://example.com/confirm",
	)

	err := service.ConfirmEmail(context.Background(), token)

	if !errors.Is(err, ErrEmailConfirmationInvalid) {
		t.Fatalf(
			"ConfirmEmail() error = %v, want ErrEmailConfirmationInvalid",
			err,
		)
	}

	if user.EmailVerified {
		t.Fatal("expired token must not verify the user")
	}

	if record.UsedAt != nil {
		t.Fatal("expired token must not be marked as used")
	}
}

func TestEmailConfirmationServiceRejectsEmptyToken(t *testing.T) {
	repo := newEmailConfirmationRepositoryStub()
	userRepo := &userRepositoryStub{}

	service := NewEmailConfirmationService(
		repo,
		userRepo,
		&emailConfirmationMailerStub{},
		"https://example.com/confirm",
	)

	err := service.ConfirmEmail(context.Background(), "")

	if !errors.Is(err, ErrEmailConfirmationInvalid) {
		t.Fatalf(
			"ConfirmEmail() error = %v, want ErrEmailConfirmationInvalid",
			err,
		)
	}
}

func TestEmailConfirmationServiceRequestSendsEmail(t *testing.T) {
	user := &model.User{
		ID:            "user-1",
		Email:         "ada@example.com",
		Name:          "Ada",
		EmailVerified: false,
	}

	repo := newEmailConfirmationRepositoryStub()
	userRepo := &userRepositoryStub{existingUser: user}
	mailer := &emailConfirmationMailerStub{}

	service := NewEmailConfirmationService(
		repo,
		userRepo,
		mailer,
		"https://example.com/confirm",
	)

	err := service.RequestEmailConfirmation(
		context.Background(),
		model.EmailConfirmationRequest{
			Email: user.Email,
		},
	)

	if err != nil {
		t.Fatalf("RequestEmailConfirmation() error = %v", err)
	}

	if !repo.invalidated {
		t.Fatal("RequestEmailConfirmation() must invalidate previous tokens")
	}

	if len(repo.records) != 1 {
		t.Fatalf(
			"created confirmations = %d, want 1",
			len(repo.records),
		)
	}

	if len(mailer.sent) != 1 {
		t.Fatalf(
			"sent emails = %d, want 1",
			len(mailer.sent),
		)
	}

	if mailer.sent[0].Email != user.Email {
		t.Fatalf(
			"sent email = %q, want %q",
			mailer.sent[0].Email,
			user.Email,
		)
	}
}

func TestEmailConfirmationServiceRequestRateLimited(t *testing.T) {
	user := &model.User{
		ID:            "user-1",
		Email:         "ada@example.com",
		EmailVerified: false,
	}

	repo := newEmailConfirmationRepositoryStub()
	repo.count = 10

	userRepo := &userRepositoryStub{existingUser: user}
	mailer := &emailConfirmationMailerStub{}

	service := NewEmailConfirmationService(
		repo,
		userRepo,
		mailer,
		"https://example.com/confirm",
	)

	err := service.RequestEmailConfirmation(
		context.Background(),
		model.EmailConfirmationRequest{
			Email: user.Email,
		},
	)

	if !errors.Is(err, ErrEmailConfirmationRateLimited) {
		t.Fatalf(
			"RequestEmailConfirmation() error = %v, want ErrEmailConfirmationRateLimited",
			err,
		)
	}

	if len(mailer.sent) != 0 {
		t.Fatal("rate-limited request must not send an email")
	}

	if len(repo.records) != 0 {
		t.Fatal("rate-limited request must not create a token")
	}
}

func TestEmailConfirmationServiceRequestPropagatesCountError(t *testing.T) {
	user := &model.User{
		ID:            "user-1",
		Email:         "ada@example.com",
		EmailVerified: false,
	}

	expectedErr := errors.New("count failed")

	repo := newEmailConfirmationRepositoryStub()
	repo.countErr = expectedErr

	userRepo := &userRepositoryStub{existingUser: user}

	service := NewEmailConfirmationService(
		repo,
		userRepo,
		&emailConfirmationMailerStub{},
		"https://example.com/confirm",
	)

	err := service.RequestEmailConfirmation(
		context.Background(),
		model.EmailConfirmationRequest{
			Email: user.Email,
		},
	)

	if !errors.Is(err, expectedErr) {
		t.Fatalf(
			"RequestEmailConfirmation() error = %v, want %v",
			err,
			expectedErr,
		)
	}
}

func TestEmailConfirmationServiceRequestDoesNotSendForVerifiedUser(t *testing.T) {
	user := &model.User{
		ID:            "user-1",
		Email:         "ada@example.com",
		EmailVerified: true,
	}

	repo := newEmailConfirmationRepositoryStub()
	userRepo := &userRepositoryStub{existingUser: user}
	mailer := &emailConfirmationMailerStub{}

	service := NewEmailConfirmationService(
		repo,
		userRepo,
		mailer,
		"https://example.com/confirm",
	)

	err := service.RequestEmailConfirmation(
		context.Background(),
		model.EmailConfirmationRequest{
			Email: user.Email,
		},
	)

	if err != nil {
		t.Fatalf("RequestEmailConfirmation() error = %v", err)
	}

	if len(mailer.sent) != 0 {
		t.Fatal("verified user must not receive another confirmation email")
	}
}

func TestEmailConfirmationServiceRequestUnknownEmailDoesNotRevealUser(t *testing.T) {
	repo := newEmailConfirmationRepositoryStub()
	userRepo := &userRepositoryStub{
		existingUser: nil,
	}
	mailer := &emailConfirmationMailerStub{}

	service := NewEmailConfirmationService(
		repo,
		userRepo,
		mailer,
		"https://example.com/confirm",
	)

	err := service.RequestEmailConfirmation(
		context.Background(),
		model.EmailConfirmationRequest{
			Email: "unknown@example.com",
		},
	)

	if err != nil {
		t.Fatalf(
			"RequestEmailConfirmation() error = %v, want nil",
			err,
		)
	}

	if len(mailer.sent) != 0 {
		t.Fatal("unknown user must not receive an email")
	}
}
