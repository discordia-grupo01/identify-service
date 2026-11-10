package service

import (
	"testing"
	"time"
)

// Invalid values in the environment fall back to the defaults; valid ones
// are used as they are.
func TestRecoveryExpiration(t *testing.T) {
	cases := map[string]time.Duration{
		"30m":  30 * time.Minute,
		"1h":   time.Hour,
		"1ns":  time.Nanosecond,
		"61m":  maxRecoveryExpiration,
		"0s":   maxRecoveryExpiration,
		"-5m":  maxRecoveryExpiration,
		"nope": maxRecoveryExpiration,
	}
	for value, want := range cases {
		t.Setenv("PASSWORD_RESET_EXPIRATION", value)
		if got := recoveryExpiration(); got != want {
			t.Errorf("PASSWORD_RESET_EXPIRATION=%q: got %v, want %v", value, got, want)
		}
	}
}

func TestRecoveryLimit(t *testing.T) {
	cases := map[string]int{"1": 1, "7": 7, "0": defaultRecoveryLimit, "-2": defaultRecoveryLimit, "nope": defaultRecoveryLimit}
	for value, want := range cases {
		t.Setenv("PASSWORD_RESET_MAX_ATTEMPTS", value)
		if got := recoveryLimit(); got != want {
			t.Errorf("PASSWORD_RESET_MAX_ATTEMPTS=%q: got %d, want %d", value, got, want)
		}
	}
}

func TestFormatExpiration(t *testing.T) {
	cases := map[time.Duration]string{time.Hour: "1 hora", 30 * time.Minute: "30m", 45 * time.Minute: "45m"}
	for value, want := range cases {
		if got := formatExpiration(value); got != want {
			t.Errorf("formatExpiration(%v) = %q, want %q", value, got, want)
		}
	}
}

func TestEmailConfirmationExpiration(t *testing.T) {
	cases := map[string]time.Duration{
		"48h":  48 * time.Hour,
		"1ns":  time.Nanosecond,
		"0s":   emailConfirmationDefaultTTL,
		"-1h":  emailConfirmationDefaultTTL,
		"nope": emailConfirmationDefaultTTL,
	}
	for value, want := range cases {
		t.Setenv("EMAIL_CONFIRMATION_EXPIRATION", value)
		if got := emailConfirmationExpiration(); got != want {
			t.Errorf("EMAIL_CONFIRMATION_EXPIRATION=%q: got %v, want %v", value, got, want)
		}
	}
}

func TestEmailConfirmationLimit(t *testing.T) {
	cases := map[string]int{"1": 1, "10": 10, "0": defaultConfirmationLimit, "-1": defaultConfirmationLimit, "nope": defaultConfirmationLimit}
	for value, want := range cases {
		t.Setenv("EMAIL_CONFIRMATION_MAX_ATTEMPTS", value)
		if got := emailConfirmationLimit(); got != want {
			t.Errorf("EMAIL_CONFIRMATION_MAX_ATTEMPTS=%q: got %d, want %d", value, got, want)
		}
	}
}

// Without EMAIL_CONFIRMATION_APP_URL the app link lives on the same domain as
// the confirmation page; a base without scheme or host gives no link.
func TestAppConfirmationBaseURLFromTheConfirmationPage(t *testing.T) {
	t.Setenv("EMAIL_CONFIRMATION_APP_URL", "")
	cases := map[string]string{
		"https://discordia.test/confirm": "https://discordia.test/app-link/confirm-email",
		"discordia.test/confirm":         "",
		"//discordia.test/confirm":       "",
		"https:///confirm":               "",
		"://bad":                         "",
	}
	for base, want := range cases {
		if got := appConfirmationBaseURL(base); got != want {
			t.Errorf("appConfirmationBaseURL(%q) = %q, want %q", base, got, want)
		}
	}
}

func TestBuildConfirmationURLNeedsSchemeAndHost(t *testing.T) {
	got, err := buildConfirmationURL("https://discordia.test/confirm", "abc")
	if err != nil || got != "https://discordia.test/confirm?token=abc" {
		t.Errorf("valid base: got %q, %v", got, err)
	}
	for _, base := range []string{"discordia.test/confirm", "//discordia.test/confirm", "https:///confirm", "://bad"} {
		if _, err := buildConfirmationURL(base, "abc"); err == nil {
			t.Errorf("buildConfirmationURL(%q) should fail", base)
		}
	}
}
