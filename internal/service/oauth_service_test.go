package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/utils/googleoauth"
)

type oauthUserRepositoryStub struct {
	byEmail   map[string]*model.User
	byID      map[string]*model.User
	createErr error
	created   []*model.User
}

var _ repository.UserRepository = (*oauthUserRepositoryStub)(nil)

func (r *oauthUserRepositoryStub) GetByEmail(_ context.Context, email string) (*model.User, error) {
	return r.byEmail[email], nil
}

func (r *oauthUserRepositoryStub) GetByID(_ context.Context, id string) (*model.User, error) {
	return r.byID[id], nil
}

func (r *oauthUserRepositoryStub) Create(_ context.Context, user *model.User) error {
	if r.createErr != nil {
		return r.createErr
	}
	if r.byID == nil {
		r.byID = make(map[string]*model.User)
	}
	if r.byEmail == nil {
		r.byEmail = make(map[string]*model.User)
	}
	r.byID[user.ID] = user
	r.byEmail[user.Email] = user
	r.created = append(r.created, user)
	return nil
}

func (r *oauthUserRepositoryStub) UpdateProfile(_ context.Context, _ string, _ string, _ string, _ string) error {
	return nil
}

func (r *oauthUserRepositoryStub) UpdateEmailVerification(_ context.Context, _ string, _ bool) error {
	return nil
}

func (r *oauthUserRepositoryStub) UpdateStatus(_ context.Context, _ string, _ *string, _ *string) (*model.User, error) {
	return nil, nil
}

func (r *oauthUserRepositoryStub) UpdatePasswordHash(_ context.Context, _ string, _ string) error {
	return nil
}

func (r *oauthUserRepositoryStub) ApplySuspension(_ context.Context, _ string, _ bool, _ time.Time, _ int64) (repository.SuspensionResult, error) {
	return repository.SuspensionApplied, nil
}

func (r *oauthUserRepositoryStub) IsSuspended(_ context.Context, _ string) (bool, error) {
	return false, nil
}

type oauthIdentityRepositoryStub struct {
	byProviderSubject map[string]*model.OAuthIdentity
	createErr         error
	created           []*model.OAuthIdentity
}

var _ repository.OAuthIdentityRepository = (*oauthIdentityRepositoryStub)(nil)

func newOAuthIdentityRepositoryStub() *oauthIdentityRepositoryStub {
	return &oauthIdentityRepositoryStub{byProviderSubject: make(map[string]*model.OAuthIdentity)}
}

func (r *oauthIdentityRepositoryStub) GetByProvider(_ context.Context, provider string, subject string) (*model.OAuthIdentity, error) {
	return r.byProviderSubject[provider+":"+subject], nil
}

func (r *oauthIdentityRepositoryStub) Create(_ context.Context, identity *model.OAuthIdentity) error {
	if r.createErr != nil {
		return r.createErr
	}
	r.byProviderSubject[identity.Provider+":"+identity.ProviderSubject] = identity
	r.created = append(r.created, identity)
	return nil
}

type googleVerifierStub struct {
	claims *googleoauth.Claims
	err    error
}

func (v *googleVerifierStub) Verify(_ context.Context, _ string) (*googleoauth.Claims, error) {
	return v.claims, v.err
}

// twoFactorChallengerStub sirve tanto para oauth_service_test.go como para
// pin_service_test.go: en ambos casos alcanza con simular una cuenta con 2FA
// activo, sin reimplementar TwoFactorService entero.
type twoFactorChallengerStub struct {
	enabledUserIDs map[string]bool
	challenge      *model.TwoFactorChallengeResponse
	isEnabledErr   error
	startErr       error
	startedForUser string
}

var _ TwoFactorChallenger = (*twoFactorChallengerStub)(nil)

func (c *twoFactorChallengerStub) IsEnabled(_ context.Context, userID string) (bool, error) {
	if c.isEnabledErr != nil {
		return false, c.isEnabledErr
	}
	return c.enabledUserIDs[userID], nil
}

func (c *twoFactorChallengerStub) StartChallenge(_ context.Context, userID string) (*model.TwoFactorChallengeResponse, error) {
	c.startedForUser = userID
	if c.startErr != nil {
		return nil, c.startErr
	}
	if c.challenge != nil {
		return c.challenge, nil
	}
	return &model.TwoFactorChallengeResponse{TwoFactorRequired: true, ChallengeToken: "challenge-token", ExpiresIn: 300}, nil
}

func TestOAuthServiceLoginWithGoogleCreatesNewAccount(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	userRepo := &oauthUserRepositoryStub{}
	identityRepo := newOAuthIdentityRepositoryStub()
	refreshRepo := newRefreshTokenRepositoryStub()
	verifier := &googleVerifierStub{claims: &googleoauth.Claims{
		Subject:       "google-subject-1",
		Email:         "ada@example.com",
		EmailVerified: true,
		Name:          "Ada Lovelace",
		Picture:       "https://example.com/ada.png",
	}}
	svc := NewOAuthService(userRepo, identityRepo, refreshRepo, verifier)

	result, err := svc.LoginWithGoogle(context.Background(), "raw-id-token")

	if err != nil {
		t.Fatalf("LoginWithGoogle() error = %v", err)
	}
	if result == nil || result.Auth == nil || result.Auth.Token == "" || result.Auth.RefreshToken == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(userRepo.created) != 1 {
		t.Fatalf("created users = %d, want 1 (CA1: cuenta nueva)", len(userRepo.created))
	}
	createdUser := userRepo.created[0]
	if createdUser.Email != "ada@example.com" || createdUser.Name != "Ada Lovelace" {
		t.Fatalf("unexpected created user: %+v", createdUser)
	}
	if createdUser.PasswordHash.Valid {
		t.Fatal("a purely federated user must not have a password hash")
	}
	if len(identityRepo.created) != 1 || identityRepo.created[0].ProviderSubject != "google-subject-1" {
		t.Fatalf("expected an oauth_identities row linking to the new user, got %+v", identityRepo.created)
	}
}

func TestOAuthServiceLoginWithGoogleLinksExistingAccountByEmail(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	existing := &model.User{
		ID:           "550e8400-e29b-41d4-a716-446655440000",
		Name:         "Ada Lovelace",
		Email:        "ada@example.com",
		PasswordHash: sql.NullString{String: "some-hash", Valid: true},
	}
	userRepo := &oauthUserRepositoryStub{
		byEmail: map[string]*model.User{"ada@example.com": existing},
		byID:    map[string]*model.User{existing.ID: existing},
	}
	identityRepo := newOAuthIdentityRepositoryStub()
	refreshRepo := newRefreshTokenRepositoryStub()
	verifier := &googleVerifierStub{claims: &googleoauth.Claims{
		Subject:       "google-subject-2",
		Email:         "ada@example.com",
		EmailVerified: true,
	}}
	svc := NewOAuthService(userRepo, identityRepo, refreshRepo, verifier)

	result, err := svc.LoginWithGoogle(context.Background(), "raw-id-token")

	if err != nil {
		t.Fatalf("LoginWithGoogle() error = %v", err)
	}
	if result.Auth == nil || result.Auth.User.ID != existing.ID {
		t.Fatalf("CA2: expected the session to belong to the existing account %q, got %+v", existing.ID, result)
	}
	if len(userRepo.created) != 0 {
		t.Fatal("CA2: must not create a new account when the email already exists")
	}
	if len(identityRepo.created) != 1 || identityRepo.created[0].UserID != existing.ID {
		t.Fatalf("expected the new identity to link to the existing account, got %+v", identityRepo.created)
	}
}

func TestOAuthServiceLoginWithGoogleReusesLinkedIdentityOnReturningLogin(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	existing := &model.User{ID: "550e8400-e29b-41d4-a716-446655440000", Email: "ada@example.com"}
	userRepo := &oauthUserRepositoryStub{byID: map[string]*model.User{existing.ID: existing}}
	identityRepo := newOAuthIdentityRepositoryStub()
	identityRepo.byProviderSubject["google:google-subject-1"] = &model.OAuthIdentity{
		UserID: existing.ID, Provider: googleProvider, ProviderSubject: "google-subject-1",
	}
	refreshRepo := newRefreshTokenRepositoryStub()
	verifier := &googleVerifierStub{claims: &googleoauth.Claims{Subject: "google-subject-1", Email: "ada@example.com", EmailVerified: true}}
	svc := NewOAuthService(userRepo, identityRepo, refreshRepo, verifier)

	result, err := svc.LoginWithGoogle(context.Background(), "raw-id-token")

	if err != nil {
		t.Fatalf("LoginWithGoogle() error = %v", err)
	}
	if result.Auth == nil || result.Auth.User.ID != existing.ID {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(userRepo.created) != 0 || len(identityRepo.created) != 0 {
		t.Fatal("a returning federated login must not create a new user nor a new identity row")
	}
}

func TestOAuthServiceLoginWithGoogleMapsProviderUnavailable(t *testing.T) {
	verifier := &googleVerifierStub{err: googleoauth.ErrProviderUnavailable}
	svc := NewOAuthService(&oauthUserRepositoryStub{}, newOAuthIdentityRepositoryStub(), newRefreshTokenRepositoryStub(), verifier)

	_, err := svc.LoginWithGoogle(context.Background(), "raw-id-token")

	if !errors.Is(err, ErrOAuthProviderUnavailable) {
		t.Fatalf("LoginWithGoogle() error = %v, want ErrOAuthProviderUnavailable (CA3)", err)
	}
}

func TestOAuthServiceLoginWithGoogleMapsInvalidToken(t *testing.T) {
	verifier := &googleVerifierStub{err: googleoauth.ErrInvalidToken}
	svc := NewOAuthService(&oauthUserRepositoryStub{}, newOAuthIdentityRepositoryStub(), newRefreshTokenRepositoryStub(), verifier)

	_, err := svc.LoginWithGoogle(context.Background(), "raw-id-token")

	if !errors.Is(err, ErrOAuthTokenInvalid) {
		t.Fatalf("LoginWithGoogle() error = %v, want ErrOAuthTokenInvalid", err)
	}
}

func TestOAuthServiceLoginWithGoogleReturnsChallengeWhenAccountHasTwoFactor(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	existing := &model.User{ID: "550e8400-e29b-41d4-a716-446655440000", Email: "ada@example.com"}
	userRepo := &oauthUserRepositoryStub{byID: map[string]*model.User{existing.ID: existing}}
	identityRepo := newOAuthIdentityRepositoryStub()
	identityRepo.byProviderSubject["google:google-subject-1"] = &model.OAuthIdentity{
		UserID: existing.ID, Provider: googleProvider, ProviderSubject: "google-subject-1",
	}
	refreshRepo := newRefreshTokenRepositoryStub()
	verifier := &googleVerifierStub{claims: &googleoauth.Claims{Subject: "google-subject-1", Email: "ada@example.com", EmailVerified: true}}
	challenger := &twoFactorChallengerStub{enabledUserIDs: map[string]bool{existing.ID: true}}
	svc := NewOAuthService(userRepo, identityRepo, refreshRepo, verifier, WithOAuthTwoFactor(challenger))

	result, err := svc.LoginWithGoogle(context.Background(), "raw-id-token")

	if err != nil {
		t.Fatalf("LoginWithGoogle() error = %v", err)
	}
	if result.Auth != nil {
		t.Fatalf("expected no session to be granted before the second factor, got %+v", result.Auth)
	}
	if result.Challenge == nil || !result.Challenge.TwoFactorRequired {
		t.Fatalf("expected a two-factor challenge, got %+v", result)
	}
	if challenger.startedForUser != existing.ID {
		t.Fatalf("StartChallenge() called for user %q, want %q (2FA must not be skippable via Google login)", challenger.startedForUser, existing.ID)
	}
	if len(refreshRepo.tokens) != 0 {
		t.Fatal("no refresh token should be issued while the second factor is pending")
	}
}

func TestOAuthServiceLoginWithGoogleSkipsChallengeWhenTwoFactorDisabled(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	existing := &model.User{ID: "550e8400-e29b-41d4-a716-446655440000", Email: "ada@example.com"}
	userRepo := &oauthUserRepositoryStub{byID: map[string]*model.User{existing.ID: existing}}
	identityRepo := newOAuthIdentityRepositoryStub()
	identityRepo.byProviderSubject["google:google-subject-1"] = &model.OAuthIdentity{
		UserID: existing.ID, Provider: googleProvider, ProviderSubject: "google-subject-1",
	}
	refreshRepo := newRefreshTokenRepositoryStub()
	verifier := &googleVerifierStub{claims: &googleoauth.Claims{Subject: "google-subject-1", Email: "ada@example.com", EmailVerified: true}}
	challenger := &twoFactorChallengerStub{}
	svc := NewOAuthService(userRepo, identityRepo, refreshRepo, verifier, WithOAuthTwoFactor(challenger))

	result, err := svc.LoginWithGoogle(context.Background(), "raw-id-token")

	if err != nil {
		t.Fatalf("LoginWithGoogle() error = %v", err)
	}
	if result.Challenge != nil || result.Auth == nil || result.Auth.Token == "" {
		t.Fatalf("expected a normal session without 2FA, got %+v", result)
	}
}
