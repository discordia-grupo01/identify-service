package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/discordia-grupo01/identify-service/internal/model"
	"github.com/discordia-grupo01/identify-service/internal/repository"
	"github.com/discordia-grupo01/identify-service/internal/utils/email"
)

const testRecoveryRequestID = "7c9e6679-7425-40de-944b-e07fc1f90ae7"

type accountRecoveryRepositoryStub struct {
	created    []*model.AccountRecoveryRequest
	createErr  error
	resolution repository.RecoveryResolution
	resolveErr error
	resolved   []model.AccountRecoveryStatus
}

var _ repository.AccountRecoveryRepository = (*accountRecoveryRepositoryStub)(nil)

func (r *accountRecoveryRepositoryStub) Create(_ context.Context, request *model.AccountRecoveryRequest) error {
	if r.createErr != nil {
		return r.createErr
	}
	stored := *request
	r.created = append(r.created, &stored)
	return nil
}

func (r *accountRecoveryRepositoryStub) Resolve(_ context.Context, _, _ string, status model.AccountRecoveryStatus, _ time.Time) (repository.RecoveryResolution, error) {
	if r.resolveErr != nil {
		return 0, r.resolveErr
	}
	r.resolved = append(r.resolved, status)
	return r.resolution, nil
}

type accountRecoveryMailerStub struct {
	sent []email.AccountRecoveryResultRequest
	err  error
}

func (m *accountRecoveryMailerStub) SendAccountRecoveryResultEmail(request email.AccountRecoveryResultRequest) error {
	m.sent = append(m.sent, request)
	return m.err
}

type accountRecoveryFixture struct {
	*twoFactorFixture
	recovery     AccountRecoveryService
	recoveryRepo *accountRecoveryRepositoryStub
	mailer       *accountRecoveryMailerStub
}

// newAccountRecoveryFixture deja la cuenta con 2FA activo, como está quien
// llega al paso de verificación del login.
func newAccountRecoveryFixture(t *testing.T) *accountRecoveryFixture {
	t.Helper()
	base := newTwoFactorFixture(t)
	base.activate(t)

	fixture := &accountRecoveryFixture{
		twoFactorFixture: base,
		recoveryRepo:     &accountRecoveryRepositoryStub{},
		mailer:           &accountRecoveryMailerStub{},
	}
	recovery := NewAccountRecoveryService(fixture.recoveryRepo, base.repo, base.userRepo, fixture.mailer, "https://discordia.test/login")
	recovery.(*accountRecoveryService).now = func() time.Time { return base.now }
	recovery.(*accountRecoveryService).newID = func() string { return testRecoveryRequestID }
	fixture.recovery = recovery
	return fixture
}

func TestAccountRecoveryRequestFromTheVerificationStep(t *testing.T) {
	fixture := newAccountRecoveryFixture(t)
	token := fixture.startChallenge(t)

	res, err := fixture.recovery.Request(context.Background(), model.AccountRecoveryCreateRequest{
		ChallengeToken: token,
		Message:        "  cambié de celular y no guardé los códigos  ",
	})
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}

	if res.RequestID != testRecoveryRequestID || res.Status != model.AccountRecoveryPending {
		t.Errorf("response = %+v", res)
	}
	if res.EmailHint != "a***a@example.com" {
		t.Errorf("EmailHint = %q, want a***a@example.com", res.EmailHint)
	}
	if len(fixture.recoveryRepo.created) != 1 {
		t.Fatalf("created = %d requests, want 1", len(fixture.recoveryRepo.created))
	}
	created := fixture.recoveryRepo.created[0]
	if created.UserID != testUserID || created.Message != "cambié de celular y no guardé los códigos" {
		t.Errorf("created = %+v", created)
	}

	// El desafío sigue sirviendo: el usuario puede volver y verificar.
	if _, err := fixture.service.VerifyLogin(context.Background(), model.TwoFactorVerifyRequest{
		ChallengeToken: token, Code: fixture.codeNow(t),
	}); err != nil {
		t.Errorf("VerifyLogin() after the request error = %v", err)
	}
}

func TestAccountRecoveryRequestWithoutMessage(t *testing.T) {
	fixture := newAccountRecoveryFixture(t)

	if _, err := fixture.recovery.Request(context.Background(), model.AccountRecoveryCreateRequest{
		ChallengeToken: fixture.startChallenge(t),
	}); err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	if got := fixture.recoveryRepo.created[0].Message; got != "" {
		t.Errorf("Message = %q, want empty", got)
	}
}

func TestAccountRecoveryRequestMessageLimitCountsCharacters(t *testing.T) {
	fixture := newAccountRecoveryFixture(t)
	token := fixture.startChallenge(t)

	// 500 caracteres con tilde: más de 500 bytes, pero dentro del límite.
	if _, err := fixture.recovery.Request(context.Background(), model.AccountRecoveryCreateRequest{
		ChallengeToken: token, Message: strings.Repeat("ó", 500),
	}); err != nil {
		t.Fatalf("Request() with 500 characters error = %v", err)
	}

	_, err := fixture.recovery.Request(context.Background(), model.AccountRecoveryCreateRequest{
		ChallengeToken: token, Message: strings.Repeat("a", 501),
	})
	if !errors.Is(err, ErrAccountRecoveryMessageTooLong) {
		t.Fatalf("Request() with 501 characters error = %v, want %v", err, ErrAccountRecoveryMessageTooLong)
	}
}

func TestAccountRecoveryRequestRejectsAnUnusableChallenge(t *testing.T) {
	cases := map[string]func(*accountRecoveryFixture, *testing.T) string{
		"inexistente": func(*accountRecoveryFixture, *testing.T) string { return "no-existe" },
		"vencido": func(f *accountRecoveryFixture, t *testing.T) string {
			token := f.startChallenge(t)
			f.advance(TwoFactorChallengeTTL + time.Second)
			return token
		},
		"ya usado": func(f *accountRecoveryFixture, t *testing.T) string {
			token := f.startChallenge(t)
			if _, err := f.service.VerifyLogin(context.Background(), model.TwoFactorVerifyRequest{ChallengeToken: token, Code: f.codeNow(t)}); err != nil {
				t.Fatalf("VerifyLogin() error = %v", err)
			}
			return token
		},
		"2FA desactivado": func(f *accountRecoveryFixture, t *testing.T) string {
			token := f.startChallenge(t)
			f.repo.record = nil
			return token
		},
	}
	for name, tokenFor := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := newAccountRecoveryFixture(t)
			token := tokenFor(fixture, t)

			_, err := fixture.recovery.Request(context.Background(), model.AccountRecoveryCreateRequest{ChallengeToken: token})
			if !errors.Is(err, ErrTwoFactorChallengeInvalid) {
				t.Fatalf("Request() error = %v, want %v", err, ErrTwoFactorChallengeInvalid)
			}
			if len(fixture.recoveryRepo.created) != 0 {
				t.Error("no debería crear la solicitud")
			}
		})
	}
}

func TestAccountRecoveryRequestRejectsASuspendedAccount(t *testing.T) {
	fixture := newAccountRecoveryFixture(t)
	token := fixture.startChallenge(t)
	suspendedAt := fixture.now
	fixture.userRepo.existingUser.SuspendedAt = &suspendedAt

	_, err := fixture.recovery.Request(context.Background(), model.AccountRecoveryCreateRequest{ChallengeToken: token})
	if !errors.Is(err, ErrAccountSuspended) {
		t.Fatalf("Request() error = %v, want %v", err, ErrAccountSuspended)
	}
}

func TestAccountRecoveryRequestWhileAnotherIsPending(t *testing.T) {
	fixture := newAccountRecoveryFixture(t)
	fixture.recoveryRepo.createErr = repository.ErrAccountRecoveryAlreadyPending

	_, err := fixture.recovery.Request(context.Background(), model.AccountRecoveryCreateRequest{ChallengeToken: fixture.startChallenge(t)})
	if !errors.Is(err, ErrAccountRecoveryAlreadyPending) {
		t.Fatalf("Request() error = %v, want %v", err, ErrAccountRecoveryAlreadyPending)
	}
}

func TestAccountRecoveryResolveApprovedSendsTheLoginLink(t *testing.T) {
	fixture := newAccountRecoveryFixture(t)
	fixture.recoveryRepo.resolution = repository.RecoveryResolved

	resolution, err := fixture.recovery.Resolve(context.Background(), testRecoveryRequestID, testUserID, true)
	if err != nil || resolution != repository.RecoveryResolved {
		t.Fatalf("Resolve() = %v, %v", resolution, err)
	}
	if len(fixture.recoveryRepo.resolved) != 1 || fixture.recoveryRepo.resolved[0] != model.AccountRecoveryApproved {
		t.Errorf("resolved = %v, want [approved]", fixture.recoveryRepo.resolved)
	}
	want := email.AccountRecoveryResultRequest{Email: "ada@example.com", Name: "Ada Lovelace", Approved: true, LoginURL: "https://discordia.test/login"}
	if len(fixture.mailer.sent) != 1 || fixture.mailer.sent[0] != want {
		t.Errorf("sent = %+v, want [%+v]", fixture.mailer.sent, want)
	}
}

func TestAccountRecoveryResolveRejected(t *testing.T) {
	fixture := newAccountRecoveryFixture(t)
	fixture.recoveryRepo.resolution = repository.RecoveryResolved

	if _, err := fixture.recovery.Resolve(context.Background(), testRecoveryRequestID, testUserID, false); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if fixture.recoveryRepo.resolved[0] != model.AccountRecoveryRejected {
		t.Errorf("resolved = %v, want [rejected]", fixture.recoveryRepo.resolved)
	}
	if len(fixture.mailer.sent) != 1 || fixture.mailer.sent[0].Approved {
		t.Errorf("sent = %+v, want one rejection", fixture.mailer.sent)
	}
}

func TestAccountRecoveryResolveDoesNotEmailWhenNothingChanged(t *testing.T) {
	for _, resolution := range []repository.RecoveryResolution{repository.RecoveryAlreadyResolved, repository.RecoveryNotFound} {
		fixture := newAccountRecoveryFixture(t)
		fixture.recoveryRepo.resolution = resolution

		got, err := fixture.recovery.Resolve(context.Background(), testRecoveryRequestID, testUserID, true)
		if err != nil || got != resolution {
			t.Errorf("Resolve() = %v, %v, want %v", got, err, resolution)
		}
		if len(fixture.mailer.sent) != 0 {
			t.Errorf("resolution %d: sent %d emails, want none", resolution, len(fixture.mailer.sent))
		}
	}
}

func TestAccountRecoveryResolveKeepsTheDecisionWhenTheEmailFails(t *testing.T) {
	fixture := newAccountRecoveryFixture(t)
	fixture.recoveryRepo.resolution = repository.RecoveryResolved
	fixture.mailer.err = errors.New("smtp caído")

	resolution, err := fixture.recovery.Resolve(context.Background(), testRecoveryRequestID, testUserID, true)
	if err != nil || resolution != repository.RecoveryResolved {
		t.Fatalf("Resolve() = %v, %v, want resolved without error", resolution, err)
	}
}

func TestAccountRecoveryResolvePropagatesRepositoryErrors(t *testing.T) {
	fixture := newAccountRecoveryFixture(t)
	fixture.recoveryRepo.resolveErr = errors.New("db caída")

	if _, err := fixture.recovery.Resolve(context.Background(), testRecoveryRequestID, testUserID, true); err == nil {
		t.Fatal("Resolve() error = nil, want the repository error")
	}
	if len(fixture.mailer.sent) != 0 {
		t.Error("no debería mandar mail si no se pudo resolver")
	}
}

func TestMaskEmail(t *testing.T) {
	cases := map[string]string{
		"ana@gmail.com":     "a***a@gmail.com",
		"aroa.alexia@x.com": "a***a@x.com",
		"ab@x.com":          "a***@x.com",
		"a@x.com":           "a***@x.com",
		"ñandú@correo.ar":   "ñ***ú@correo.ar",
		"sin-arroba":        "",
		"@solo-dominio.com": "",
	}
	for input, want := range cases {
		if got := MaskEmail(input); got != want {
			t.Errorf("MaskEmail(%q) = %q, want %q", input, got, want)
		}
	}
}
