package twofactor

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

func TestGenerateProducesScannableCredentials(t *testing.T) {
	credentials, err := Generate("Discordia", "ada@example.com")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if credentials.Secret == "" {
		t.Fatal("Generate() must return a secret")
	}
	if !strings.HasPrefix(credentials.OtpauthURL, "otpauth://totp/") {
		t.Fatalf("OtpauthURL = %q, want an otpauth:// URI", credentials.OtpauthURL)
	}
	// La app autenticadora muestra estos dos datos; si no viajan en la URI, el
	// usuario ve una entrada sin nombre entre todas sus cuentas.
	for _, want := range []string{"Discordia", "ada@example.com"} {
		if !strings.Contains(credentials.OtpauthURL, want) {
			t.Fatalf("OtpauthURL = %q, want it to contain %q", credentials.OtpauthURL, want)
		}
	}
}

func TestOtpauthURLRebuildsTheSameSecret(t *testing.T) {
	credentials, err := Generate("Discordia", "ada@example.com")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	rebuilt, err := OtpauthURL(credentials.Secret, "Discordia", "ada@example.com")
	if err != nil {
		t.Fatalf("OtpauthURL() error = %v", err)
	}
	if rebuilt != credentials.OtpauthURL {
		t.Fatalf("OtpauthURL() = %q, want %q", rebuilt, credentials.OtpauthURL)
	}
}

func TestOtpauthURLRejectsSecretThatIsNotBase32(t *testing.T) {
	if _, err := OtpauthURL("not base32!", "Discordia", "ada@example.com"); err == nil {
		t.Fatal("OtpauthURL() error = nil, want ErrInvalidSecret")
	}
}

func TestValidateAcceptsCurrentCodeAndReportsItsStep(t *testing.T) {
	credentials, err := Generate("Discordia", "ada@example.com")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	now := time.Now()

	step, valid := Validate(codeAt(t, credentials.Secret, now), credentials.Secret, now)
	if !valid {
		t.Fatal("Validate() must accept the code of the current period")
	}
	if want := now.Unix() / Period; step != want {
		t.Fatalf("step = %d, want %d", step, want)
	}
}

// La ventana de tolerancia existe para relojes desincronizados y para el
// usuario que tipea justo cuando el codigo rota.
func TestValidateAcceptsAdjacentPeriodsAndRejectsTheRest(t *testing.T) {
	credentials, err := Generate("Discordia", "ada@example.com")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	now := time.Now()

	tests := []struct {
		name         string
		periodOffset int
		wantValid    bool
	}{
		{name: "periodo anterior", periodOffset: -1, wantValid: true},
		{name: "periodo actual", periodOffset: 0, wantValid: true},
		{name: "periodo siguiente", periodOffset: 1, wantValid: true},
		{name: "dos periodos atras", periodOffset: -2, wantValid: false},
		{name: "dos periodos adelante", periodOffset: 2, wantValid: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			at := now.Add(time.Duration(test.periodOffset) * Period * time.Second)

			_, valid := Validate(codeAt(t, credentials.Secret, at), credentials.Secret, now)
			if valid != test.wantValid {
				t.Fatalf("Validate() = %v, want %v", valid, test.wantValid)
			}
		})
	}
}

func TestValidateRejectsWrongCode(t *testing.T) {
	credentials, err := Generate("Discordia", "ada@example.com")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if _, valid := Validate("000000", credentials.Secret, time.Now()); valid {
		// 1 en un millon de que "000000" sea el codigo real; si el test
		// parpadea alguna vez es por eso.
		t.Fatal("Validate() must reject a code that does not match the secret")
	}
}

func TestCipherRoundTrip(t *testing.T) {
	c, err := NewCipher("una-clave-de-prueba")
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}

	encrypted, err := c.Encrypt("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if encrypted == "JBSWY3DPEHPK3PXP" {
		t.Fatal("Encrypt() must not return the secret in the clear")
	}

	decrypted, err := c.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if decrypted != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("Decrypt() = %q, want the original secret", decrypted)
	}
}

// Cifrar dos veces lo mismo tiene que dar distinto: si diera igual, mirar la
// tabla revelaria que dos cuentas comparten secreto.
func TestCipherUsesAFreshNoncePerEncryption(t *testing.T) {
	c, err := NewCipher("una-clave-de-prueba")
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}

	first, err := c.Encrypt("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	second, err := c.Encrypt("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if first == second {
		t.Fatal("Encrypt() must produce a different ciphertext each time")
	}
}

func TestCipherRejectsAnotherKeyAndTamperedData(t *testing.T) {
	c, err := NewCipher("clave-a")
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	other, err := NewCipher("clave-b")
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}

	encrypted, err := c.Encrypt("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	if _, err := other.Decrypt(encrypted); err != ErrDecrypt {
		t.Fatalf("Decrypt() with another key error = %v, want ErrDecrypt", err)
	}
	if _, err := c.Decrypt("no es base64 valido %%%"); err != ErrDecrypt {
		t.Fatalf("Decrypt() of garbage error = %v, want ErrDecrypt", err)
	}
	if _, err := c.Decrypt("c2hvcnQ="); err != ErrDecrypt {
		t.Fatalf("Decrypt() of a too-short payload error = %v, want ErrDecrypt", err)
	}
}

func TestNewCipherRejectsEmptyKey(t *testing.T) {
	if _, err := NewCipher(""); err != ErrEmptyEncryptionKey {
		t.Fatalf("NewCipher(\"\") error = %v, want ErrEmptyEncryptionKey", err)
	}
}

func TestGenerateRecoveryCodesAreUniqueAndUnambiguous(t *testing.T) {
	codes, err := GenerateRecoveryCodes()
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes() error = %v", err)
	}
	if len(codes) != RecoveryCodeCount {
		t.Fatalf("len(codes) = %d, want %d", len(codes), RecoveryCodeCount)
	}

	seen := make(map[string]bool, len(codes))
	for _, code := range codes {
		if seen[code] {
			t.Fatalf("duplicated recovery code %q", code)
		}
		seen[code] = true

		if len(code) != recoveryCodeGroups*recoveryGroupSize+1 {
			t.Fatalf("code %q has an unexpected length", code)
		}
		// Sin I, L, O ni U: se confunden con 1, 0 y V al copiarlos a mano.
		if strings.ContainsAny(code, "ILOU") {
			t.Fatalf("code %q contains an ambiguous character", code)
		}
		// Siempre tiene al menos una letra, que es lo que permite
		// distinguirlo de un TOTP de 6 digitos en el paso de verificacion.
		if !strings.ContainsAny(code, "ABCDEFGHJKMNPQRSTVWXYZ") {
			t.Fatalf("code %q is all digits and would be read as a TOTP code", code)
		}
	}
}

func TestNormalizeRecoveryCodeIgnoresFormatting(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "tal cual se genera", input: "A1B2C-D3E4F", want: "A1B2CD3E4F"},
		{name: "en minuscula", input: "a1b2c-d3e4f", want: "A1B2CD3E4F"},
		{name: "con espacios", input: "  a1b2c d3e4f  ", want: "A1B2CD3E4F"},
		{name: "sin separador", input: "A1B2CD3E4F", want: "A1B2CD3E4F"},
		{name: "vacio", input: "   ", want: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := NormalizeRecoveryCode(test.input); got != test.want {
				t.Fatalf("NormalizeRecoveryCode(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

// codeAt calcula el codigo que mostraria la app autenticadora en ese instante.
func codeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateCodeCustom(secret, at, totp.ValidateOpts{
		Period:    Period,
		Skew:      0,
		Digits:    Digits,
		Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		t.Fatalf("GenerateCodeCustom() error = %v", err)
	}
	return code
}
