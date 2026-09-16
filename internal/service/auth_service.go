package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"regexp"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrPasswordInsecure    = errors.New("la contraseña debe tener al menos 8 caracteres, incluir una mayúscula, una minúscula y un número")
	ErrEmailInUse          = errors.New("el correo electrónico ya está registrado")
	ErrInvalidCredentials  = errors.New("correo electrónico o contraseña inválidos")
	ErrInexistentSession   = errors.New("la sesión no existe o ya ha sido revocada")
	ErrInvalidRefreshToken = errors.New("token de actualización inválido o expirado")
	ErrRefreshReused       = errors.New("el token de actualización ha sido reutilizado y ha sido revocado")
)

const (
	AccessTokenTTL  = 15 * time.Minute
	RefreshTokenTTL = 24 * 30 * time.Hour
	jwtIssuer       = "discordia-issuer"
)

type AuthService interface {
	Register(ctx context.Context, req model.RegisterRequest) (*model.UserResponse, error)
	Login(ctx context.Context, req model.LoginRequest) (*model.AuthResponse, error)
	Logout(ctx context.Context, refreshToken string) error
	Refresh(ctx context.Context, refreshToken string) (*model.AuthResponse, error)
}

type authService struct {
	repo        repository.UserRepository
	refreshRepo repository.RefreshTokenRepository
}

func NewAuthService(repo repository.UserRepository, tokenRepo ...repository.RefreshTokenRepository) AuthService {
	var refreshRepo repository.RefreshTokenRepository
	if len(tokenRepo) > 0 {
		refreshRepo = tokenRepo[0]
	}
	return &authService{repo: repo, refreshRepo: refreshRepo}
}

func (s *authService) Register(ctx context.Context, req model.RegisterRequest) (*model.UserResponse, error) {

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
		ID:           uuid.NewString(),
		Name:         req.Name,
		Email:        req.Email,
		PasswordHash: string(hashedPassword),
	}

	if err := s.repo.Create(ctx, user); err != nil {
		return nil, err
	}

	createdAtUTC := user.CreatedAt.UTC()

	user.CreatedAt = createdAtUTC
	return model.NewUserResponse(user), nil
}

func (s *authService) Login(ctx context.Context, req model.LoginRequest) (*model.AuthResponse, error) {
	user, err := s.repo.GetByEmail(ctx, req.Email)
	if err != nil {
		return nil, err
	}
	if user == nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
		return nil, ErrInvalidCredentials
	}

	return s.issueTokenPair(ctx, user, uuid.NewString())
}

func (s *authService) Logout(ctx context.Context, refreshToken string) error {
	hash := hashToken(refreshToken)
	if err := s.refreshRepo.RevokeByHash(ctx, hash); err != nil {
		if errors.Is(err, repository.ErrRefreshTokenNotFound) {
			return ErrInexistentSession
		}
		return err
	}
	return nil
}

func (s *authService) Refresh(ctx context.Context, refreshToken string) (*model.AuthResponse, error) {
	hash := hashToken(refreshToken)

	rt, err := s.refreshRepo.GetByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, repository.ErrRefreshTokenNotFound) {
			return nil, ErrInvalidRefreshToken
		}
		return nil, err
	}

	if rt.RevokedAt != nil {
		// Posible ataque de reutilización de token: revocamos toda la familia de tokens.
		_ = s.refreshRepo.RevokeFamily(ctx, rt.FamilyID)
		return nil, ErrRefreshReused
	}
	if time.Now().After(rt.ExpiresAt) {
		return nil, ErrInvalidRefreshToken
	}

	if err := s.refreshRepo.RevokeByHash(ctx, hash); err != nil {
		return nil, err
	}

	user, err := s.repo.GetByID(ctx, rt.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrInvalidRefreshToken
	}

	return s.issueTokenPair(ctx, user, rt.FamilyID)
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

func generateAccessJWT(userID string) (string, error) {
	secret := os.Getenv("JWT_SECRET")
	claims := jwt.MapClaims{
		"iss": jwtIssuer,
		"sub": userID,
		"jti": uuid.NewString(),
		"exp": time.Now().Add(AccessTokenTTL).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

func generateRefreshToken() (plain string, hash string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	plain = hex.EncodeToString(buf)
	return plain, hashToken(plain), nil
}

func newTokenID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *authService) issueTokenPair(ctx context.Context, user *model.User, familyID string) (*model.AuthResponse, error) {
	accessToken, err := generateAccessJWT(user.ID)
	if err != nil {
		return nil, err
	}

	refreshPlain, refreshHash, err := generateRefreshToken()
	if err != nil {
		return nil, err
	}

	expiresAt := time.Now().Add(RefreshTokenTTL)
	if err := s.refreshRepo.Create(ctx, refreshHash, user.ID, familyID, expiresAt); err != nil {
		return nil, err
	}

	return &model.AuthResponse{
		User:         *model.NewUserResponse(user),
		Token:        accessToken,
		RefreshToken: refreshPlain,
	}, nil
}
