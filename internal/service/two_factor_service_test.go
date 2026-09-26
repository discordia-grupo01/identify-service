package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/utils/twofactor"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

const (
	testUserID   = "550e8400-e29b-41d4-a716-446655440000"
	testPassword = "Secure123"
)

type twoFactorRepositoryStub struct {
	record        *model.UserTwoFactor
	recoveryCodes map[string]bool // hash -> ya usado
	challenges    map[string]*model.TwoFactorChallenge
	nextID        int
}

var _ repository.TwoFactorRepository = (*twoFactorRepositoryStub)(nil)

func newTwoFactorRepositoryStub() *twoFactorRepositoryStub {
	return &twoFactorRepositoryStub{
		recoveryCodes: map[string]bool{},
		challenges:    map[string]*model.TwoFactorChallenge{},
	}
}

func (r *twoFactorRepositoryStub) GetByUserID(_ context.Context, _ string) (*model.UserTwoFactor, error) {
	return r.record, nil
}

func (r *twoFactorRepositoryStub) UpsertSecret(_ context.Context, userID string, encryptedSecret string) error {
	r.record = &model.UserTwoFactor{UserID: userID, Secret: encryptedSecret}
	return nil
}

func (r *twoFactorRepositoryStub) Confirm(_ context.Context, _ string, step int64) error {
	confirmedAt := time.Now()
	r.record.ConfirmedAt = &confirmedAt
	r.record.LastUsedStep = step
	return nil
}

func (r *twoFactorRepositoryStub) MarkStepUsed(_ context.Context, _ string, step int64) error {
	r.record.LastUsedStep = step
	return nil
}

func (r *twoFactorRepositoryStub) Disable(_ context.Context, _ string) error {
	r.record = nil
	r.recoveryCodes = map[string]bool{}
	r.challenges = map[string]*model.TwoFactorChallenge{}
	return nil
}

func (r *twoFactorRepositoryStub) ReplaceRecoveryCodes(_ context.Context, _ string, codeHashes []string) error {
	r.recoveryCodes = map[string]bool{}
	for _, hash := range codeHashes {
		r.recoveryCodes[hash] = false
	}
	return nil
}

func (r *twoFactorRepositoryStub) ConsumeRecoveryCode(_ context.Context, _ string, codeHash string) (bool, error) {
	used, exists := r.recoveryCodes[codeHash]
	if !exists || used {
		return false, nil
	}
	r.recoveryCodes[codeHash] = true
	return true, nil
}

func (r *twoFactorRepositoryStub) CountActiveRecoveryCodes(_ context.Context, _ string) (int, error) {
	count := 0
	for _, used := range r.recoveryCodes {
		if !used {
			count++
		}
	}
	return count, nil
}

func (r *twoFactorRepositoryStub) CreateChallenge(_ context.Context, challenge *model.TwoFactorChallenge) error {
	r.nextID++
	challenge.ID = fmt.Sprintf("challenge-%d", r.nextID)
	stored := *challenge
	r.challenges[challenge.ID] = &stored
	return nil
}

func (r *twoFactorRepositoryStub) GetChallengeByHash(_ context.Context, tokenHash string) (*model.TwoFactorChallenge, error) {
	for _, challenge := range r.challenges {
		if challenge.TokenHash == tokenHash {
			found := *challenge
			return &found, nil
		}
	}
	return nil, repository.ErrTwoFactorChallengeNotFound
}

func (r *twoFactorRepositoryStub) IncrementChallengeAttempts(_ context.Context, id string) (int, error) {
	challenge, ok := r.challenges[id]
	if !ok {
		return 0, repository.ErrTwoFactorChallengeNotFound
	}
	challenge.Attempts++
	return challenge.Attempts, nil
}

func (r *twoFactorRepositoryStub) ConsumeChallenge(_ context.Context, id string) error {
	challenge, ok := r.challenges[id]
	if !ok {
		return repository.ErrTwoFactorChallengeNotFound
	}
	usedAt := time.Now()
	challenge.UsedAt = &usedAt
	return nil
}

// --- Fixture ---

// twoFactorFixture arma un servicio con reloj controlado. El reloj importa
// porque TOTP depende del tiempo: sin poder moverlo no se puede probar ni un
// codigo vencido ni uno reusado.
type twoFactorFixture struct {
	service       TwoFactorService
	repo          *twoFactorRepositoryStub
	userRepo      *userRepositoryStub
	refreshRepo   *refreshTokenRepositoryStub
	secret        string
	recoveryCodes []string
	now           time.Time
}

func newTwoFactorFixture(t *testing.T) *twoFactorFixture {
	t.Helper()
	t.Setenv("JWT_SECRET", "test-secret")

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}

	fixture := &twoFactorFixture{
		repo:        newTwoFactorRepositoryStub(),
		refreshRepo: newRefreshTokenRepositoryStub(),
		now:         time.Date(2026, time.March, 1, 10, 0, 0, 0, time.UTC),
		userRepo: &userRepositoryStub{existingUser: &model.User{
			ID:            testUserID,
			Name:          "Ada Lovelace",
			Email:         "ada@example.com",
			PasswordHash:  sql.NullString{String: string(passwordHash), Valid: true},
			EmailVerified: true,
		}},
	}

	cipher, err := twofactor.NewCipher("clave-de-prueba")
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}

	service := NewTwoFactorService(fixture.repo, fixture.userRepo, fixture.refreshRepo, cipher)
	service.(*twoFactorService).now = func() time.Time { return fixture.now }
	fixture.service = service

	return fixture
}

// activate corre el flujo real de CA1 (pedir QR, confirmar con un codigo) para
// dejar la cuenta con 2FA activo.
func (f *twoFactorFixture) activate(t *testing.T) {
	t.Helper()

	setup, err := f.service.StartSetup(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("StartSetup() error = %v", err)
	}
	f.secret = setup.Secret

	codes, err := f.service.ConfirmSetup(context.Background(), testUserID, f.codeNow(t))
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	f.recoveryCodes = codes

	// El codigo recien usado queda quemado. Se avanza un periodo para que los
	// tests que siguen puedan usar uno nuevo.
	f.advance(twofactor.Period * time.Second)
}

func (f *twoFactorFixture) codeNow(t *testing.T) string {
	t.Helper()
	code, err := totp.GenerateCodeCustom(f.secret, f.now, totp.ValidateOpts{
		Period:    twofactor.Period,
		Skew:      0,
		Digits:    twofactor.Digits,
		Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		t.Fatalf("GenerateCodeCustom() error = %v", err)
	}
	return code
}

func (f *twoFactorFixture) advance(d time.Duration) {
	f.now = f.now.Add(d)
}

// startChallenge hace el primer paso del login y devuelve el token del
// desafio.
func (f *twoFactorFixture) startChallenge(t *testing.T) string {
	t.Helper()
	challenge, err := f.service.StartChallenge(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("StartChallenge() error = %v", err)
	}
	return challenge.ChallengeToken
}

// --- CA1: activacion ---

func TestTwoFactorSetupDoesNotActivateOnItsOwn(t *testing.T) {
	fixture := newTwoFactorFixture(t)

	setup, err := fixture.service.StartSetup(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("StartSetup() error = %v", err)
	}
	if setup.Secret == "" || setup.OtpauthURL == "" {
		t.Fatalf("StartSetup() = %+v, want a secret and an otpauth URL", setup)
	}
	if setup.Account != "ada@example.com" {
		t.Fatalf("Account = %q, want the user email", setup.Account)
	}

	// Un QR escaneado a medias no puede dejar a nadie afuera de su cuenta.
	enabled, err := fixture.service.IsEnabled(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("IsEnabled() error = %v", err)
	}
	if enabled {
		t.Fatal("StartSetup() must not enable 2FA before the code is confirmed")
	}

	status, err := fixture.service.Status(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Enabled || !status.PendingActivation {
		t.Fatalf("Status() = %+v, want pending activation", status)
	}
}

// El secreto no se guarda en claro: quien lea la tabla no puede generar
// codigos sin la clave de cifrado.
func TestTwoFactorSetupStoresTheSecretEncrypted(t *testing.T) {
	fixture := newTwoFactorFixture(t)

	setup, err := fixture.service.StartSetup(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("StartSetup() error = %v", err)
	}

	if fixture.repo.record.Secret == setup.Secret {
		t.Fatal("the stored secret must not be the plaintext secret")
	}
}

func TestTwoFactorSetupReusesAPendingSecret(t *testing.T) {
	fixture := newTwoFactorFixture(t)

	first, err := fixture.service.StartSetup(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("first StartSetup() error = %v", err)
	}
	second, err := fixture.service.StartSetup(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("second StartSetup() error = %v", err)
	}

	// Rotar el secreto acá dejaría muerto el QR ya escaneado en otra pantalla.
	if first.Secret != second.Secret || first.OtpauthURL != second.OtpauthURL {
		t.Fatal("asking for the QR again must reuse the pending secret")
	}
}

func TestTwoFactorActivationEnablesAndReturnsRecoveryCodes(t *testing.T) {
	fixture := newTwoFactorFixture(t)

	setup, err := fixture.service.StartSetup(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("StartSetup() error = %v", err)
	}
	fixture.secret = setup.Secret

	codes, err := fixture.service.ConfirmSetup(context.Background(), testUserID, fixture.codeNow(t))
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}

	if len(codes) != twofactor.RecoveryCodeCount {
		t.Fatalf("len(recovery codes) = %d, want %d", len(codes), twofactor.RecoveryCodeCount)
	}

	status, err := fixture.service.Status(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !status.Enabled || status.PendingActivation {
		t.Fatalf("Status() = %+v, want enabled", status)
	}
	if status.RecoveryCodesRemaining != twofactor.RecoveryCodeCount {
		t.Fatalf("RecoveryCodesRemaining = %d, want %d", status.RecoveryCodesRemaining, twofactor.RecoveryCodeCount)
	}
}

func TestTwoFactorActivationRejectsAWrongCode(t *testing.T) {
	fixture := newTwoFactorFixture(t)

	if _, err := fixture.service.StartSetup(context.Background(), testUserID); err != nil {
		t.Fatalf("StartSetup() error = %v", err)
	}

	_, err := fixture.service.ConfirmSetup(context.Background(), testUserID, "123456")
	if !errors.Is(err, ErrTwoFactorCodeInvalid) {
		t.Fatalf("Activate() error = %v, want ErrTwoFactorCodeInvalid", err)
	}

	enabled, err := fixture.service.IsEnabled(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("IsEnabled() error = %v", err)
	}
	if enabled {
		t.Fatal("a rejected code must not enable 2FA")
	}
}

func TestTwoFactorActivationRequiresScanningTheQRFirst(t *testing.T) {
	fixture := newTwoFactorFixture(t)

	_, err := fixture.service.ConfirmSetup(context.Background(), testUserID, "123456")
	if !errors.Is(err, ErrTwoFactorSetupMissing) {
		t.Fatalf("Activate() error = %v, want ErrTwoFactorSetupMissing", err)
	}
}

func TestTwoFactorSetupAndActivateRejectAnAlreadyActiveAccount(t *testing.T) {
	fixture := newTwoFactorFixture(t)
	fixture.activate(t)

	if _, err := fixture.service.StartSetup(context.Background(), testUserID); !errors.Is(err, ErrTwoFactorAlreadyEnabled) {
		t.Fatalf("StartSetup() error = %v, want ErrTwoFactorAlreadyEnabled", err)
	}
	if _, err := fixture.service.ConfirmSetup(context.Background(), testUserID, fixture.codeNow(t)); !errors.Is(err, ErrTwoFactorAlreadyEnabled) {
		t.Fatalf("Activate() error = %v, want ErrTwoFactorAlreadyEnabled", err)
	}
}

// --- CA2: login con 2FA activo ---

func TestLoginWithTwoFactorActiveReturnsAChallengeInsteadOfASession(t *testing.T) {
	fixture := newTwoFactorFixture(t)
	fixture.activate(t)

	authService := NewAuthService(fixture.userRepo, fixture.refreshRepo, WithTwoFactor(fixture.service))
	result, err := authService.Login(context.Background(), model.LoginRequest{
		Email:    "ada@example.com",
		Password: testPassword,
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	if result.Auth != nil {
		t.Fatal("Login() must not grant a session before the second factor")
	}
	if result.Challenge == nil || result.Challenge.ChallengeToken == "" {
		t.Fatalf("Login() = %+v, want a two factor challenge", result)
	}
	if !result.Challenge.TwoFactorRequired {
		t.Fatal("the challenge must flag that a second factor is required")
	}
	// Lo importante de CA2: hasta acá no se emitió ningún refresh token.
	if len(fixture.refreshRepo.tokens) != 0 {
		t.Fatalf("persisted refresh tokens = %d, want 0", len(fixture.refreshRepo.tokens))
	}
}

func TestLoginWithoutTwoFactorStillReturnsASession(t *testing.T) {
	fixture := newTwoFactorFixture(t)

	authService := NewAuthService(fixture.userRepo, fixture.refreshRepo, WithTwoFactor(fixture.service))
	result, err := authService.Login(context.Background(), model.LoginRequest{
		Email:    "ada@example.com",
		Password: testPassword,
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	if result.Challenge != nil {
		t.Fatal("an account without 2FA must not get a challenge")
	}
	if result.Auth == nil || result.Auth.Token == "" {
		t.Fatalf("Login() = %+v, want a session", result)
	}
}

func TestVerifyLoginWithAValidCodeGrantsTheSession(t *testing.T) {
	fixture := newTwoFactorFixture(t)
	fixture.activate(t)
	challengeToken := fixture.startChallenge(t)

	res, err := fixture.service.VerifyLogin(context.Background(), model.TwoFactorVerifyRequest{
		ChallengeToken: challengeToken,
		Code:           fixture.codeNow(t),
	})
	if err != nil {
		t.Fatalf("VerifyLogin() error = %v", err)
	}

	if res.Token == "" || res.RefreshToken == "" {
		t.Fatalf("VerifyLogin() = %+v, want a full session", res)
	}
	if res.User.ID != testUserID {
		t.Fatalf("User.ID = %q, want %q", res.User.ID, testUserID)
	}
	if res.RecoveryCodeUsed {
		t.Fatal("a TOTP login must not be flagged as a recovery code login")
	}
	if _, ok := fixture.refreshRepo.tokens[hashToken(res.RefreshToken)]; !ok {
		t.Fatal("VerifyLogin() must persist the refresh token")
	}
}

// Un desafio es de un solo uso: si no, el mismo token produciria dos sesiones.
func TestVerifyLoginBurnsTheChallenge(t *testing.T) {
	fixture := newTwoFactorFixture(t)
	fixture.activate(t)
	challengeToken := fixture.startChallenge(t)

	if _, err := fixture.service.VerifyLogin(context.Background(), model.TwoFactorVerifyRequest{
		ChallengeToken: challengeToken,
		Code:           fixture.codeNow(t),
	}); err != nil {
		t.Fatalf("first VerifyLogin() error = %v", err)
	}

	fixture.advance(twofactor.Period * time.Second)
	_, err := fixture.service.VerifyLogin(context.Background(), model.TwoFactorVerifyRequest{
		ChallengeToken: challengeToken,
		Code:           fixture.codeNow(t),
	})
	if !errors.Is(err, ErrTwoFactorChallengeInvalid) {
		t.Fatalf("second VerifyLogin() error = %v, want ErrTwoFactorChallengeInvalid", err)
	}
}

// --- CA3: codigo incorrecto o expirado ---

func TestVerifyLoginRejectsAWrongCodeWithoutGrantingASession(t *testing.T) {
	fixture := newTwoFactorFixture(t)
	fixture.activate(t)
	challengeToken := fixture.startChallenge(t)

	res, err := fixture.service.VerifyLogin(context.Background(), model.TwoFactorVerifyRequest{
		ChallengeToken: challengeToken,
		Code:           "000000",
	})
	if !errors.Is(err, ErrTwoFactorCodeInvalid) {
		t.Fatalf("VerifyLogin() error = %v, want ErrTwoFactorCodeInvalid", err)
	}
	if res != nil {
		t.Fatalf("VerifyLogin() = %+v, want nil", res)
	}
	if len(fixture.refreshRepo.tokens) != 0 {
		t.Fatalf("persisted refresh tokens = %d, want 0", len(fixture.refreshRepo.tokens))
	}
}

func TestVerifyLoginRejectsAnExpiredCode(t *testing.T) {
	fixture := newTwoFactorFixture(t)
	fixture.activate(t)
	challengeToken := fixture.startChallenge(t)
	staleCode := fixture.codeNow(t)

	// Dos periodos: queda fuera de la ventana de tolerancia.
	fixture.advance(2 * twofactor.Period * time.Second)

	_, err := fixture.service.VerifyLogin(context.Background(), model.TwoFactorVerifyRequest{
		ChallengeToken: challengeToken,
		Code:           staleCode,
	})
	if !errors.Is(err, ErrTwoFactorCodeInvalid) {
		t.Fatalf("VerifyLogin() error = %v, want ErrTwoFactorCodeInvalid", err)
	}
}

// Ver el codigo por encima del hombro no alcanza para reusarlo dentro de su
// ventana de 30s.
func TestVerifyLoginRejectsACodeThatWasAlreadyUsed(t *testing.T) {
	fixture := newTwoFactorFixture(t)
	fixture.activate(t)
	code := fixture.codeNow(t)

	if _, err := fixture.service.VerifyLogin(context.Background(), model.TwoFactorVerifyRequest{
		ChallengeToken: fixture.startChallenge(t),
		Code:           code,
	}); err != nil {
		t.Fatalf("first VerifyLogin() error = %v", err)
	}

	_, err := fixture.service.VerifyLogin(context.Background(), model.TwoFactorVerifyRequest{
		ChallengeToken: fixture.startChallenge(t),
		Code:           code,
	})
	if !errors.Is(err, ErrTwoFactorCodeInvalid) {
		t.Fatalf("replayed VerifyLogin() error = %v, want ErrTwoFactorCodeInvalid", err)
	}
}

func TestVerifyLoginRejectsAnExpiredChallenge(t *testing.T) {
	fixture := newTwoFactorFixture(t)
	fixture.activate(t)
	challengeToken := fixture.startChallenge(t)

	fixture.advance(TwoFactorChallengeTTL + time.Minute)

	_, err := fixture.service.VerifyLogin(context.Background(), model.TwoFactorVerifyRequest{
		ChallengeToken: challengeToken,
		Code:           fixture.codeNow(t),
	})
	if !errors.Is(err, ErrTwoFactorChallengeInvalid) {
		t.Fatalf("VerifyLogin() error = %v, want ErrTwoFactorChallengeInvalid", err)
	}
}

func TestVerifyLoginRejectsAnUnknownChallenge(t *testing.T) {
	fixture := newTwoFactorFixture(t)
	fixture.activate(t)

	_, err := fixture.service.VerifyLogin(context.Background(), model.TwoFactorVerifyRequest{
		ChallengeToken: "nunca-emitido",
		Code:           fixture.codeNow(t),
	})
	if !errors.Is(err, ErrTwoFactorChallengeInvalid) {
		t.Fatalf("VerifyLogin() error = %v, want ErrTwoFactorChallengeInvalid", err)
	}
}

// Sin este corte, un codigo de 6 digitos se adivina a fuerza de reintentos.
func TestVerifyLoginBurnsTheChallengeAfterTooManyFailedAttempts(t *testing.T) {
	fixture := newTwoFactorFixture(t)
	fixture.activate(t)
	challengeToken := fixture.startChallenge(t)

	var lastErr error
	for i := 0; i < maxChallengeAttempts; i++ {
		_, lastErr = fixture.service.VerifyLogin(context.Background(), model.TwoFactorVerifyRequest{
			ChallengeToken: challengeToken,
			Code:           "000000",
		})
	}
	if !errors.Is(lastErr, ErrTwoFactorTooManyAttempts) {
		t.Fatalf("last attempt error = %v, want ErrTwoFactorTooManyAttempts", lastErr)
	}

	// Ni siquiera el codigo correcto entra ya: hay que volver a empezar.
	_, err := fixture.service.VerifyLogin(context.Background(), model.TwoFactorVerifyRequest{
		ChallengeToken: challengeToken,
		Code:           fixture.codeNow(t),
	})
	if !errors.Is(err, ErrTwoFactorChallengeInvalid) {
		t.Fatalf("VerifyLogin() after burning the challenge error = %v, want ErrTwoFactorChallengeInvalid", err)
	}
}

// --- CA4: codigos de recuperacion ---

func TestVerifyLoginWithARecoveryCodeGrantsTheSessionAndConsumesIt(t *testing.T) {
	fixture := newTwoFactorFixture(t)
	fixture.activate(t)

	res, err := fixture.service.VerifyLogin(context.Background(), model.TwoFactorVerifyRequest{
		ChallengeToken: fixture.startChallenge(t),
		Code:           fixture.recoveryCodes[0],
	})
	if err != nil {
		t.Fatalf("VerifyLogin() error = %v", err)
	}

	if res.Token == "" || res.RefreshToken == "" {
		t.Fatalf("VerifyLogin() = %+v, want a full session", res)
	}
	// Con esto el front recomienda regenerar la lista.
	if !res.RecoveryCodeUsed {
		t.Fatal("RecoveryCodeUsed = false, want true")
	}
	if res.RecoveryCodesRemaining != twofactor.RecoveryCodeCount-1 {
		t.Fatalf("RecoveryCodesRemaining = %d, want %d", res.RecoveryCodesRemaining, twofactor.RecoveryCodeCount-1)
	}
}

func TestVerifyLoginRejectsAReusedRecoveryCode(t *testing.T) {
	fixture := newTwoFactorFixture(t)
	fixture.activate(t)

	if _, err := fixture.service.VerifyLogin(context.Background(), model.TwoFactorVerifyRequest{
		ChallengeToken: fixture.startChallenge(t),
		Code:           fixture.recoveryCodes[0],
	}); err != nil {
		t.Fatalf("first VerifyLogin() error = %v", err)
	}

	_, err := fixture.service.VerifyLogin(context.Background(), model.TwoFactorVerifyRequest{
		ChallengeToken: fixture.startChallenge(t),
		Code:           fixture.recoveryCodes[0],
	})
	if !errors.Is(err, ErrTwoFactorCodeInvalid) {
		t.Fatalf("second VerifyLogin() error = %v, want ErrTwoFactorCodeInvalid", err)
	}
}

// Pegar el codigo desde un gestor de contraseñas no tiene que fallar por el
// formato.
func TestVerifyLoginAcceptsARecoveryCodeTypedInAnyFormat(t *testing.T) {
	fixture := newTwoFactorFixture(t)
	fixture.activate(t)

	messy := "  " + strings.ToLower(fixture.recoveryCodes[0]) + "  "
	res, err := fixture.service.VerifyLogin(context.Background(), model.TwoFactorVerifyRequest{
		ChallengeToken: fixture.startChallenge(t),
		Code:           messy,
	})
	if err != nil {
		t.Fatalf("VerifyLogin(%q) error = %v", messy, err)
	}
	if !res.RecoveryCodeUsed {
		t.Fatal("RecoveryCodeUsed = false, want true")
	}
}

func TestVerifyLoginRejectsAnUnknownRecoveryCode(t *testing.T) {
	fixture := newTwoFactorFixture(t)
	fixture.activate(t)

	_, err := fixture.service.VerifyLogin(context.Background(), model.TwoFactorVerifyRequest{
		ChallengeToken: fixture.startChallenge(t),
		Code:           "ZZZZZ-ZZZZZ",
	})
	if !errors.Is(err, ErrTwoFactorCodeInvalid) {
		t.Fatalf("VerifyLogin() error = %v, want ErrTwoFactorCodeInvalid", err)
	}
}

func TestRegenerateRecoveryCodesInvalidatesTheOldList(t *testing.T) {
	fixture := newTwoFactorFixture(t)
	fixture.activate(t)
	oldCode := fixture.recoveryCodes[0]

	newCodes, err := fixture.service.RegenerateRecoveryCodes(context.Background(), testUserID, testPassword)
	if err != nil {
		t.Fatalf("RegenerateRecoveryCodes() error = %v", err)
	}
	if len(newCodes) != twofactor.RecoveryCodeCount {
		t.Fatalf("len(new codes) = %d, want %d", len(newCodes), twofactor.RecoveryCodeCount)
	}

	// Si los viejos siguieran sirviendo, regenerar no protegería de una lista
	// filtrada.
	_, err = fixture.service.VerifyLogin(context.Background(), model.TwoFactorVerifyRequest{
		ChallengeToken: fixture.startChallenge(t),
		Code:           oldCode,
	})
	if !errors.Is(err, ErrTwoFactorCodeInvalid) {
		t.Fatalf("VerifyLogin() with an old code error = %v, want ErrTwoFactorCodeInvalid", err)
	}

	if _, err := fixture.service.VerifyLogin(context.Background(), model.TwoFactorVerifyRequest{
		ChallengeToken: fixture.startChallenge(t),
		Code:           newCodes[0],
	}); err != nil {
		t.Fatalf("VerifyLogin() with a new code error = %v", err)
	}
}

func TestRegenerateRecoveryCodesRequiresThePassword(t *testing.T) {
	fixture := newTwoFactorFixture(t)
	fixture.activate(t)

	_, err := fixture.service.RegenerateRecoveryCodes(context.Background(), testUserID, "Incorrecta1")
	if !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("RegenerateRecoveryCodes() error = %v, want ErrPasswordMismatch", err)
	}
}

// --- CA5: desactivacion ---

func TestDisableTurnsOffTheSecondFactorForFutureLogins(t *testing.T) {
	fixture := newTwoFactorFixture(t)
	fixture.activate(t)

	if err := fixture.service.DisableWithPassword(context.Background(), testUserID, testPassword); err != nil {
		t.Fatalf("Disable() error = %v", err)
	}

	status, err := fixture.service.Status(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Enabled || status.PendingActivation {
		t.Fatalf("Status() = %+v, want disabled", status)
	}

	// CA5: el login vuelve a ser de un solo paso.
	authService := NewAuthService(fixture.userRepo, fixture.refreshRepo, WithTwoFactor(fixture.service))
	result, err := authService.Login(context.Background(), model.LoginRequest{
		Email:    "ada@example.com",
		Password: testPassword,
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if result.Challenge != nil {
		t.Fatal("Login() must not ask for a second factor after disabling it")
	}
	if result.Auth == nil || result.Auth.Token == "" {
		t.Fatalf("Login() = %+v, want a session", result)
	}
}

func TestDisableRejectsAWrongPassword(t *testing.T) {
	fixture := newTwoFactorFixture(t)
	fixture.activate(t)

	err := fixture.service.DisableWithPassword(context.Background(), testUserID, "Incorrecta1")
	if !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("Disable() error = %v, want ErrPasswordMismatch", err)
	}

	// Una sesión abierta ajena no puede dejar la cuenta sin segundo factor.
	enabled, err := fixture.service.IsEnabled(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("IsEnabled() error = %v", err)
	}
	if !enabled {
		t.Fatal("a rejected password must leave 2FA active")
	}
}

func TestDisableRejectsAnAccountWithoutTwoFactor(t *testing.T) {
	fixture := newTwoFactorFixture(t)

	err := fixture.service.DisableWithPassword(context.Background(), testUserID, testPassword)
	if !errors.Is(err, ErrTwoFactorNotEnabled) {
		t.Fatalf("Disable() error = %v, want ErrTwoFactorNotEnabled", err)
	}
}

// Una cuenta creada solo con Google no tiene contraseña para reingresar.
func TestDisableRejectsAnAccountWithoutAPassword(t *testing.T) {
	fixture := newTwoFactorFixture(t)
	fixture.activate(t)
	fixture.userRepo.existingUser.PasswordHash = sql.NullString{}

	err := fixture.service.DisableWithPassword(context.Background(), testUserID, testPassword)
	if !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("Disable() error = %v, want ErrPasswordMismatch", err)
	}
}
