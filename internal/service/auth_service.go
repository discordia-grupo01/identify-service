package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
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
	ErrPasswordInsecure      = errors.New("la contraseña debe tener al menos 8 caracteres, incluir una mayúscula, una minúscula y un número")
	ErrEmailInUse            = errors.New("el correo electrónico ya está registrado")
	ErrInvalidCredentials    = errors.New("correo electrónico o contraseña inválidos")
	ErrEmailNotVerified      = errors.New("el correo electrónico todavía no fue verificado")
	ErrInexistentSession     = errors.New("la sesión no existe o ya ha sido revocada")
	ErrInvalidRefreshToken   = errors.New("token de actualización inválido o expirado")
	ErrRefreshReused         = errors.New("el token de actualización ha sido reutilizado y ha sido revocado")
	ErrLoginAttemptsExceeded = errors.New("demasiados intentos fallidos, esperá unos minutos y probá de nuevo")
)

const (
	AccessTokenTTL  = 15 * time.Minute
	RefreshTokenTTL = 24 * 30 * time.Hour
	jwtIssuer       = "discordia-issuer"

	// Se cuenta por (email, ip) y no solo por email: bloquear la cuenta
	// entera dejaria que cualquiera que sepa tu correo te saque de tu propia
	// cuenta a proposito, sin necesidad de saber tu contrasena. Alguien que
	// no comparte tu red solo se frena a si mismo.
	loginAttemptWindow = 15 * time.Minute
	maxLoginAttempts   = 5
)

type AuthService interface {
	Register(ctx context.Context, req model.RegisterRequest) (*model.UserResponse, error)
	Login(ctx context.Context, req model.LoginRequest) (*model.LoginResult, error)
	Logout(ctx context.Context, refreshToken string) error
	Refresh(ctx context.Context, refreshToken string) (*model.AuthResponse, error)
}

type EmailConfirmationRequester interface {
	RequestEmailConfirmation(ctx context.Context, request model.EmailConfirmationRequest) error
}

type TwoFactorChallenger interface {
	IsEnabled(ctx context.Context, userID string) (bool, error)
	StartChallenge(ctx context.Context, userID string) (*model.TwoFactorChallengeResponse, error)
}

type authService struct {
	repo                  repository.UserRepository
	refreshRepo           repository.RefreshTokenRepository
	confirmationRequester EmailConfirmationRequester
	twoFactor             TwoFactorChallenger
	loginAttempts         repository.LoginAttemptRepository
}

// AuthOption configura dependencias opcionales de authService. Son opciones y
// no parametros porque el servicio funciona sin ellas: sin
// EmailConfirmationRequester no manda el mail de alta, y sin
// TwoFactorChallenger el login nunca pide segundo factor.
type AuthOption func(*authService)

func WithEmailConfirmation(requester EmailConfirmationRequester) AuthOption {
	return func(s *authService) { s.confirmationRequester = requester }
}

func WithTwoFactor(challenger TwoFactorChallenger) AuthOption {
	return func(s *authService) { s.twoFactor = challenger }
}

func WithLoginAttempts(repo repository.LoginAttemptRepository) AuthOption {
	return func(s *authService) { s.loginAttempts = repo }
}

func NewAuthService(repo repository.UserRepository, tokenRepo repository.RefreshTokenRepository, opts ...AuthOption) AuthService {
	var refreshRepo repository.RefreshTokenRepository
	if tokenRepo != nil {
		refreshRepo = tokenRepo
	}
	service := &authService{repo: repo, refreshRepo: refreshRepo}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

func (s *authService) Register(ctx context.Context, req model.RegisterRequest) (*model.UserResponse, error) {

	// Mismo criterio que PATCH /v1/me/profile: si no, se puede registrar un
	// nombre que despues el propio usuario no puede volver a guardar. Sin esto
	// un nombre de mas de 100 caracteres llegaba hasta la columna VARCHAR(100)
	// y salia como 500 en vez de 400.
	name, err := normalizeProfileName(req.Name)
	if err != nil {
		return nil, err
	}

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
		Name:         name,
		Email:        req.Email,
		PasswordHash: sql.NullString{String: string(hashedPassword), Valid: true},
	}

	if err := s.repo.Create(ctx, user); err != nil {
		return nil, err
	}
	if s.confirmationRequester != nil {
		if err := s.confirmationRequester.RequestEmailConfirmation(ctx, model.EmailConfirmationRequest{Email: user.Email}); err != nil {
			return nil, fmt.Errorf("send registration confirmation email: %w", err)
		}
	}

	createdAtUTC := user.CreatedAt.UTC()

	user.CreatedAt = createdAtUTC
	return model.NewUserResponse(user), nil
}

func (s *authService) Login(ctx context.Context, req model.LoginRequest) (*model.LoginResult, error) {
	if s.loginAttempts != nil {
		count, err := s.loginAttempts.CountRecentFailures(ctx, req.Email, req.IP, time.Now().UTC().Add(-loginAttemptWindow))
		if err != nil {
			return nil, err
		}
		if count >= maxLoginAttempts {
			return nil, ErrLoginAttemptsExceeded
		}
	}

	user, err := s.repo.GetByEmail(ctx, req.Email)
	if err != nil {
		return nil, err
	}
	if user == nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash.String), []byte(req.Password)) != nil {
		if s.loginAttempts != nil {
			_ = s.loginAttempts.RecordFailure(ctx, req.Email, req.IP)
		}
		return nil, ErrInvalidCredentials
	}
	if s.loginAttempts != nil {
		_ = s.loginAttempts.ClearFailures(ctx, req.Email, req.IP)
	}
	if !user.EmailVerified {
		return nil, ErrEmailNotVerified
	}

	if s.twoFactor != nil {
		enabled, err := s.twoFactor.IsEnabled(ctx, user.ID)
		if err != nil {
			return nil, err
		}
		if enabled {
			challenge, err := s.twoFactor.StartChallenge(ctx, user.ID)
			if err != nil {
				return nil, err
			}
			return &model.LoginResult{Challenge: challenge}, nil
		}
	}

	auth, err := s.issueTokenPair(ctx, user, uuid.NewString())
	if err != nil {
		return nil, err
	}
	return &model.LoginResult{Auth: auth}, nil
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

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *authService) issueTokenPair(ctx context.Context, user *model.User, familyID string) (*model.AuthResponse, error) {
	return issueTokenPair(ctx, s.refreshRepo, user, familyID)
}

// issueTokenPair emite el par de tokens (access JWT + refresh token opaco) y
// persiste el refresh token. Es compartida por authService y oauthService
// para que el mecanismo de sesion (misma cookie, misma rotacion) sea
// identico sin importar como se autentico el usuario.
func issueTokenPair(ctx context.Context, refreshRepo repository.RefreshTokenRepository, user *model.User, familyID string) (*model.AuthResponse, error) {
	accessToken, err := generateAccessJWT(user.ID)
	if err != nil {
		return nil, err
	}

	refreshPlain, refreshHash, err := generateRefreshToken()
	if err != nil {
		return nil, err
	}

	expiresAt := time.Now().Add(RefreshTokenTTL)
	if err := refreshRepo.Create(ctx, refreshHash, user.ID, familyID, expiresAt); err != nil {
		return nil, err
	}

	return &model.AuthResponse{
		User:         *model.NewUserResponse(user),
		Token:        accessToken,
		RefreshToken: refreshPlain,
	}, nil
}
