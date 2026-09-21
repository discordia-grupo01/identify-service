package service

import (
	"context"
	"errors"
	"regexp"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrPinInvalidFormat = errors.New("el PIN debe tener exactamente 6 dígitos numéricos")
	ErrPinNotConfigured = errors.New("este dispositivo no tiene un PIN configurado")
	ErrPinIncorrect     = errors.New("el PIN ingresado es incorrecto")
)

var pinFormat = regexp.MustCompile(`^\d{6}$`)

type PinService interface {
	ConfigurePin(ctx context.Context, userID string, deviceID string, pin string) error
	LoginWithPin(ctx context.Context, deviceID string, pin string) (*model.AuthResponse, error)
}

type pinService struct {
	pinRepo     repository.DevicePinRepository
	userRepo    repository.UserRepository
	refreshRepo repository.RefreshTokenRepository
}

func NewPinService(
	pinRepo repository.DevicePinRepository,
	userRepo repository.UserRepository,
	refreshRepo repository.RefreshTokenRepository,
) PinService {
	return &pinService{pinRepo: pinRepo, userRepo: userRepo, refreshRepo: refreshRepo}
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

func (s *pinService) LoginWithPin(ctx context.Context, deviceID string, pin string) (*model.AuthResponse, error) {
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
	if bcrypt.CompareHashAndPassword([]byte(devicePin.PinHash), []byte(pin)) != nil {
		return nil, ErrPinIncorrect
	}

	user, err := s.userRepo.GetByID(ctx, devicePin.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrPinNotConfigured
	}

	return issueTokenPair(ctx, s.refreshRepo, user, uuid.NewString())
}
