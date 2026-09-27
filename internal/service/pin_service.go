package service

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrPinInvalidFormat = errors.New("el PIN debe tener entre 6 y 8 dígitos numéricos")
	ErrPinNotConfigured = errors.New("este dispositivo no tiene un PIN configurado")
	ErrPinIncorrect     = errors.New("el PIN ingresado es incorrecto")
	ErrPinLocked        = errors.New("demasiados intentos fallidos: el acceso por PIN quedó bloqueado temporalmente, iniciá sesión con tu email y contraseña")
)

var pinFormat = regexp.MustCompile(`^\d{6,8}$`)

const (
	maxPinAttempts  = 5
	pinLockDuration = 15 * time.Minute
)

type PinLockedError struct {
	RetryAfter time.Duration
}

func (e *PinLockedError) Error() string { return ErrPinLocked.Error() }
func (e *PinLockedError) Unwrap() error { return ErrPinLocked }

type PinService interface {
	ConfigurePin(ctx context.Context, userID string, deviceID string, pin string) error
	LoginWithPin(ctx context.Context, deviceID string, pin string) (*model.LoginResult, error)
}

type pinService struct {
	pinRepo     repository.DevicePinRepository
	userRepo    repository.UserRepository
	refreshRepo repository.RefreshTokenRepository
	twoFactor   TwoFactorChallenger
}

// PinOption configura dependencias opcionales de pinService, igual que
// AuthOption en authService: sin TwoFactorChallenger el login con PIN nunca
// pide segundo factor.
type PinOption func(*pinService)

func WithPinTwoFactor(challenger TwoFactorChallenger) PinOption {
	return func(s *pinService) { s.twoFactor = challenger }
}

func NewPinService(
	pinRepo repository.DevicePinRepository,
	userRepo repository.UserRepository,
	refreshRepo repository.RefreshTokenRepository,
	opts ...PinOption,
) PinService {
	s := &pinService{pinRepo: pinRepo, userRepo: userRepo, refreshRepo: refreshRepo}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *pinService) ConfigurePin(ctx context.Context, userID string, deviceID string, pin string) error {
	if !pinFormat.MatchString(pin) {
		return ErrPinInvalidFormat
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	return s.pinRepo.Upsert(ctx, &model.DevicePin{
		ID:       uuid.NewString(),
		UserID:   userID,
		DeviceID: deviceID,
		PinHash:  string(hash),
	})
}

func (s *pinService) LoginWithPin(ctx context.Context, deviceID string, pin string) (*model.LoginResult, error) {
	if !pinFormat.MatchString(pin) {
		return nil, ErrPinInvalidFormat
	}

	devicePin, err := s.pinRepo.GetByDeviceID(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if devicePin == nil {
		return nil, ErrPinNotConfigured
	}

	if devicePin.LockedUntil != nil {
		if devicePin.LockedUntil.After(time.Now()) {
			return nil, &PinLockedError{RetryAfter: devicePin.LockedUntil.Sub(time.Now())}
		}
		if err := s.pinRepo.ResetFailedAttempts(ctx, deviceID); err != nil {
			return nil, err
		}
		devicePin.FailedAttempts = 0
		devicePin.LockedUntil = nil
	}

	if bcrypt.CompareHashAndPassword([]byte(devicePin.PinHash), []byte(pin)) != nil {
		attempts, err := s.pinRepo.IncrementFailedAttempts(ctx, deviceID)
		if err != nil {
			return nil, err
		}
		if attempts >= maxPinAttempts {
			if err := s.pinRepo.Lock(ctx, deviceID, time.Now().Add(pinLockDuration)); err != nil {
				return nil, err
			}
			return nil, &PinLockedError{RetryAfter: pinLockDuration}
		}
		return nil, ErrPinIncorrect
	}

	if err := s.pinRepo.ResetFailedAttempts(ctx, deviceID); err != nil {
		return nil, err
	}

	user, err := s.userRepo.GetByID(ctx, devicePin.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrPinNotConfigured
	}
	if !user.EmailVerified {
		return nil, ErrEmailNotVerified
	}

	// El PIN es un atajo del login de esta cuenta en este dispositivo, no una
	// forma alternativa de esquivar el segundo factor: si la cuenta tiene 2FA
	// activo, lo pide igual que el login con email/contrasena.
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

	auth, err := issueTokenPair(ctx, s.refreshRepo, user, uuid.NewString())
	if err != nil {
		return nil, err
	}
	return &model.LoginResult{Auth: auth}, nil
}
