package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type userRepositoryStub struct {
	existingUser *model.User
	getErr       error
	createErr    error
	createdUser  *model.User
}

type sessionRepositoryStub struct {
	createdTokenID string
	createdUserID  string
	createdExpiry  time.Time
	revokedTokenID string
	active         map[string]bool
	revokeErr      error
	created        map[string]time.Time
}

func (r *sessionRepositoryStub) Create(_ context.Context, tokenID string, userID string, expiresAt time.Time) error {
	r.createdTokenID = tokenID
	r.createdUserID = userID
	r.createdExpiry = expiresAt
	if r.active == nil {
		r.active = make(map[string]bool)
	}
	if r.created == nil {
		r.created = make(map[string]time.Time)
	}
	r.active[tokenID] = true
	r.created[tokenID] = expiresAt
	return nil
}

func (r *sessionRepositoryStub) IsActive(_ context.Context, tokenID string) (bool, error) {
	return r.active[tokenID], nil
}

func (r *sessionRepositoryStub) Revoke(_ context.Context, tokenID string) error {
	if r.revokeErr != nil {
		return r.revokeErr
	}
	if !r.active[tokenID] {
		return repository.ErrSessionNotFound
	}
	r.revokedTokenID = tokenID
	r.active[tokenID] = false
	return nil
}

var _ repository.UserRepository = (*userRepositoryStub)(nil)

func (r *userRepositoryStub) GetByEmail(_ context.Context, _ string) (*model.User, error) {
	return r.existingUser, r.getErr
}

func (r *userRepositoryStub) Create(_ context.Context, user *model.User) error {
	if r.createErr != nil {
		return r.createErr
	}

	r.createdUser = user
	user.ID = "550e8400-e29b-41d4-a716-446655440000"
	user.CreatedAt = time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	return nil
}

func TestAuthServiceRegisterCreatesUserAndReturnsToken(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	repo := &userRepositoryStub{}
	service := NewAuthService(repo)
	request := model.RegisterRequest{
		Name:     "Ada Lovelace",
		Email:    "ada@example.com",
		Password: "Secure123",
	}

	response, err := service.Register(context.Background(), request)

	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if response == nil || response.Token == "" {
		t.Fatal("Register() should return a non-empty token")
	}
	if response.User.ID != "550e8400-e29b-41d4-a716-446655440000" || response.User.Name != request.Name || response.User.Email != request.Email {
		t.Fatalf("unexpected user response: %+v", response.User)
	}
	if repo.createdUser == nil {
		t.Fatal("Create() was not called")
	}
	if repo.createdUser.PasswordHash == request.Password {
		t.Fatal("password must not be stored in plain text")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(repo.createdUser.PasswordHash), []byte(request.Password)); err != nil {
		t.Fatalf("stored password is not a valid hash: %v", err)
	}
}

func TestAuthServiceRegisterRejectsExistingEmail(t *testing.T) {
	repo := &userRepositoryStub{existingUser: &model.User{ID: "550e8400-e29b-41d4-a716-446655440007", Email: "ada@example.com"}}
	service := NewAuthService(repo)

	response, err := service.Register(context.Background(), model.RegisterRequest{
		Name:     "Ada Lovelace",
		Email:    "ada@example.com",
		Password: "Secure123",
	})

	if !errors.Is(err, ErrEmailInUse) {
		t.Fatalf("Register() error = %v, want ErrEmailInUse", err)
	}
	if response != nil {
		t.Fatalf("Register() response = %+v, want nil", response)
	}
	if repo.createdUser != nil {
		t.Fatal("Create() must not be called for an existing email")
	}
}

func TestAuthServiceRegisterRejectsInsecurePasswords(t *testing.T) {
	tests := []struct {
		name     string
		password string
	}{
		{name: "too short", password: "Aa1aaaa"},
		{name: "without uppercase", password: "secure123"},
		{name: "without lowercase", password: "SECURE123"},
		{name: "without number", password: "SecurePass"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &userRepositoryStub{}
			service := NewAuthService(repo)

			response, err := service.Register(context.Background(), model.RegisterRequest{
				Name:     "Ada Lovelace",
				Email:    "ada@example.com",
				Password: test.password,
			})

			if !errors.Is(err, ErrPasswordInsecure) {
				t.Fatalf("Register() error = %v, want ErrPasswordInsecure", err)
			}
			if response != nil || repo.createdUser != nil {
				t.Fatal("insecure password must not create a user")
			}
		})
	}
}

func TestAuthServiceRegisterPropagatesRepositoryErrors(t *testing.T) {
	repositoryErr := errors.New("database unavailable")

	t.Run("lookup", func(t *testing.T) {
		repo := &userRepositoryStub{getErr: repositoryErr}
		service := NewAuthService(repo)

		_, err := service.Register(context.Background(), model.RegisterRequest{
			Name:     "Ada Lovelace",
			Email:    "ada@example.com",
			Password: "Secure123",
		})

		if !errors.Is(err, repositoryErr) {
			t.Fatalf("Register() error = %v, want repository error", err)
		}
	})

	t.Run("create", func(t *testing.T) {
		repo := &userRepositoryStub{createErr: repositoryErr}
		service := NewAuthService(repo)

		_, err := service.Register(context.Background(), model.RegisterRequest{
			Name:     "Ada Lovelace",
			Email:    "ada@example.com",
			Password: "Secure123",
		})

		if !errors.Is(err, repositoryErr) {
			t.Fatalf("Register() error = %v, want repository error", err)
		}
	})
}

func TestAuthServiceLoginReturnsUserAndToken(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("Secure123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	repo := &userRepositoryStub{existingUser: &model.User{
		ID:           "550e8400-e29b-41d4-a716-446655440000",
		Name:         "Ada Lovelace",
		Email:        "ada@example.com",
		PasswordHash: string(passwordHash),
		CreatedAt:    time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC),
	}}
	sessionRepo := &sessionRepositoryStub{}

	response, err := NewAuthService(repo, sessionRepo).Login(context.Background(), model.LoginRequest{
		Email:    "ada@example.com",
		Password: "Secure123",
	})

	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if response == nil || response.Token == "" || response.User.ID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("unexpected login response: %+v", response)
	}
	if sessionRepo.createdTokenID == "" {
		t.Fatal("Login() must persist the session")
	}

	if err := NewAuthService(repo, sessionRepo).Logout(context.Background(), sessionRepo.createdTokenID); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if sessionRepo.revokedTokenID != sessionRepo.createdTokenID {
		t.Fatalf("Logout() revoked token = %q, want %q", sessionRepo.revokedTokenID, sessionRepo.createdTokenID)
	}
}

func TestAuthServiceLoginCreatesUniqueExpiringSession(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("Secure123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	repo := &userRepositoryStub{existingUser: &model.User{
		ID:           "550e8400-e29b-41d4-a716-446655440000",
		Name:         "Ada Lovelace",
		Email:        "ada@example.com",
		PasswordHash: string(passwordHash),
	}}
	sessionRepo := &sessionRepositoryStub{}
	authService := NewAuthService(repo, sessionRepo)

	first, err := authService.Login(context.Background(), model.LoginRequest{Email: "ada@example.com", Password: "Secure123"})
	if err != nil {
		t.Fatalf("first Login() error = %v", err)
	}
	second, err := authService.Login(context.Background(), model.LoginRequest{Email: "ada@example.com", Password: "Secure123"})
	if err != nil {
		t.Fatalf("second Login() error = %v", err)
	}
	if first.Token == second.Token {
		t.Fatal("separate logins must create different tokens")
	}
	if len(sessionRepo.created) != 2 {
		t.Fatalf("created sessions = %d, want 2", len(sessionRepo.created))
	}
	firstClaims := parseTokenClaims(t, first.Token)
	secondClaims := parseTokenClaims(t, second.Token)
	if firstClaims["jti"] == secondClaims["jti"] {
		t.Fatal("separate logins must create different token IDs")
	}
	for name, claims := range map[string]jwt.MapClaims{"first": firstClaims, "second": secondClaims} {
		expiration, ok := claims["exp"].(float64)
		if !ok {
			t.Fatalf("%s token exp claim = %T, want number", name, claims["exp"])
		}
		remaining := time.Until(time.Unix(int64(expiration), 0))
		if remaining < 71*time.Hour || remaining > 73*time.Hour {
			t.Fatalf("%s token expiration remaining = %v, want approximately 72 hours", name, remaining)
		}
	}

	for tokenID, expiresAt := range sessionRepo.created {
		if !sessionRepo.active[tokenID] {
			t.Fatalf("session %q must be active after login", tokenID)
		}
		remaining := time.Until(expiresAt)
		if remaining < 71*time.Hour || remaining > 73*time.Hour {
			t.Fatalf("session %q expiration remaining = %v, want approximately 72 hours", tokenID, remaining)
		}
	}
}

func parseTokenClaims(t *testing.T, tokenString string) jwt.MapClaims {
	t.Helper()
	token, err := jwt.ParseWithClaims(tokenString, jwt.MapClaims{}, func(token *jwt.Token) (any, error) {
		return []byte("test-secret"), nil
	})
	if err != nil {
		t.Fatalf("ParseWithClaims() error = %v", err)
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatalf("token claims = %T, want jwt.MapClaims", token.Claims)
	}
	return claims
}

func TestAuthServiceRegisterCreatesExpiringSession(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	sessionRepo := &sessionRepositoryStub{}
	response, err := NewAuthService(&userRepositoryStub{}, sessionRepo).Register(context.Background(), model.RegisterRequest{
		Name: "Ada Lovelace", Email: "ada@example.com", Password: "Secure123",
	})

	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if response == nil || response.Token == "" {
		t.Fatal("Register() must return a token")
	}
	if len(sessionRepo.created) != 1 {
		t.Fatalf("created sessions = %d, want 1", len(sessionRepo.created))
	}
	if sessionRepo.createdUserID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("created session user ID = %s, want test user ID", sessionRepo.createdUserID)
	}
}

func TestAuthServiceLogoutRejectsAbsentToken(t *testing.T) {
	sessionRepo := &sessionRepositoryStub{active: map[string]bool{}}

	err := NewAuthService(&userRepositoryStub{}, sessionRepo).Logout(context.Background(), "missing-token")

	if !errors.Is(err, ErrInexistentSession) {
		t.Fatalf("Logout() error = %v, want ErrInexistentSession", err)
	}
}

func TestAuthServiceLoginDoesNotRevealInvalidCredentialReason(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("Secure123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}

	tests := []struct {
		name string
		user *model.User
	}{
		{name: "unknown email", user: nil},
		{name: "wrong password", user: &model.User{PasswordHash: string(passwordHash)}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &userRepositoryStub{existingUser: test.user}
			response, loginErr := NewAuthService(repo).Login(context.Background(), model.LoginRequest{
				Email:    "ada@example.com",
				Password: "Wrong123",
			})

			if !errors.Is(loginErr, ErrInvalidCredentials) {
				t.Fatalf("Login() error = %v, want ErrInvalidCredentials", loginErr)
			}
			if response != nil {
				t.Fatalf("Login() response = %+v, want nil", response)
			}
		})
	}
}

func TestAuthServiceLoginPropagatesRepositoryErrors(t *testing.T) {
	repositoryErr := errors.New("database unavailable")
	repo := &userRepositoryStub{getErr: repositoryErr}
	service := NewAuthService(repo)

	_, err := service.Login(context.Background(), model.LoginRequest{
		Email:    "ada@example.com",
		Password: "Secure123",
	})

	if !errors.Is(err, repositoryErr) {
		t.Fatalf("Login() error = %v, want repository error", err)
	}
}
