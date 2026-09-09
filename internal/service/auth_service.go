package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"regexp"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrPasswordInsecure   = errors.New("la contraseña debe tener al menos 8 caracteres, incluir una mayúscula, una minúscula y un número")
	ErrEmailInUse         = errors.New("el correo electrónico ya está registrado")
	ErrInvalidCredentials = errors.New("correo electrónico o contraseña inválidos")
	ErrInexistentSession  = errors.New("la sesión no existe o ya ha sido revocada")
)

const (
	HOURS_TO_EXPIRE = 72
)

type AuthService interface {
	Register(ctx context.Context, req model.RegisterRequest) (*model.AuthResponse, error)
	Login(ctx context.Context, req model.LoginRequest) (*model.AuthResponse, error)
	Logout(ctx context.Context, tokenID string) error
}

type authService struct {
	repo        repository.UserRepository
	sessionRepo repository.SessionRepository
}

func NewAuthService(repo repository.UserRepository, sessionRepos ...repository.SessionRepository) AuthService {
	var sessionRepo repository.SessionRepository
	if len(sessionRepos) > 0 {
		sessionRepo = sessionRepos[0]
	}
	return &authService{repo: repo, sessionRepo: sessionRepo}
}

func (s *authService) Register(ctx context.Context, req model.RegisterRequest) (*model.AuthResponse, error) {

	if !validatePassword(req.Password) {
		return nil, ErrPasswordInsecure
	}

	existingUser, err := s.repo.GetByEmail(ctx, req.Email)
	if err != nil {
		return nil, err
	}
	if existingUser != nil {
		return nil, ErrEmailInUse
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	user := &model.User{
		Name:         req.Name,
		Email:        req.Email,
		PasswordHash: string(hashedPassword),
	}

	if err := s.repo.Create(ctx, user); err != nil {
		return nil, err
	}

	token, tokenID, expiresAt, err := generateJWT(user.ID)
	if err != nil {
		return nil, err
	}
	if s.sessionRepo != nil {
		if err := s.sessionRepo.Create(ctx, tokenID, user.ID, expiresAt); err != nil {
			return nil, err
		}
	}

	createdAtUTC := user.CreatedAt.UTC()

	return &model.AuthResponse{
		User: model.UserResponse{
			ID:        user.ID,
			Name:      user.Name,
			Email:     user.Email,
			CreatedAt: createdAtUTC,
		},
		Token: token,
	}, nil
}

func (s *authService) Login(ctx context.Context, req model.LoginRequest) (*model.AuthResponse, error) {
	user, err := s.repo.GetByEmail(ctx, req.Email)
	if err != nil {
		return nil, err
	}
	if user == nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
		return nil, ErrInvalidCredentials
	}

	token, tokenID, expiresAt, err := generateJWT(user.ID)
	if err != nil {
		return nil, err
	}
	if s.sessionRepo != nil {
		if err := s.sessionRepo.Create(ctx, tokenID, user.ID, expiresAt); err != nil {
			return nil, err
		}
	}

	return &model.AuthResponse{
		User: model.UserResponse{
			ID:        user.ID,
			Name:      user.Name,
			Email:     user.Email,
			CreatedAt: user.CreatedAt.UTC(),
		},
		Token: token,
	}, nil
}

func (s *authService) Logout(ctx context.Context, tokenID string) error {
	if s.sessionRepo == nil {
		return ErrInexistentSession
	}
	if err := s.sessionRepo.Revoke(ctx, tokenID); err != nil {
		if errors.Is(err, repository.ErrSessionNotFound) {
			return ErrInexistentSession
		}
		return err
	}
	return nil
}

func validatePassword(password string) bool {
	if len(password) < 8 {
		return false
	}
	hasUpper := regexp.MustCompile(`[A-Z]`).MatchString(password)
	hasLower := regexp.MustCompile(`[a-z]`).MatchString(password)
	hasNumber := regexp.MustCompile(`[0-9]`).MatchString(password)

	return hasUpper && hasLower && hasNumber
}

func generateJWT(userID int64) (string, string, time.Time, error) {
	secret := os.Getenv("JWT_SECRET")
	tokenID, err := newTokenID()
	if err != nil {
		return "", "", time.Time{}, err
	}
	expiresAt := time.Now().Add(HOURS_TO_EXPIRE * time.Hour)
	claims := jwt.MapClaims{
		"sub": userID,
		"jti": tokenID,
		"exp": expiresAt.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString([]byte(secret))
	return signedToken, tokenID, expiresAt, err
}

func newTokenID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
