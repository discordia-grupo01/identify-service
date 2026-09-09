package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"golang.org/x/crypto/bcrypt"
)

type userRepositoryStub struct {
	existingUser *model.User
	getErr       error
	createErr    error
	createdUser  *model.User
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
	user.ID = 42
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
	if response.User.ID != 42 || response.User.Name != request.Name || response.User.Email != request.Email {
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
	repo := &userRepositoryStub{existingUser: &model.User{ID: 7, Email: "ada@example.com"}}
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
		ID:           42,
		Name:         "Ada Lovelace",
		Email:        "ada@example.com",
		PasswordHash: string(passwordHash),
		CreatedAt:    time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC),
	}}

	response, err := NewAuthService(repo).Login(context.Background(), model.LoginRequest{
		Email:    "ada@example.com",
		Password: "Secure123",
	})

	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if response == nil || response.Token == "" || response.User.ID != 42 {
		t.Fatalf("unexpected login response: %+v", response)
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

