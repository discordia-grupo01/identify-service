package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/utils/email"
)

var (
	ErrEmailConfirmationRateLimited = errors.New("email confirmation rate limit exceeded")
	ErrEmailConfirmationInvalid     = errors.New("email confirmation token is invalid or expired")
	ErrEmailConfirmationAlreadyUsed = errors.New("email confirmation token has already been used")
)

const (
	emailConfirmationDefaultTTL = 24 * time.Hour
	emailConfirmationWindow     = time.Hour
	defaultConfirmationLimit    = 3
)

type EmailConfirmationService interface {
	RequestEmailConfirmation(ctx context.Context, request model.EmailConfirmationRequest) error
	ConfirmEmail(ctx context.Context, token string) error
}

type EmailConfirmationMailer interface {
	SendEmailConfirmationEmail(request email.EmailConfirmationRequest) error
}

type EmailConfirmationServiceImpl struct {
	confirmationRepo repository.EmailConfirmationRepository
	userRepo         repository.UserRepository
	mailer           EmailConfirmationMailer
	confirmBaseURL   string
	expiration       time.Duration
	limit            int
	clock            func() time.Time
}

func NewEmailConfirmationService(repo repository.EmailConfirmationRepository, userRepo repository.UserRepository, mailer EmailConfirmationMailer, confirmBaseURL string) EmailConfirmationService {
	if confirmBaseURL == "" {
		confirmBaseURL = getEnv("EMAIL_CONFIRMATION_URL", "http://localhost:3000/confirm-email")
	}
	return &EmailConfirmationServiceImpl{
		confirmationRepo: repo,
		userRepo:         userRepo,
		mailer:           mailer,
		confirmBaseURL:   confirmBaseURL,
		expiration:       emailConfirmationExpiration(),
		limit:            emailConfirmationLimit(),
		clock:            time.Now,
	}
}

func (s *EmailConfirmationServiceImpl) RequestEmailConfirmation(ctx context.Context, request model.EmailConfirmationRequest) error {
	now := s.clock().UTC()
	count, err := s.confirmationRepo.CountRecentByEmail(ctx, request.Email, now.Add(-emailConfirmationWindow))
	if err != nil {
		return fmt.Errorf("count email confirmation requests: %w", err)
	}
	if count >= s.limit {
		return ErrEmailConfirmationRateLimited
	}

	user, err := s.userRepo.GetByEmail(ctx, request.Email)
	if err != nil {
		return fmt.Errorf("find user by email: %w", err)
	}
	if user == nil || user.EmailVerified {
		return nil
	}

	token, err := GenerateEmailConfirmationToken()
	if err != nil {
		return fmt.Errorf("generate email confirmation token: %w", err)
	}
	if err := s.confirmationRepo.InvalidatePrevious(ctx, user.ID, HashEmailConfirmationToken(token)); err != nil {
		return fmt.Errorf("invalidate previous confirmation tokens: %w", err)
	}

	confirmation := &model.EmailConfirmation{
		UserID:    user.ID,
		Email:     user.Email,
		TokenHash: HashEmailConfirmationToken(token),
		ExpiresAt: now.Add(s.expiration),
	}
	if err := s.confirmationRepo.Create(ctx, confirmation); err != nil {
		return fmt.Errorf("create email confirmation request: %w", err)
	}

	confirmURL, err := buildConfirmationURL(s.confirmBaseURL, token)
	if err != nil {
		return fmt.Errorf("build email confirmation URL: %w", err)
	}
	if err := s.mailer.SendEmailConfirmationEmail(email.EmailConfirmationRequest{
		Email:      user.Email,
		Name:       user.Name,
		ConfirmURL: confirmURL,
		ExpiresIn:  formatExpiration(s.expiration),
	}); err != nil {
		return fmt.Errorf("send email confirmation email: %w", err)
	}
	return nil
}

func (s *EmailConfirmationServiceImpl) ConfirmEmail(ctx context.Context, token string) error {
	if token == "" {
		return ErrEmailConfirmationInvalid
	}

	confirmation, err := s.confirmationRepo.GetByHash(ctx, HashEmailConfirmationToken(token))
	if err != nil {
		if errors.Is(err, repository.ErrEmailConfirmationNotFound) {
			return ErrEmailConfirmationInvalid
		}
		return fmt.Errorf("find email confirmation token: %w", err)
	}
	if confirmation.UsedAt != nil {
		return ErrEmailConfirmationAlreadyUsed
	}
	if s.clock().After(confirmation.ExpiresAt) {
		return ErrEmailConfirmationInvalid
	}

	if err := s.confirmationRepo.MarkUsed(ctx, confirmation.TokenHash); err != nil {
		if errors.Is(err, repository.ErrEmailConfirmationNotFound) {
			return ErrEmailConfirmationAlreadyUsed
		}
		return fmt.Errorf("mark confirmation as used: %w", err)
	}

	if err := s.userRepo.UpdateEmailVerification(ctx, confirmation.UserID, true); err != nil {
		return fmt.Errorf("mark user email verified: %w", err)
	}
	return nil
}

func GenerateEmailConfirmationToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func HashEmailConfirmationToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func buildConfirmationURL(baseURL string, token string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("invalid email confirmation base URL")
	}
	query := parsed.Query()
	query.Set("token", token)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func emailConfirmationExpiration() time.Duration {
	value, err := time.ParseDuration(getEnv("EMAIL_CONFIRMATION_EXPIRATION", "24h"))
	if err != nil || value <= 0 {
		return emailConfirmationDefaultTTL
	}
	return value
}

func emailConfirmationLimit() int {
	value, err := strconv.Atoi(getEnv("EMAIL_CONFIRMATION_MAX_ATTEMPTS", strconv.Itoa(defaultConfirmationLimit)))
	if err != nil || value < 1 {
		return defaultConfirmationLimit
	}
	return value
}
