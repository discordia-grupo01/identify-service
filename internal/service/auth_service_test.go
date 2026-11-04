package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type userRepositoryStub struct {
	existingUser        *model.User
	getErr              error
	createErr           error
	updateErr           error
	createdUser         *model.User
	updatePasswordErr   error
	updatedPasswordHash string
	updatedPasswordUser string
}

var _ repository.UserRepository = (*userRepositoryStub)(nil)

func (r *userRepositoryStub) GetByEmail(_ context.Context, _ string) (*model.User, error) {
	return r.existingUser, r.getErr
}

func (r *userRepositoryStub) GetByID(_ context.Context, _ string) (*model.User, error) {
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

func (r *userRepositoryStub) UpdateEmailVerification(_ context.Context, _ string, _ bool) error {
	if r.existingUser == nil {
		r.existingUser = &model.User{}
	}
	if r.updateErr != nil {
		return r.updateErr
	}
	r.existingUser.EmailVerified = true
	return nil
}

func (r *userRepositoryStub) UpdateProfile(_ context.Context, _ string, name string, description string, avatarURL string) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	if r.existingUser == nil {
		r.existingUser = &model.User{}
	}
	r.existingUser.Name = name
	r.existingUser.Description = description
	r.existingUser.AvatarURL = avatarURL
	return nil
}

func (r *userRepositoryStub) UpdatePasswordHash(_ context.Context, userID string, passwordHash string) error {
	if r.updatePasswordErr != nil {
		return r.updatePasswordErr
	}
	r.updatedPasswordUser = userID
	r.updatedPasswordHash = passwordHash
	if r.existingUser != nil {
		r.existingUser.PasswordHash = sql.NullString{String: passwordHash, Valid: true}
	}
	return nil
}

func (r *userRepositoryStub) ApplySuspension(_ context.Context, _ string, _ bool, _ time.Time, _ int64) (repository.SuspensionResult, error) {
	return repository.SuspensionApplied, nil
}

func (r *userRepositoryStub) ApplyPremium(_ context.Context, _ string, _ bool, _ int64) (repository.PremiumResult, error) {
	return repository.PremiumApplied, nil
}

func (r *userRepositoryStub) IsSuspended(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (r *userRepositoryStub) UpdateStatus(_ context.Context, _ string, statusText *string, statusEmoji *string) (*model.User, error) {
	if r.updateErr != nil {
		return nil, r.updateErr
	}
	if r.existingUser == nil {
		return nil, nil
	}
	if statusText != nil {
		r.existingUser.StatusText = *statusText
	}
	if statusEmoji != nil {
		r.existingUser.StatusEmoji = *statusEmoji
	}
	return r.existingUser, nil
}

type loginAttemptRepositoryStub struct {
	failures   map[string]int
	countErr   error
	recordErr  error
	clearCalls int
}

func newLoginAttemptRepositoryStub() *loginAttemptRepositoryStub {
	return &loginAttemptRepositoryStub{failures: make(map[string]int)}
}

var _ repository.LoginAttemptRepository = (*loginAttemptRepositoryStub)(nil)

func loginAttemptKey(email, ip string) string { return email + "|" + ip }

func (r *loginAttemptRepositoryStub) CountRecentFailures(_ context.Context, email string, ip string, _ time.Time) (int, error) {
	if r.countErr != nil {
		return 0, r.countErr
	}
	return r.failures[loginAttemptKey(email, ip)], nil
}

func (r *loginAttemptRepositoryStub) RecordFailure(_ context.Context, email string, ip string) error {
	if r.recordErr != nil {
		return r.recordErr
	}
	r.failures[loginAttemptKey(email, ip)]++
	return nil
}

func (r *loginAttemptRepositoryStub) ClearFailures(_ context.Context, email string, ip string) error {
	r.clearCalls++
	delete(r.failures, loginAttemptKey(email, ip))
	return nil
}

type refreshTokenRecord struct {
	userID    string
	familyID  string
	expiresAt time.Time
	revokedAt *time.Time
}

type refreshTokenRepositoryStub struct {
	tokens          map[string]*refreshTokenRecord
	createErr       error
	getErr          error
	revokeErr       error
	revokeFamilyErr error
	revokeAllErr    error
}

func newRefreshTokenRepositoryStub() *refreshTokenRepositoryStub {
	return &refreshTokenRepositoryStub{tokens: make(map[string]*refreshTokenRecord)}
}

var _ repository.RefreshTokenRepository = (*refreshTokenRepositoryStub)(nil)

func (r *refreshTokenRepositoryStub) Create(_ context.Context, tokenHash string, userID string, familyID string, expiresAt time.Time) error {
	if r.createErr != nil {
		return r.createErr
	}
	if r.tokens == nil {
		r.tokens = make(map[string]*refreshTokenRecord)
	}
	r.tokens[tokenHash] = &refreshTokenRecord{userID: userID, familyID: familyID, expiresAt: expiresAt}
	return nil
}

func (r *refreshTokenRepositoryStub) GetByHash(_ context.Context, tokenHash string) (*model.RefreshToken, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	record, ok := r.tokens[tokenHash]
	if !ok {
		return nil, repository.ErrRefreshTokenNotFound
	}
	return &model.RefreshToken{
		UserID:    record.userID,
		FamilyID:  record.familyID,
		ExpiresAt: record.expiresAt,
		RevokedAt: record.revokedAt,
	}, nil
}

func (r *refreshTokenRepositoryStub) RevokeByHash(_ context.Context, tokenHash string) error {
	if r.revokeErr != nil {
		return r.revokeErr
	}
	record, ok := r.tokens[tokenHash]
	if !ok || record.revokedAt != nil {
		return repository.ErrRefreshTokenNotFound
	}
	now := time.Now()
	record.revokedAt = &now
	return nil
}

func (r *refreshTokenRepositoryStub) RevokeFamily(_ context.Context, familyID string) error {
	if r.revokeFamilyErr != nil {
		return r.revokeFamilyErr
	}
	now := time.Now()
	for _, record := range r.tokens {
		if record.familyID == familyID && record.revokedAt == nil {
			record.revokedAt = &now
		}
	}
	return nil
}

func (r *refreshTokenRepositoryStub) RevokeAllForUser(_ context.Context, userID string) error {
	if r.revokeAllErr != nil {
		return r.revokeAllErr
	}
	now := time.Now()
	for _, record := range r.tokens {
		if record.userID == userID && record.revokedAt == nil {
			record.revokedAt = &now
		}
	}
	return nil
}

func TestAuthServiceRegisterCreatesUserWithoutToken(t *testing.T) {
	repo := &userRepositoryStub{}
	service := NewAuthService(repo, nil)
	request := model.RegisterRequest{
		Name:     "Ada Lovelace",
		Email:    "ada@example.com",
		Password: "Secure123",
	}

	response, err := service.Register(context.Background(), request)

	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if response == nil || response.ID == "" {
		t.Fatal("Register() should return the created user")
	}
	if response.ID != "550e8400-e29b-41d4-a716-446655440000" || response.Name != request.Name || response.Email != request.Email {
		t.Fatalf("unexpected user response: %+v", response)
	}
	if repo.createdUser == nil {
		t.Fatal("Create() was not called")
	}
	if repo.createdUser.PasswordHash.String == request.Password {
		t.Fatal("password must not be stored in plain text")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(repo.createdUser.PasswordHash.String), []byte(request.Password)); err != nil {
		t.Fatalf("stored password is not a valid hash: %v", err)
	}
}

func TestAuthServiceRegisterRejectsExistingEmail(t *testing.T) {
	repo := &userRepositoryStub{existingUser: &model.User{ID: "550e8400-e29b-41d4-a716-446655440007", Email: "ada@example.com"}}
	service := NewAuthService(repo, nil)

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
			service := NewAuthService(repo, nil)

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

func TestAuthServiceRegisterRejectsInvalidNames(t *testing.T) {
	tests := []struct {
		name     string
		userName string
	}{
		{name: "empty", userName: ""},
		{name: "only whitespace", userName: "   "},
		{name: "longer than 100 runes", userName: strings.Repeat("a", MaxNameRunes+1)},
		{name: "disallowed characters", userName: "Ada<script>"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &userRepositoryStub{}
			service := NewAuthService(repo, nil)

			response, err := service.Register(context.Background(), model.RegisterRequest{
				Name:     test.userName,
				Email:    "ada@example.com",
				Password: "SecurePass1",
			})

			if !errors.Is(err, ErrInvalidProfileName) {
				t.Fatalf("Register() error = %v, want ErrInvalidProfileName", err)
			}
			if response != nil || repo.createdUser != nil {
				t.Fatal("invalid name must not create a user")
			}
		})
	}
}

// El nombre se guarda normalizado, igual que en PATCH /v1/me/profile: si no,
// registrarse y editar el perfil dejarian valores distintos para el mismo dato.
func TestAuthServiceRegisterNormalizesName(t *testing.T) {
	repo := &userRepositoryStub{}
	service := NewAuthService(repo, nil)

	response, err := service.Register(context.Background(), model.RegisterRequest{
		Name:     "  Ada   Lovelace  ",
		Email:    "ada@example.com",
		Password: "SecurePass1",
	})
	if err != nil {
		t.Fatalf("Register() error = %v, want nil", err)
	}

	if repo.createdUser.Name != "Ada Lovelace" {
		t.Fatalf("persisted name = %q, want %q", repo.createdUser.Name, "Ada Lovelace")
	}
	if response.Name != "Ada Lovelace" {
		t.Fatalf("response name = %q, want %q", response.Name, "Ada Lovelace")
	}
}

func TestAuthServiceRegisterAcceptsNameAtMaxLength(t *testing.T) {
	repo := &userRepositoryStub{}
	service := NewAuthService(repo, nil)

	maxName := strings.Repeat("a", MaxNameRunes)
	if _, err := service.Register(context.Background(), model.RegisterRequest{
		Name:     maxName,
		Email:    "ada@example.com",
		Password: "SecurePass1",
	}); err != nil {
		t.Fatalf("Register() error = %v, want nil", err)
	}

	if repo.createdUser.Name != maxName {
		t.Fatalf("persisted name length = %d, want %d", len(repo.createdUser.Name), MaxNameRunes)
	}
}

func TestAuthServiceRegisterPropagatesRepositoryErrors(t *testing.T) {
	repositoryErr := errors.New("database unavailable")

	t.Run("lookup", func(t *testing.T) {
		repo := &userRepositoryStub{getErr: repositoryErr}
		service := NewAuthService(repo, nil)

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
		service := NewAuthService(repo, nil)

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

func TestAuthServiceRegisterDoesNotCreateRefreshToken(t *testing.T) {
	refreshRepo := newRefreshTokenRepositoryStub()
	response, err := NewAuthService(&userRepositoryStub{}, refreshRepo).Register(context.Background(), model.RegisterRequest{
		Name: "Ada Lovelace", Email: "ada@example.com", Password: "Secure123",
	})

	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if response == nil || response.ID == "" {
		t.Fatal("Register() must return the created user")
	}
	if len(refreshRepo.tokens) != 0 {
		t.Fatalf("persisted refresh tokens = %d, want 0", len(refreshRepo.tokens))
	}
}

func TestAuthServiceLoginReturnsUserAndTokens(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("Secure123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	repo := &userRepositoryStub{existingUser: &model.User{
		ID:            "550e8400-e29b-41d4-a716-446655440000",
		Name:          "Ada Lovelace",
		Email:         "ada@example.com",
		PasswordHash:  sql.NullString{String: string(passwordHash), Valid: true},
		EmailVerified: true,
		CreatedAt:     time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC),
	}}
	refreshRepo := newRefreshTokenRepositoryStub()

	result, err := NewAuthService(repo, refreshRepo).Login(context.Background(), model.LoginRequest{
		Email:    "ada@example.com",
		Password: "Secure123",
	})

	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if result == nil || result.Auth == nil {
		t.Fatalf("Login() without 2FA must return a session: %+v", result)
	}
	response := result.Auth
	if response.Token == "" || response.RefreshToken == "" {
		t.Fatalf("unexpected login response: %+v", response)
	}
	if response.User.ID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("unexpected user in response: %+v", response.User)
	}

	hash := hashToken(response.RefreshToken)
	if _, ok := refreshRepo.tokens[hash]; !ok {
		t.Fatal("Login() must persist the refresh token")
	}

	if err := NewAuthService(repo, refreshRepo).Logout(context.Background(), response.RefreshToken); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if refreshRepo.tokens[hash].revokedAt == nil {
		t.Fatal("Logout() must revoke the refresh token")
	}
}

func TestAuthServiceLoginCreatesUniqueExpiringTokens(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("Secure123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	repo := &userRepositoryStub{existingUser: &model.User{
		ID:            "550e8400-e29b-41d4-a716-446655440000",
		Name:          "Ada Lovelace",
		Email:         "ada@example.com",
		PasswordHash:  sql.NullString{String: string(passwordHash), Valid: true},
		EmailVerified: true,
	}}
	refreshRepo := newRefreshTokenRepositoryStub()
	authService := NewAuthService(repo, refreshRepo)

	firstResult, err := authService.Login(context.Background(), model.LoginRequest{Email: "ada@example.com", Password: "Secure123"})
	if err != nil {
		t.Fatalf("first Login() error = %v", err)
	}
	secondResult, err := authService.Login(context.Background(), model.LoginRequest{Email: "ada@example.com", Password: "Secure123"})
	if err != nil {
		t.Fatalf("second Login() error = %v", err)
	}
	first, second := firstResult.Auth, secondResult.Auth

	if first.Token == second.Token {
		t.Fatal("separate logins must create different access tokens")
	}
	if first.RefreshToken == second.RefreshToken {
		t.Fatal("separate logins must create different refresh tokens")
	}
	if len(refreshRepo.tokens) != 2 {
		t.Fatalf("persisted refresh tokens = %d, want 2", len(refreshRepo.tokens))
	}

	firstClaims := parseTokenClaims(t, first.Token)
	secondClaims := parseTokenClaims(t, second.Token)
	if firstClaims["jti"] == secondClaims["jti"] {
		t.Fatal("separate logins must create different access token IDs")
	}

	for name, claims := range map[string]jwt.MapClaims{"first": firstClaims, "second": secondClaims} {
		expiration, ok := claims["exp"].(float64)
		if !ok {
			t.Fatalf("%s access token exp claim = %T, want number", name, claims["exp"])
		}
		remaining := time.Until(time.Unix(int64(expiration), 0))
		if remaining < 14*time.Minute || remaining > 16*time.Minute {
			t.Fatalf("%s access token expiration remaining = %v, want approximately 15 minutes", name, remaining)
		}
	}

	firstHash := hashToken(first.RefreshToken)
	secondHash := hashToken(second.RefreshToken)
	if refreshRepo.tokens[firstHash].familyID == refreshRepo.tokens[secondHash].familyID {
		t.Fatal("separate logins must create different token families")
	}
	for name, record := range map[string]*refreshTokenRecord{"first": refreshRepo.tokens[firstHash], "second": refreshRepo.tokens[secondHash]} {
		remaining := time.Until(record.expiresAt)
		if remaining < 29*24*time.Hour || remaining > 31*24*time.Hour {
			t.Fatalf("%s refresh token expiration remaining = %v, want approximately 30 days", name, remaining)
		}
	}
}

func TestAuthServiceLoginPropagatesRefreshTokenPersistenceError(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("Secure123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	repo := &userRepositoryStub{existingUser: &model.User{
		ID:            "550e8400-e29b-41d4-a716-446655440000",
		Email:         "ada@example.com",
		PasswordHash:  sql.NullString{String: string(passwordHash), Valid: true},
		EmailVerified: true,
	}}
	refreshRepo := newRefreshTokenRepositoryStub()
	repositoryErr := errors.New("database unavailable")
	refreshRepo.createErr = repositoryErr

	_, err = NewAuthService(repo, refreshRepo).Login(context.Background(), model.LoginRequest{
		Email:    "ada@example.com",
		Password: "Secure123",
	})

	if !errors.Is(err, repositoryErr) {
		t.Fatalf("Login() error = %v, want repository error", err)
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
		{name: "wrong password", user: &model.User{PasswordHash: sql.NullString{String: string(passwordHash), Valid: true}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &userRepositoryStub{existingUser: test.user}
			response, loginErr := NewAuthService(repo, nil).Login(context.Background(), model.LoginRequest{
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

func TestAuthServiceLoginReturnsEmailNotVerifiedForValidPassword(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("Secure123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	repo := &userRepositoryStub{existingUser: &model.User{
		Email:         "ada@example.com",
		PasswordHash:  sql.NullString{String: string(passwordHash), Valid: true},
		EmailVerified: false,
	}}

	response, loginErr := NewAuthService(repo, nil).Login(context.Background(), model.LoginRequest{
		Email:    "ada@example.com",
		Password: "Secure123",
	})

	if !errors.Is(loginErr, ErrEmailNotVerified) {
		t.Fatalf("Login() error = %v, want ErrEmailNotVerified", loginErr)
	}
	if response != nil {
		t.Fatalf("Login() response = %+v, want nil", response)
	}
}

func TestAuthServiceLoginPropagatesRepositoryErrors(t *testing.T) {
	repositoryErr := errors.New("database unavailable")
	repo := &userRepositoryStub{getErr: repositoryErr}
	service := NewAuthService(repo, nil)

	_, err := service.Login(context.Background(), model.LoginRequest{
		Email:    "ada@example.com",
		Password: "Secure123",
	})

	if !errors.Is(err, repositoryErr) {
		t.Fatalf("Login() error = %v, want repository error", err)
	}
}

func TestAuthServiceLogin_BlocksAfterTooManyFailuresFromSameEmailAndIP(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("Secure123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	repo := &userRepositoryStub{existingUser: &model.User{
		ID:            "550e8400-e29b-41d4-a716-446655440000",
		Email:         "ada@example.com",
		PasswordHash:  sql.NullString{String: string(passwordHash), Valid: true},
		EmailVerified: true,
	}}
	attempts := newLoginAttemptRepositoryStub()
	attempts.failures[loginAttemptKey("ada@example.com", "203.0.113.1")] = maxLoginAttempts

	service := NewAuthService(repo, newRefreshTokenRepositoryStub(), WithLoginAttempts(attempts))

	// Contrasena CORRECTA: el bloqueo tiene que activarse antes de siquiera
	// comparar el hash, no depender de que la contrasena este mal.
	_, err = service.Login(context.Background(), model.LoginRequest{
		Email:    "ada@example.com",
		Password: "Secure123",
		IP:       "203.0.113.1",
	})

	if !errors.Is(err, ErrLoginAttemptsExceeded) {
		t.Fatalf("Login() error = %v, want ErrLoginAttemptsExceeded", err)
	}
}

func TestAuthServiceLogin_SameEmailDifferentIP_NotBlocked(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("Secure123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	repo := &userRepositoryStub{existingUser: &model.User{
		ID:            "550e8400-e29b-41d4-a716-446655440000",
		Email:         "ada@example.com",
		PasswordHash:  sql.NullString{String: string(passwordHash), Valid: true},
		EmailVerified: true,
	}}
	attempts := newLoginAttemptRepositoryStub()
	// Un atacante agoto el limite para esta cuenta desde SU ip.
	attempts.failures[loginAttemptKey("ada@example.com", "203.0.113.1")] = maxLoginAttempts

	service := NewAuthService(repo, newRefreshTokenRepositoryStub(), WithLoginAttempts(attempts))

	// La dueña real, desde otra ip, no deberia quedar bloqueada.
	result, err := service.Login(context.Background(), model.LoginRequest{
		Email:    "ada@example.com",
		Password: "Secure123",
		IP:       "198.51.100.7",
	})

	if err != nil {
		t.Fatalf("Login() from a different IP error = %v, want success", err)
	}
	if result == nil || result.Auth == nil {
		t.Fatalf("Login() from a different IP must return a session: %+v", result)
	}
}

func TestAuthServiceLogin_WrongPassword_RecordsFailure(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("Secure123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	repo := &userRepositoryStub{existingUser: &model.User{
		Email:        "ada@example.com",
		PasswordHash: sql.NullString{String: string(passwordHash), Valid: true},
	}}
	attempts := newLoginAttemptRepositoryStub()
	service := NewAuthService(repo, newRefreshTokenRepositoryStub(), WithLoginAttempts(attempts))

	_, err = service.Login(context.Background(), model.LoginRequest{
		Email:    "ada@example.com",
		Password: "wrong-password",
		IP:       "203.0.113.1",
	})

	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
	}
	if got := attempts.failures[loginAttemptKey("ada@example.com", "203.0.113.1")]; got != 1 {
		t.Errorf("recorded failures = %d, want 1", got)
	}
}

func TestAuthServiceLogin_Success_ClearsFailures(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("Secure123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	repo := &userRepositoryStub{existingUser: &model.User{
		ID:            "550e8400-e29b-41d4-a716-446655440000",
		Email:         "ada@example.com",
		PasswordHash:  sql.NullString{String: string(passwordHash), Valid: true},
		EmailVerified: true,
	}}
	attempts := newLoginAttemptRepositoryStub()
	attempts.failures[loginAttemptKey("ada@example.com", "203.0.113.1")] = maxLoginAttempts - 1
	service := NewAuthService(repo, newRefreshTokenRepositoryStub(), WithLoginAttempts(attempts))

	_, err = service.Login(context.Background(), model.LoginRequest{
		Email:    "ada@example.com",
		Password: "Secure123",
		IP:       "203.0.113.1",
	})

	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if attempts.clearCalls != 1 {
		t.Errorf("ClearFailures calls = %d, want 1", attempts.clearCalls)
	}
	if got := attempts.failures[loginAttemptKey("ada@example.com", "203.0.113.1")]; got != 0 {
		t.Errorf("failures after success = %d, want 0", got)
	}
}

func TestAuthServiceLogoutRejectsAbsentToken(t *testing.T) {
	refreshRepo := newRefreshTokenRepositoryStub()

	err := NewAuthService(&userRepositoryStub{}, refreshRepo).Logout(context.Background(), "missing-token")

	if !errors.Is(err, ErrInexistentSession) {
		t.Fatalf("Logout() error = %v, want ErrInexistentSession", err)
	}
}

func loginTestUser(t *testing.T, refreshRepo *refreshTokenRepositoryStub) (*userRepositoryStub, *model.AuthResponse) {
	t.Helper()
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("Secure123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	repo := &userRepositoryStub{existingUser: &model.User{
		ID:            "550e8400-e29b-41d4-a716-446655440000",
		Name:          "Ada Lovelace",
		Email:         "ada@example.com",
		PasswordHash:  sql.NullString{String: string(passwordHash), Valid: true},
		EmailVerified: true,
	}}
	response, err := NewAuthService(repo, refreshRepo).Login(context.Background(), model.LoginRequest{
		Email:    "ada@example.com",
		Password: "Secure123",
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	return repo, response.Auth
}

func TestAuthServiceRefreshRotatesTokenWithinSameFamily(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	refreshRepo := newRefreshTokenRepositoryStub()
	repo, login := loginTestUser(t, refreshRepo)

	rotated, err := NewAuthService(repo, refreshRepo).Refresh(context.Background(), login.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if rotated.Token == login.Token {
		t.Fatal("Refresh() must issue a new access token")
	}
	if rotated.RefreshToken == login.RefreshToken {
		t.Fatal("Refresh() must issue a new refresh token")
	}

	oldHash := hashToken(login.RefreshToken)
	newHash := hashToken(rotated.RefreshToken)
	if refreshRepo.tokens[oldHash].revokedAt == nil {
		t.Fatal("Refresh() must revoke the previous refresh token")
	}
	if refreshRepo.tokens[newHash].revokedAt != nil {
		t.Fatal("the newly issued refresh token must not be revoked")
	}
	if refreshRepo.tokens[oldHash].familyID != refreshRepo.tokens[newHash].familyID {
		t.Fatal("rotation must preserve the token family")
	}
}

func TestAuthServiceRefreshRejectsUnknownToken(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	refreshRepo := newRefreshTokenRepositoryStub()

	_, err := NewAuthService(&userRepositoryStub{}, refreshRepo).Refresh(context.Background(), "never-issued-token")

	if !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("Refresh() error = %v, want ErrInvalidRefreshToken", err)
	}
}

func TestAuthServiceRefreshRejectsExpiredToken(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	refreshRepo := newRefreshTokenRepositoryStub()
	plainToken := "expired-plain-token"
	refreshRepo.tokens[hashToken(plainToken)] = &refreshTokenRecord{
		userID:    "550e8400-e29b-41d4-a716-446655440000",
		familyID:  "family-1",
		expiresAt: time.Now().Add(-time.Hour),
	}

	_, err := NewAuthService(&userRepositoryStub{}, refreshRepo).Refresh(context.Background(), plainToken)

	if !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("Refresh() error = %v, want ErrInvalidRefreshToken", err)
	}
}

func TestAuthServiceRefreshDetectsReuseAndRevokesWholeFamily(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	refreshRepo := newRefreshTokenRepositoryStub()
	repo, login := loginTestUser(t, refreshRepo)
	authService := NewAuthService(repo, refreshRepo)

	rotated, err := authService.Refresh(context.Background(), login.RefreshToken)
	if err != nil {
		t.Fatalf("first Refresh() error = %v", err)
	}

	_, err = authService.Refresh(context.Background(), login.RefreshToken)
	if !errors.Is(err, ErrRefreshReused) {
		t.Fatalf("reused Refresh() error = %v, want ErrRefreshReused", err)
	}

	rotatedHash := hashToken(rotated.RefreshToken)
	if refreshRepo.tokens[rotatedHash].revokedAt == nil {
		t.Fatal("detecting reuse must revoke every token in the family, including the one from the legitimate rotation")
	}

	_, err = authService.Refresh(context.Background(), rotated.RefreshToken)
	if !errors.Is(err, ErrRefreshReused) {
		t.Fatalf("Refresh() with the now-revoked rotated token error = %v, want ErrRefreshReused", err)
	}
}

func TestAuthServiceRefreshAfterLogoutIsRejected(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	refreshRepo := newRefreshTokenRepositoryStub()
	repo, login := loginTestUser(t, refreshRepo)
	authService := NewAuthService(repo, refreshRepo)

	if err := authService.Logout(context.Background(), login.RefreshToken); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}

	_, err := authService.Refresh(context.Background(), login.RefreshToken)
	if !errors.Is(err, ErrRefreshReused) {
		t.Fatalf("Refresh() after logout error = %v, want ErrRefreshReused", err)
	}
}

func TestAuthServiceRefreshRejectsWhenUserNoLongerExists(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	refreshRepo := newRefreshTokenRepositoryStub()
	plainToken := "orphan-plain-token"
	refreshRepo.tokens[hashToken(plainToken)] = &refreshTokenRecord{
		userID:    "550e8400-e29b-41d4-a716-446655440099",
		familyID:  "family-2",
		expiresAt: time.Now().Add(time.Hour),
	}
	repo := &userRepositoryStub{existingUser: nil}

	_, err := NewAuthService(repo, refreshRepo).Refresh(context.Background(), plainToken)

	if !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("Refresh() error = %v, want ErrInvalidRefreshToken", err)
	}
}

func TestAuthServiceSetPasswordConfiguresAHashForAFederatedAccount(t *testing.T) {
	user := &model.User{ID: "550e8400-e29b-41d4-a716-446655440000", Email: "ada@example.com"}
	repo := &userRepositoryStub{existingUser: user}
	svc := NewAuthService(repo, nil)

	if err := svc.SetPassword(context.Background(), user.ID, "Secure123"); err != nil {
		t.Fatalf("SetPassword() error = %v", err)
	}

	if repo.updatedPasswordUser != user.ID {
		t.Fatalf("UpdatePasswordHash() called for user %q, want %q", repo.updatedPasswordUser, user.ID)
	}
	if repo.updatedPasswordHash == "" || repo.updatedPasswordHash == "Secure123" {
		t.Fatalf("password must be persisted as a bcrypt hash, never in plain text: %q", repo.updatedPasswordHash)
	}
	if bcrypt.CompareHashAndPassword([]byte(repo.updatedPasswordHash), []byte("Secure123")) != nil {
		t.Fatal("persisted hash does not match the password that was set")
	}
}

func TestAuthServiceSetPasswordRejectsAccountsThatAlreadyHaveOne(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("Existing123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	user := &model.User{
		ID:           "550e8400-e29b-41d4-a716-446655440000",
		PasswordHash: sql.NullString{String: string(passwordHash), Valid: true},
	}
	repo := &userRepositoryStub{existingUser: user}
	svc := NewAuthService(repo, nil)

	err = svc.SetPassword(context.Background(), user.ID, "Secure123")

	if !errors.Is(err, ErrPasswordAlreadySet) {
		t.Fatalf("SetPassword() error = %v, want ErrPasswordAlreadySet", err)
	}
	if repo.updatedPasswordHash != "" {
		t.Fatal("must not overwrite an existing password hash")
	}
}

func TestAuthServiceSetPasswordRejectsInsecurePasswords(t *testing.T) {
	user := &model.User{ID: "550e8400-e29b-41d4-a716-446655440000"}
	repo := &userRepositoryStub{existingUser: user}
	svc := NewAuthService(repo, nil)

	err := svc.SetPassword(context.Background(), user.ID, "weak")

	if !errors.Is(err, ErrPasswordInsecure) {
		t.Fatalf("SetPassword() error = %v, want ErrPasswordInsecure", err)
	}
}

func TestAuthServiceSetPasswordRejectsUnknownUser(t *testing.T) {
	repo := &userRepositoryStub{existingUser: nil}
	svc := NewAuthService(repo, nil)

	err := svc.SetPassword(context.Background(), "550e8400-e29b-41d4-a716-446655440099", "Secure123")

	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("SetPassword() error = %v, want ErrUserNotFound", err)
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
