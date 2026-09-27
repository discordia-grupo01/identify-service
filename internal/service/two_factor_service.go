package service

import (
	"context"
	"errors"
	"os"
	"regexp"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/utils/twofactor"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrTwoFactorAlreadyEnabled      = errors.New("el segundo factor ya está activo en esta cuenta")
	ErrTwoFactorNotEnabled          = errors.New("el segundo factor no está activo en esta cuenta")
	ErrTwoFactorSetupMissing        = errors.New("primero hay que escanear el código QR")
	ErrTwoFactorCodeInvalid         = errors.New("el código de verificación es incorrecto o expiró")
	ErrTwoFactorChallengeInvalid    = errors.New("la verificación expiró, volvé a iniciar sesión")
	ErrTwoFactorTooManyAttempts     = errors.New("demasiados intentos fallidos, volvé a iniciar sesión")
	ErrPasswordMismatch             = errors.New("la contraseña es incorrecta")
	ErrPasswordRequiredForTwoFactor = errors.New("primero tenés que configurar una contraseña para esta cuenta")
)

const (
	TwoFactorChallengeTTL = 5 * time.Minute
	maxChallengeAttempts  = 5
)

var totpCodeFormat = regexp.MustCompile(`^\d{6}$`)

type TwoFactorService interface {
	Status(ctx context.Context, userID string) (*model.TwoFactorStatusResponse, error)
	StartSetup(ctx context.Context, userID string) (*model.TwoFactorSetupResponse, error)
	ConfirmSetup(ctx context.Context, userID string, code string) ([]string, error)
	DisableWithPassword(ctx context.Context, userID string, password string) error
	RegenerateRecoveryCodes(ctx context.Context, userID string, password string) ([]string, error)

	IsEnabled(ctx context.Context, userID string) (bool, error)
	StartChallenge(ctx context.Context, userID string) (*model.TwoFactorChallengeResponse, error)

	VerifyLogin(ctx context.Context, req model.TwoFactorVerifyRequest) (*model.TwoFactorVerifyResponse, error)
}

type twoFactorService struct {
	repo        repository.TwoFactorRepository
	userRepo    repository.UserRepository
	refreshRepo repository.RefreshTokenRepository
	cipher      *twofactor.Cipher
	issuer      string
	now         func() time.Time
}

func NewTwoFactorService(
	repo repository.TwoFactorRepository,
	userRepo repository.UserRepository,
	refreshRepo repository.RefreshTokenRepository,
	secretCipher *twofactor.Cipher,
) TwoFactorService {
	issuer := os.Getenv("APP_NAME")
	if issuer == "" {
		issuer = "Discordia"
	}
	return &twoFactorService{
		repo:        repo,
		userRepo:    userRepo,
		refreshRepo: refreshRepo,
		cipher:      secretCipher,
		issuer:      issuer,
		now:         time.Now,
	}
}

func (s *twoFactorService) Status(ctx context.Context, userID string) (*model.TwoFactorStatusResponse, error) {
	tf, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	status := &model.TwoFactorStatusResponse{
		Enabled:           tf.IsActive(),
		PendingActivation: tf != nil && !tf.IsActive(),
	}
	if status.Enabled {
		remaining, err := s.repo.CountActiveRecoveryCodes(ctx, userID)
		if err != nil {
			return nil, err
		}
		status.RecoveryCodesRemaining = remaining
	}
	return status, nil
}

func (s *twoFactorService) StartSetup(ctx context.Context, userID string) (*model.TwoFactorSetupResponse, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrInvalidCredentials
	}
	if !user.PasswordHash.Valid {
		return nil, ErrPasswordRequiredForTwoFactor
	}

	existing, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if existing.IsActive() {
		return nil, ErrTwoFactorAlreadyEnabled
	}

	// Si ya habia una activacion a medias se reusa ese mismo secreto en vez de
	// rotarlo: quien ya escaneo el QR en otra pestaña o pantalla no se queda
	// con una entrada muerta en su app autenticadora.
	if existing != nil {
		secret, err := s.cipher.Decrypt(existing.Secret)
		if err != nil {
			return nil, err
		}
		otpauthURL, err := twofactor.OtpauthURL(secret, s.issuer, user.Email)
		if err != nil {
			return nil, err
		}
		return &model.TwoFactorSetupResponse{
			Secret:     secret,
			OtpauthURL: otpauthURL,
			Issuer:     s.issuer,
			Account:    user.Email,
		}, nil
	}

	credentials, err := twofactor.Generate(s.issuer, user.Email)
	if err != nil {
		return nil, err
	}

	encrypted, err := s.cipher.Encrypt(credentials.Secret)
	if err != nil {
		return nil, err
	}
	if err := s.repo.UpsertSecret(ctx, userID, encrypted); err != nil {
		return nil, err
	}

	return &model.TwoFactorSetupResponse{
		Secret:     credentials.Secret,
		OtpauthURL: credentials.OtpauthURL,
		Issuer:     s.issuer,
		Account:    user.Email,
	}, nil
}

// ConfirmSetup cierra el StartSetup: el codigo que manda solo puede salir del
// secreto que se acaba de escanear, asi que aceptarlo prueba que la app
// autenticadora quedo bien configurada. Recien ahi activa el 2FA y entrega
// los codigos de recuperacion.
func (s *twoFactorService) ConfirmSetup(ctx context.Context, userID string, code string) ([]string, error) {
	tf, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if tf == nil {
		return nil, ErrTwoFactorSetupMissing
	}
	if tf.IsActive() {
		return nil, ErrTwoFactorAlreadyEnabled
	}

	secret, err := s.cipher.Decrypt(tf.Secret)
	if err != nil {
		return nil, err
	}

	step, valid := twofactor.Validate(code, secret, s.now())
	if !valid {
		return nil, ErrTwoFactorCodeInvalid
	}

	if err := s.repo.Confirm(ctx, userID, step); err != nil {
		return nil, err
	}

	return s.issueRecoveryCodes(ctx, userID)
}

func (s *twoFactorService) DisableWithPassword(ctx context.Context, userID string, password string) error {
	if err := s.verifyPassword(ctx, userID, password); err != nil {
		return err
	}

	tf, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		return err
	}
	if !tf.IsActive() {
		return ErrTwoFactorNotEnabled
	}

	return s.repo.Disable(ctx, userID)
}

func (s *twoFactorService) RegenerateRecoveryCodes(ctx context.Context, userID string, password string) ([]string, error) {
	if err := s.verifyPassword(ctx, userID, password); err != nil {
		return nil, err
	}

	tf, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !tf.IsActive() {
		return nil, ErrTwoFactorNotEnabled
	}

	return s.issueRecoveryCodes(ctx, userID)
}

func (s *twoFactorService) IsEnabled(ctx context.Context, userID string) (bool, error) {
	tf, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		return false, err
	}
	return tf.IsActive(), nil
}

func (s *twoFactorService) StartChallenge(ctx context.Context, userID string) (*model.TwoFactorChallengeResponse, error) {
	plain, hash, err := generateRefreshToken()
	if err != nil {
		return nil, err
	}

	challenge := &model.TwoFactorChallenge{
		UserID:    userID,
		TokenHash: hash,
		ExpiresAt: s.now().Add(TwoFactorChallengeTTL),
	}
	if err := s.repo.CreateChallenge(ctx, challenge); err != nil {
		return nil, err
	}

	return &model.TwoFactorChallengeResponse{
		TwoFactorRequired: true,
		ChallengeToken:    plain,
		ExpiresIn:         int(TwoFactorChallengeTTL.Seconds()),
	}, nil
}

func (s *twoFactorService) VerifyLogin(ctx context.Context, req model.TwoFactorVerifyRequest) (*model.TwoFactorVerifyResponse, error) {
	challenge, err := s.repo.GetChallengeByHash(ctx, hashToken(req.ChallengeToken))
	if err != nil {
		if errors.Is(err, repository.ErrTwoFactorChallengeNotFound) {
			return nil, ErrTwoFactorChallengeInvalid
		}
		return nil, err
	}
	if challenge.UsedAt != nil || s.now().After(challenge.ExpiresAt) {
		return nil, ErrTwoFactorChallengeInvalid
	}
	if challenge.Attempts >= maxChallengeAttempts {
		if err := s.repo.ConsumeChallenge(ctx, challenge.ID); err != nil {
			return nil, err
		}
		return nil, ErrTwoFactorTooManyAttempts
	}

	tf, err := s.repo.GetByUserID(ctx, challenge.UserID)
	if err != nil {
		return nil, err
	}
	if !tf.IsActive() {
		// El 2FA se desactivo entre el paso uno y el dos. El desafio ya no
		// significa nada: se vuelve a empezar.
		return nil, ErrTwoFactorChallengeInvalid
	}

	outcome, err := s.consumeCode(ctx, tf, req.Code)
	if err != nil {
		return nil, err
	}
	if !outcome.accepted {
		attempts, err := s.repo.IncrementChallengeAttempts(ctx, challenge.ID)
		if err != nil {
			return nil, err
		}
		if attempts >= maxChallengeAttempts {
			if err := s.repo.ConsumeChallenge(ctx, challenge.ID); err != nil {
				return nil, err
			}
			return nil, ErrTwoFactorTooManyAttempts
		}
		return nil, ErrTwoFactorCodeInvalid
	}

	// El desafio se quema apenas se acepta el codigo: un mismo token no puede
	// producir dos sesiones.
	if err := s.repo.ConsumeChallenge(ctx, challenge.ID); err != nil {
		return nil, err
	}

	user, err := s.userRepo.GetByID(ctx, challenge.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrInvalidCredentials
	}

	auth, err := issueTokenPair(ctx, s.refreshRepo, user, uuid.NewString())
	if err != nil {
		return nil, err
	}

	response := &model.TwoFactorVerifyResponse{
		AuthResponse:     *auth,
		RecoveryCodeUsed: outcome.viaRecoveryCode,
	}
	if outcome.viaRecoveryCode {
		remaining, err := s.repo.CountActiveRecoveryCodes(ctx, challenge.UserID)
		if err != nil {
			return nil, err
		}
		response.RecoveryCodesRemaining = remaining
	}
	return response, nil
}

type codeOutcome struct {
	accepted        bool
	viaRecoveryCode bool
}

func (s *twoFactorService) consumeCode(ctx context.Context, tf *model.UserTwoFactor, code string) (codeOutcome, error) {
	if totpCodeFormat.MatchString(code) {
		secret, err := s.cipher.Decrypt(tf.Secret)
		if err != nil {
			return codeOutcome{}, err
		}

		step, valid := twofactor.Validate(code, secret, s.now())
		// step <= LastUsedStep es un codigo ya consumido: sigue siendo
		// matematicamente valido durante su ventana, pero no se acepta dos
		// veces.
		if !valid || step <= tf.LastUsedStep {
			return codeOutcome{}, nil
		}
		if err := s.repo.MarkStepUsed(ctx, tf.UserID, step); err != nil {
			return codeOutcome{}, err
		}
		return codeOutcome{accepted: true}, nil
	}

	normalized := twofactor.NormalizeRecoveryCode(code)
	if normalized == "" {
		return codeOutcome{}, nil
	}

	consumed, err := s.repo.ConsumeRecoveryCode(ctx, tf.UserID, hashToken(normalized))
	if err != nil {
		return codeOutcome{}, err
	}
	return codeOutcome{accepted: consumed, viaRecoveryCode: consumed}, nil
}

func (s *twoFactorService) issueRecoveryCodes(ctx context.Context, userID string) ([]string, error) {
	codes, err := twofactor.GenerateRecoveryCodes()
	if err != nil {
		return nil, err
	}

	hashes := make([]string, 0, len(codes))
	for _, code := range codes {
		hashes = append(hashes, hashToken(twofactor.NormalizeRecoveryCode(code)))
	}
	if err := s.repo.ReplaceRecoveryCodes(ctx, userID, hashes); err != nil {
		return nil, err
	}
	return codes, nil
}

func (s *twoFactorService) verifyPassword(ctx context.Context, userID string, password string) error {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	// Una cuenta creada solo con Google no tiene password_hash.
	// No hay forma de reingresar la contraseña, asi que el bcrypt falla y
	// devolvemos el mismo error que una contraseña incorrecta.
	if user == nil || !user.PasswordHash.Valid {
		return ErrPasswordMismatch
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash.String), []byte(password)) != nil {
		return ErrPasswordMismatch
	}
	return nil
}
