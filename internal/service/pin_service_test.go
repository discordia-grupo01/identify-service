package service

import (
	"context"
	"errors"
	"testing"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
)

type devicePinRepositoryStub struct {
	byDeviceID map[string]*model.DevicePin
	upsertErr  error
	upserted   []*model.DevicePin
}

var _ repository.DevicePinRepository = (*devicePinRepositoryStub)(nil)

func newDevicePinRepositoryStub() *devicePinRepositoryStub {
	return &devicePinRepositoryStub{byDeviceID: make(map[string]*model.DevicePin)}
}

func (r *devicePinRepositoryStub) GetByDeviceID(_ context.Context, deviceID string) (*model.DevicePin, error) {
	return r.byDeviceID[deviceID], nil
}

func (r *devicePinRepositoryStub) Upsert(_ context.Context, pin *model.DevicePin) error {
	if r.upsertErr != nil {
		return r.upsertErr
	}
	r.byDeviceID[pin.DeviceID] = pin
	r.upserted = append(r.upserted, pin)
	return nil
}

func TestPinServiceConfigurePinRejectsInvalidFormat(t *testing.T) {
	svc := NewPinService(newDevicePinRepositoryStub(), &userRepositoryStub{}, newRefreshTokenRepositoryStub())

	for _, pin := range []string{"", "12345", "123456789", "12a45678", "abcdefgh"} {
		if err := svc.ConfigurePin(context.Background(), "user-1", "device-1", pin); !errors.Is(err, ErrPinInvalidFormat) {
			t.Fatalf("ConfigurePin(%q) error = %v, want ErrPinInvalidFormat (CA1: 6 a 8 dígitos numéricos)", pin, err)
		}
	}
}

func TestPinServiceConfigurePinAcceptsAnyLengthWithinTheRange(t *testing.T) {
	for _, pin := range []string{"123456", "1234567", "12345678"} {
		pinRepo := newDevicePinRepositoryStub()
		svc := NewPinService(pinRepo, &userRepositoryStub{}, newRefreshTokenRepositoryStub())

		if err := svc.ConfigurePin(context.Background(), "user-1", "device-1", pin); err != nil {
			t.Fatalf("ConfigurePin(%q) error = %v, want nil (CA1: 6 a 8 dígitos numéricos)", pin, err)
		}
	}
}

func TestPinServiceConfigurePinStoresAHashNotThePlainPin(t *testing.T) {
	pinRepo := newDevicePinRepositoryStub()
	svc := NewPinService(pinRepo, &userRepositoryStub{}, newRefreshTokenRepositoryStub())

	if err := svc.ConfigurePin(context.Background(), "user-1", "device-1", "123456"); err != nil {
		t.Fatalf("ConfigurePin() error = %v", err)
	}

	stored := pinRepo.byDeviceID["device-1"]
	if stored == nil {
		t.Fatal("expected the PIN to be persisted for device-1")
	}
	if stored.UserID != "user-1" {
		t.Fatalf("stored.UserID = %q, want user-1", stored.UserID)
	}
	if stored.PinHash == "123456" || stored.PinHash == "" {
		t.Fatalf("PinHash must be a bcrypt hash, never the plain PIN: %q", stored.PinHash)
	}
}

func TestPinServiceConfigurePinReplacesAnExistingPinOnTheSameDevice(t *testing.T) {
	pinRepo := newDevicePinRepositoryStub()
	svc := NewPinService(pinRepo, &userRepositoryStub{}, newRefreshTokenRepositoryStub())

	if err := svc.ConfigurePin(context.Background(), "user-1", "device-1", "111111"); err != nil {
		t.Fatalf("first ConfigurePin() error = %v", err)
	}
	if err := svc.ConfigurePin(context.Background(), "user-1", "device-1", "222222"); err != nil {
		t.Fatalf("second ConfigurePin() error = %v", err)
	}

	if len(pinRepo.upserted) != 2 {
		t.Fatalf("upserted = %d, want 2 (reconfiguring must overwrite, not add a second row)", len(pinRepo.upserted))
	}
	if _, err := svc.LoginWithPin(context.Background(), "device-1", "111111"); !errors.Is(err, ErrPinIncorrect) {
		t.Fatalf("LoginWithPin() with the replaced PIN error = %v, want ErrPinIncorrect", err)
	}
}

func TestPinServiceLoginWithPinAllowsVerifiedUser(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	pinRepo := newDevicePinRepositoryStub()
	user := &model.User{ID: "550e8400-e29b-41d4-a716-446655440000", EmailVerified: true}
	userRepo := &userRepositoryStub{existingUser: user}
	svc := NewPinService(pinRepo, userRepo, newRefreshTokenRepositoryStub())

	if err := svc.ConfigurePin(context.Background(), user.ID, "device-1", "654321"); err != nil {
		t.Fatalf("ConfigurePin() error = %v", err)
	}

	result, err := svc.LoginWithPin(context.Background(), "device-1", "654321")

	if err != nil {
		t.Fatalf("LoginWithPin() error = %v", err)
	}
	if result == nil || result.Auth == nil || result.Auth.Token == "" || result.Auth.RefreshToken == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestPinServiceLoginWithPinRejectsUnverifiedUser(t *testing.T) {
	pinRepo := newDevicePinRepositoryStub()
	user := &model.User{ID: "550e8400-e29b-41d4-a716-446655440000"}
	svc := NewPinService(pinRepo, &userRepositoryStub{existingUser: user}, newRefreshTokenRepositoryStub())

	if err := svc.ConfigurePin(context.Background(), user.ID, "device-1", "654321"); err != nil {
		t.Fatalf("ConfigurePin() error = %v", err)
	}

	_, err := svc.LoginWithPin(context.Background(), "device-1", "654321")
	if !errors.Is(err, ErrEmailNotVerified) {
		t.Fatalf("LoginWithPin() error = %v, want ErrEmailNotVerified", err)
	}
}

func TestPinServiceLoginWithPinRejectsAnUnconfiguredDevice(t *testing.T) {
	svc := NewPinService(newDevicePinRepositoryStub(), &userRepositoryStub{}, newRefreshTokenRepositoryStub())

	_, err := svc.LoginWithPin(context.Background(), "device-never-configured", "123456")

	if !errors.Is(err, ErrPinNotConfigured) {
		t.Fatalf("LoginWithPin() error = %v, want ErrPinNotConfigured (CA4: atado a este dispositivo)", err)
	}
}

func TestPinServiceLoginWithPinRejectsAWrongPin(t *testing.T) {
	pinRepo := newDevicePinRepositoryStub()
	user := &model.User{ID: "550e8400-e29b-41d4-a716-446655440000"}
	svc := NewPinService(pinRepo, &userRepositoryStub{existingUser: user}, newRefreshTokenRepositoryStub())

	if err := svc.ConfigurePin(context.Background(), user.ID, "device-1", "654321"); err != nil {
		t.Fatalf("ConfigurePin() error = %v", err)
	}

	_, err := svc.LoginWithPin(context.Background(), "device-1", "000000")

	if !errors.Is(err, ErrPinIncorrect) {
		t.Fatalf("LoginWithPin() error = %v, want ErrPinIncorrect", err)
	}
}

func TestPinServiceLoginWithPinReturnsChallengeWhenAccountHasTwoFactor(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	pinRepo := newDevicePinRepositoryStub()
	user := &model.User{ID: "550e8400-e29b-41d4-a716-446655440000", EmailVerified: true}
	userRepo := &userRepositoryStub{existingUser: user}
	refreshRepo := newRefreshTokenRepositoryStub()
	challenger := &twoFactorChallengerStub{enabledUserIDs: map[string]bool{user.ID: true}}
	svc := NewPinService(pinRepo, userRepo, refreshRepo, WithPinTwoFactor(challenger))

	if err := svc.ConfigurePin(context.Background(), user.ID, "device-1", "654321"); err != nil {
		t.Fatalf("ConfigurePin() error = %v", err)
	}

	result, err := svc.LoginWithPin(context.Background(), "device-1", "654321")

	if err != nil {
		t.Fatalf("LoginWithPin() error = %v", err)
	}
	if result.Auth != nil {
		t.Fatalf("expected no session to be granted before the second factor, got %+v", result.Auth)
	}
	if result.Challenge == nil || !result.Challenge.TwoFactorRequired {
		t.Fatalf("expected a two-factor challenge, got %+v", result)
	}
	if challenger.startedForUser != user.ID {
		t.Fatalf("StartChallenge() called for user %q, want %q (2FA must not be skippable via the device PIN)", challenger.startedForUser, user.ID)
	}
	if len(refreshRepo.tokens) != 0 {
		t.Fatal("no refresh token should be issued while the second factor is pending")
	}
}

func TestPinServiceLoginWithPinSkipsChallengeWhenTwoFactorDisabled(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	pinRepo := newDevicePinRepositoryStub()
	user := &model.User{ID: "550e8400-e29b-41d4-a716-446655440000", EmailVerified: true}
	userRepo := &userRepositoryStub{existingUser: user}
	svc := NewPinService(pinRepo, userRepo, newRefreshTokenRepositoryStub(), WithPinTwoFactor(&twoFactorChallengerStub{}))

	if err := svc.ConfigurePin(context.Background(), user.ID, "device-1", "654321"); err != nil {
		t.Fatalf("ConfigurePin() error = %v", err)
	}

	result, err := svc.LoginWithPin(context.Background(), "device-1", "654321")

	if err != nil {
		t.Fatalf("LoginWithPin() error = %v", err)
	}
	if result.Challenge != nil || result.Auth == nil || result.Auth.Token == "" {
		t.Fatalf("expected a normal session without 2FA, got %+v", result)
	}
}
