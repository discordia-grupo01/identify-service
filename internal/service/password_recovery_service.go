package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/utils/email"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrRecoveryRateLimited = errors.New("password recovery rate limit exceeded")
	ErrInvalidResetToken   = errors.New("password recovery token is invalid or expired")
	ErrPasswordsDoNotMatch = errors.New("passwords do not match")
)

const (
	passwordRecoveryWindow = time.Hour
	defaultRecoveryLimit   = 3
	maxRecoveryExpiration  = time.Hour
)

type PasswordRecoveryService interface {
	RecoverPassword(ctx context.Context, request model.PasswordRecoveryRequest) error
	ResetPassword(ctx context.Context, request model.PasswordResetRequest) error
}

type PasswordRecoveryMailer interface {
	SendPasswordResetEmail(request email.PasswordResetRequest) error
}

type PasswordRecoveryServiceImpl struct {
	passRepo      repository.PasswordRecoveryRepository
	userRepo      repository.UserRepository
	mailer        PasswordRecoveryMailer
	resetBaseURL  string
	expiration    time.Duration
	recoveryLimit int
	clock         func() time.Time
}

func NewPasswordRecoveryService(passRepo repository.PasswordRecoveryRepository, userRepo repository.UserRepository, mailer PasswordRecoveryMailer) PasswordRecoveryService {
	return &PasswordRecoveryServiceImpl{
		passRepo:      passRepo,
		userRepo:      userRepo,
		mailer:        mailer,
		resetBaseURL:  getEnv("PASSWORD_RESET_URL", "http://localhost:3000/reset-password"),
		expiration:    recoveryExpiration(),
		recoveryLimit: recoveryLimit(),
		clock:         time.Now,
	}
}

func (s *PasswordRecoveryServiceImpl) RecoverPassword(ctx context.Context, request model.PasswordRecoveryRequest) error {
	now := s.clock().UTC()
	count, err := s.passRepo.CountRecentByEmail(ctx, request.Email, now.Add(-passwordRecoveryWindow))
	if err != nil {
		return fmt.Errorf("count password recovery requests: %w", err)
	}
	if count >= s.recoveryLimit {
		return ErrRecoveryRateLimited
	}

	user, err := s.userRepo.GetByEmail(ctx, request.Email)
	if err != nil {
		return fmt.Errorf("find user by email: %w", err)
	}
	if user == nil {
		return nil
	}

	token, err := GenerateResetToken()
	if err != nil {
		return fmt.Errorf("generate reset token: %w", err)
	}
	recovery := &model.PasswordRecovery{
		UserID:    user.ID,
		Email:     user.Email,
		TokenHash: HashResetToken(token),
		ExpiresAt: now.Add(s.expiration),
	}
	if err := s.passRepo.Create(ctx, recovery); err != nil {
		return fmt.Errorf("create password recovery request: %w", err)
	}

	resetURL, err := buildResetURL(s.resetBaseURL, token)
	if err != nil {
		return fmt.Errorf("build password reset URL: %w", err)
	}
	if err := s.mailer.SendPasswordResetEmail(email.PasswordResetRequest{
		Email:     user.Email,
		Name:      user.Name,
		ResetURL:  resetURL,
		ExpiresIn: formatExpiration(s.expiration),
	}); err != nil {
		return fmt.Errorf("send password reset email: %w", err)
	}
	return nil
}

func (s *PasswordRecoveryServiceImpl) ResetPassword(ctx context.Context, request model.PasswordResetRequest) error {
	if request.NewPassword != request.ConfirmPassword {
		return ErrPasswordsDoNotMatch
	}
	if !validatePassword(request.NewPassword) {
		return ErrPasswordInsecure
	}
	if request.Token == "" {
		return ErrInvalidResetToken
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(request.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash new password: %w", err)
	}
	if err := s.passRepo.ResetPassword(ctx, HashResetToken(request.Token), string(passwordHash)); err != nil {
		if errors.Is(err, repository.ErrPasswordRecoveryNotFound) {
			return ErrInvalidResetToken
		}
		return fmt.Errorf("reset password: %w", err)
	}
	return nil
}

func GenerateResetToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func HashResetToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func buildResetURL(baseURL string, token string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("invalid password reset base URL")
	}
	query := parsed.Query()
	query.Set("token", token)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func recoveryExpiration() time.Duration {
	value, err := time.ParseDuration(getEnv("PASSWORD_RESET_EXPIRATION", "1h"))
	if err != nil || value <= 0 || value > maxRecoveryExpiration {
		return maxRecoveryExpiration
	}
	return value
}

func recoveryLimit() int {
	value, err := strconv.Atoi(getEnv("PASSWORD_RESET_MAX_ATTEMPTS", strconv.Itoa(defaultRecoveryLimit)))
	if err != nil || value < 1 {
		return defaultRecoveryLimit
	}
	return value
}

func formatExpiration(expiration time.Duration) string {
	if expiration == time.Hour {
		return "1 hora"
	}
	return strings.TrimSuffix(expiration.String(), "0s")
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
