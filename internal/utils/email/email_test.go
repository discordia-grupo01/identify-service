package email

import (
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
