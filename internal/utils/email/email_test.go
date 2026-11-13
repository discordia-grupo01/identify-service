package email

import (
	"mime"
	"strings"
	"testing"
)

type capturedMail struct {
	from       string
	recipients []string
	message    []byte
}

func (mail *capturedMail) Send(from string, recipients []string, message []byte) error {
	mail.from = from
	mail.recipients = recipients
	mail.message = message
	return nil
}

func TestSendPasswordResetEmail(t *testing.T) {
	captured := &capturedMail{}
	service := newService(Config{
		From:    "no-reply@discordia.test",
		AppName: "Discordia",
	}, captured)

	err := service.SendPasswordResetEmail(PasswordResetRequest{
		Email:     "ana@example.com",
		Name:      "Ana",
		ResetURL:  "https://discordia.test/reset?token=abc",
		ExpiresIn: "1 hora",
	})
	if err != nil {
		t.Fatalf("SendPasswordResetEmail() error = %v", err)
	}

	message := string(captured.message)
	for _, expected := range []string{
		"To: ana@example.com",
		"Subject: Recupera tu contrasena de Discordia",
		"https://discordia.test/reset?token=abc",
		"font-family:Inter,Arial,sans-serif",
		"Recibimos una solicitud",
	} {
		if !strings.Contains(message, expected) {
			t.Errorf("message does not contain %q", expected)
		}
	}
}

func TestSendPasswordResetEmailRejectsInvalidRequest(t *testing.T) {
	service := newService(Config{From: "no-reply@discordia.test"}, &capturedMail{})

	if err := service.SendPasswordResetEmail(PasswordResetRequest{}); err != ErrInvalidEmail {
		t.Fatalf("empty email error = %v, want %v", err, ErrInvalidEmail)
	}
	if err := service.SendPasswordResetEmail(PasswordResetRequest{Email: "invalid"}); err == nil {
		t.Fatal("invalid email should return an error")
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "465")
	t.Setenv("SMTP_FROM", "no-reply@example.com")
	t.Setenv("APP_NAME", "Discordia")

	config, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv() error = %v", err)
	}
	if config.Port != 465 || config.Host != "smtp.example.com" {
		t.Fatalf("ConfigFromEnv() = %#v", config)
	}
}

func TestConfigFromEnvRejectsInvalidPort(t *testing.T) {
	t.Setenv("SMTP_PORT", "invalid")
	_, err := ConfigFromEnv()
	if err == nil {
		t.Fatal("ConfigFromEnv() should return an error for invalid SMTP_PORT")
	}
	if !strings.Contains(err.Error(), "SMTP_PORT") {
		t.Fatalf("ConfigFromEnv() error = %v, want error containing 'SMTP_PORT'", err)
	}
}

func TestSendEmailConfirmationEmail(t *testing.T) {
	captured := &capturedMail{}

	service := newService(Config{
		From:    "no-reply@discordia.test",
		AppName: "Discordia",
	}, captured)

	err := service.SendEmailConfirmationEmail(
		EmailConfirmationRequest{
			Email:      "ada@example.com",
			Name:       "Ada",
			ConfirmURL: "https://discordia.test/confirm?token=abc123",
			ExpiresIn:  "24 horas",
		},
	)

	if err != nil {
		t.Fatalf(
			"SendEmailConfirmationEmail() error = %v",
			err,
		)
	}

	message := string(captured.message)

	for _, expected := range []string{
		"To: ada@example.com",
		"Subject:",
		"https://discordia.test/confirm?token=abc123",
		"Discordia",
		"24 horas",
		"font-family:Inter,Arial,sans-serif",
	} {
		if !strings.Contains(message, expected) {
			t.Errorf(
				"message does not contain %q",
				expected,
			)
		}
	}
}

func TestSendEmailConfirmationEmailRejectsInvalidRequest(t *testing.T) {
	service := newService(
		Config{
			From:    "no-reply@discordia.test",
			AppName: "Discordia",
		},
		&capturedMail{},
	)

	tests := []struct {
		name    string
		request EmailConfirmationRequest
	}{
		{
			name: "empty email",
			request: EmailConfirmationRequest{
				Name:       "Ada",
				ConfirmURL: "https://discordia.test/confirm?token=abc",
				ExpiresIn:  "24 horas",
			},
		},
		{
			name: "invalid email",
			request: EmailConfirmationRequest{
				Email:      "invalid",
				Name:       "Ada",
				ConfirmURL: "https://discordia.test/confirm?token=abc",
				ExpiresIn:  "24 horas",
			},
		},
		{
			name: "empty confirmation URL",
			request: EmailConfirmationRequest{
				Email:     "ada@example.com",
				Name:      "Ada",
				ExpiresIn: "24 horas",
			},
		},
		{
			name: "empty token expiration",
			request: EmailConfirmationRequest{
				Email:      "ada@example.com",
				Name:       "Ada",
				ConfirmURL: "https://discordia.test/confirm?token=abc",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := service.SendEmailConfirmationEmail(tt.request)

			if err == nil {
				t.Fatal(
					"SendEmailConfirmationEmail() should return an error",
				)
			}
		})
	}
}

func TestSendEmailConfirmationEmailContainsConfirmationURL(t *testing.T) {
	captured := &capturedMail{}

	service := newService(
		Config{
			From:    "no-reply@discordia.test",
			AppName: "Discordia",
		},
		captured,
	)

	const confirmURL = "https://discordia.test/confirm?token=super-secret-token"

	err := service.SendEmailConfirmationEmail(
		EmailConfirmationRequest{
			Email:      "ada@example.com",
			Name:       "Ada",
			ConfirmURL: confirmURL,
			ExpiresIn:  "24 horas",
		},
	)

	if err != nil {
		t.Fatalf(
			"SendEmailConfirmationEmail() error = %v",
			err,
		)
	}

	message := string(captured.message)

	if !strings.Contains(message, confirmURL) {
		t.Fatalf(
			"email does not contain confirmation URL %q",
			confirmURL,
		)
	}
}

func TestSendAccountRecoveryResultEmailApproved(t *testing.T) {
	captured := &capturedMail{}
	service := newService(Config{From: "no-reply@discordia.test", AppName: "Discordia"}, captured)

	err := service.SendAccountRecoveryResultEmail(AccountRecoveryResultRequest{
		Email:    "ana@example.com",
		Name:     "Ana",
		Approved: true,
		LoginURL: "https://discordia.test/login",
	})
	if err != nil {
		t.Fatalf("SendAccountRecoveryResultEmail() error = %v", err)
	}

	message := string(captured.message)
	for _, expected := range []string{
		"To: ana@example.com",
		"Subject: " + mime.QEncoding.Encode("utf-8", "Recuperaste el acceso a tu cuenta"),
		"desactivamos la verificación en dos pasos",
		"Ajustes",
		`href="https://discordia.test/login"`,
		"Iniciar sesión",
	} {
		if !strings.Contains(message, expected) {
			t.Errorf("message does not contain %q", expected)
		}
	}
	if strings.Contains(message, "no pudimos validarla") {
		t.Error("an approval must not carry the rejection text")
	}
	if len(captured.recipients) != 1 || captured.recipients[0] != "ana@example.com" {
		t.Errorf("recipients = %v", captured.recipients)
	}
}

func TestSendAccountRecoveryResultEmailRejected(t *testing.T) {
	captured := &capturedMail{}
	service := newService(Config{From: "no-reply@discordia.test", AppName: "Discordia"}, captured)

	err := service.SendAccountRecoveryResultEmail(AccountRecoveryResultRequest{Email: "ana@example.com", Name: "Ana"})
	if err != nil {
		t.Fatalf("SendAccountRecoveryResultEmail() error = %v", err)
	}

	message := string(captured.message)
	for _, expected := range []string{
		"Subject: " + mime.QEncoding.Encode("utf-8", "Tu solicitud de recuperación fue rechazada"),
		"no pudimos validarla",
		"sigue activa",
	} {
		if !strings.Contains(message, expected) {
			t.Errorf("message does not contain %q", expected)
		}
	}
	if strings.Contains(message, "Iniciar sesión") {
		t.Error("a rejection must not carry the login button")
	}
}

func TestSendAccountRecoveryResultEmailRejectsInvalidRequest(t *testing.T) {
	service := newService(Config{From: "no-reply@discordia.test"}, &capturedMail{})

	if err := service.SendAccountRecoveryResultEmail(AccountRecoveryResultRequest{}); err != ErrInvalidEmail {
		t.Fatalf("empty email error = %v, want %v", err, ErrInvalidEmail)
	}
	if err := service.SendAccountRecoveryResultEmail(AccountRecoveryResultRequest{Email: "invalid"}); err == nil {
		t.Fatal("invalid email should return an error")
	}
	if err := service.SendAccountRecoveryResultEmail(AccountRecoveryResultRequest{Email: "ana@example.com", Approved: true}); err == nil {
		t.Fatal("an approval without login URL should return an error")
	}
}
