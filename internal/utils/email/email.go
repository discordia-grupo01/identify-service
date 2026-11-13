package email

import (
	"errors"
	"fmt"
	"net/smtp"
	"os"
	"strconv"
)

var (
	ErrInvalidSMTPConfig = errors.New("invalid SMTP configuration")
	ErrInvalidEmail      = errors.New("recipient email is required")
)

type Config struct {
	Host      string
	Port      int
	Username  string
	Password  string
	From      string
	ReplyTo   string
	AppName   string
	ExpiresIn string
}

func ConfigFromEnv() (Config, error) {
	port, err := strconv.Atoi(getEnv("SMTP_PORT", "587"))
	if err != nil {
		return Config{}, fmt.Errorf("SMTP_PORT: %w", err)
	}

	config := Config{
		Host:      os.Getenv("SMTP_HOST"),
		Port:      port,
		Username:  os.Getenv("SMTP_USERNAME"),
		Password:  os.Getenv("SMTP_PASSWORD"),
		From:      os.Getenv("SMTP_FROM"),
		ReplyTo:   os.Getenv("SMTP_REPLY_TO"),
		AppName:   getEnv("APP_NAME", "Discordia"),
		ExpiresIn: getEnv("PASSWORD_RESET_EXPIRATION", "1 hora"),
	}
	return config, config.Validate()
}

func (c Config) Validate() error {
	if c.Host == "" || c.Port < 1 || c.Port > 65535 || c.From == "" {
		return ErrInvalidSMTPConfig
	}
	return nil
}

type MailSender interface {
	Send(from string, to []string, message []byte) error
}

type smtpSender struct {
	config Config
}

func NewSMTPService(config Config) (*EmailService, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return &EmailService{
		config: config,
		sender: smtpSender{config: config},
	}, nil
}

type EmailService struct {
	config Config
	sender MailSender
}

func newService(config Config, sender MailSender) *EmailService {
	return &EmailService{config: config, sender: sender}
}

func (s *EmailService) SendPasswordResetEmail(request PasswordResetRequest) error {
	if request.Email == "" {
		return ErrInvalidEmail
	}

	message, err := renderPasswordResetEmail(s.config, request)
	if err != nil {
		return fmt.Errorf("render password reset email: %w", err)
	}

	return s.sender.Send(s.config.From, []string{request.Email}, message)
}

func (s *EmailService) SendEmailConfirmationEmail(request EmailConfirmationRequest) error {
	if request.Email == "" {
		return ErrInvalidEmail
	}

	message, err := renderEmailConfirmationEmail(s.config, request)
	if err != nil {
		return fmt.Errorf("render email confirmation email: %w", err)
	}

	return s.sender.Send(s.config.From, []string{request.Email}, message)
}

func (s *EmailService) SendAccountRecoveryResultEmail(request AccountRecoveryResultRequest) error {
	if request.Email == "" {
		return ErrInvalidEmail
	}

	message, err := renderAccountRecoveryEmail(s.config, request)
	if err != nil {
		return fmt.Errorf("render account recovery email: %w", err)
	}

	return s.sender.Send(s.config.From, []string{request.Email}, message)
}

func (s smtpSender) Send(from string, to []string, message []byte) error {
	address := fmt.Sprintf("%s:%d", s.config.Host, s.config.Port)

	var auth smtp.Auth
	if s.config.Username != "" {
		auth = smtp.PlainAuth("", s.config.Username, s.config.Password, s.config.Host)
	}

	return smtp.SendMail(address, auth, from, to, message)
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
