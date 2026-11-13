package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/discordia-grupo01/identify-service/internal/logging"
	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/utils/email"
)

var (
	ErrAccountRecoveryAlreadyPending = repository.ErrAccountRecoveryAlreadyPending
	ErrAccountRecoveryMessageTooLong = errors.New("el mensaje no puede superar los 500 caracteres")
)

// AccountRecoveryService cubre la recuperación de una cuenta que perdió su
// segundo factor: el usuario la pide desde el paso de verificación del login
// y el staff la resuelve desde el backoffice (admin), que avisa por evento.
type AccountRecoveryService interface {
	Request(ctx context.Context, request model.AccountRecoveryCreateRequest) (*model.AccountRecoveryCreateResponse, error)
	Resolve(ctx context.Context, requestID, userID string, approved bool) (repository.RecoveryResolution, error)
}

type AccountRecoveryMailer interface {
	SendAccountRecoveryResultEmail(request email.AccountRecoveryResultRequest) error
}

type accountRecoveryService struct {
	repo          repository.AccountRecoveryRepository
	twoFactorRepo repository.TwoFactorRepository
	userRepo      repository.UserRepository
	mailer        AccountRecoveryMailer
	loginURL      string
	now           func() time.Time
	newID         func() string
}

func NewAccountRecoveryService(
	repo repository.AccountRecoveryRepository,
	twoFactorRepo repository.TwoFactorRepository,
	userRepo repository.UserRepository,
	mailer AccountRecoveryMailer,
	loginURL string,
) AccountRecoveryService {
	return &accountRecoveryService{
		repo:          repo,
		twoFactorRepo: twoFactorRepo,
		userRepo:      userRepo,
		mailer:        mailer,
		loginURL:      loginURL,
		now:           time.Now,
		newID:         uuid.NewString,
	}
}

// Request registra la solicitud. El desafío del segundo paso del login prueba
// que el usuario pasó el primer factor; no se consume, así puede volver a la
// verificación si encuentra un código.
func (s *accountRecoveryService) Request(ctx context.Context, request model.AccountRecoveryCreateRequest) (*model.AccountRecoveryCreateResponse, error) {
	message := strings.TrimSpace(request.Message)
	if utf8.RuneCountInString(message) > model.AccountRecoveryMessageMaxLength {
		return nil, ErrAccountRecoveryMessageTooLong
	}

	challenge, err := s.twoFactorRepo.GetChallengeByHash(ctx, hashToken(request.ChallengeToken))
	if err != nil {
		if errors.Is(err, repository.ErrTwoFactorChallengeNotFound) {
			return nil, ErrTwoFactorChallengeInvalid
		}
		return nil, err
	}
	if challenge.UsedAt != nil || s.now().After(challenge.ExpiresAt) {
		return nil, ErrTwoFactorChallengeInvalid
	}

	tf, err := s.twoFactorRepo.GetByUserID(ctx, challenge.UserID)
	if err != nil {
		return nil, err
	}
	if !tf.IsActive() {
		return nil, ErrTwoFactorChallengeInvalid
	}

	user, err := s.userRepo.GetByID(ctx, challenge.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrTwoFactorChallengeInvalid
	}
	if user.SuspendedAt != nil {
		return nil, ErrAccountSuspended
	}

	created := &model.AccountRecoveryRequest{
		ID:        s.newID(),
		UserID:    user.ID,
		Message:   message,
		CreatedAt: s.now().UTC().Truncate(time.Microsecond),
	}
	if err := s.repo.Create(ctx, created); err != nil {
		return nil, err
	}

	return &model.AccountRecoveryCreateResponse{
		RequestID: created.ID,
		Status:    model.AccountRecoveryPending,
		EmailHint: MaskEmail(user.Email),
	}, nil
}

// Resolve aplica la decisión del staff. El mail se manda solo si la solicitud
// cambió de estado (no ante un evento repetido); si falla, la decisión ya
// quedó aplicada y solo se registra el error.
func (s *accountRecoveryService) Resolve(ctx context.Context, requestID, userID string, approved bool) (repository.RecoveryResolution, error) {
	status := model.AccountRecoveryRejected
	if approved {
		status = model.AccountRecoveryApproved
	}

	resolution, err := s.repo.Resolve(ctx, requestID, userID, status, s.now().UTC())
	if err != nil {
		return 0, fmt.Errorf("resolver solicitud de recuperación: %w", err)
	}
	if resolution != repository.RecoveryResolved {
		return resolution, nil
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil || user == nil {
		logging.Warnf("account recovery: solicitud %s resuelta pero no se pudo leer al usuario %s para avisarle: %v", requestID, userID, err)
		return resolution, nil
	}
	if err := s.mailer.SendAccountRecoveryResultEmail(email.AccountRecoveryResultRequest{
		Email:    user.Email,
		Name:     user.Name,
		Approved: approved,
		LoginURL: s.loginURL,
	}); err != nil {
		logging.Warnf("account recovery: solicitud %s resuelta pero no se pudo mandar el email: %v", requestID, err)
	}
	return resolution, nil
}

func MaskEmail(address string) string {
	at := strings.LastIndex(address, "@")
	if at <= 0 {
		return ""
	}
	local, domain := []rune(address[:at]), address[at+1:]
	if len(local) <= 2 {
		return string(local[0]) + "***@" + domain
	}
	return string(local[0]) + "***" + string(local[len(local)-1]) + "@" + domain
}
