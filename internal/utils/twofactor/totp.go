// Package twofactor concentra la criptografia del segundo factor: generar el
// secreto TOTP, armar la URI otpauth:// que la app autenticadora entiende,
// validar un codigo contra el reloj, y cifrar el secreto para guardarlo.
//
// Esta aparte del service para que la logica de negocio (cuando se activa,
// que pasa si el codigo esta mal) se lea sin mezclarse con RFC 6238.
package twofactor

import (
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const (
	Period = 30
	Digits = otp.DigitsSix
	Skew = 1
	secretSize = 20
)

var ErrInvalidSecret = errors.New("el secreto TOTP no es base32 válido")

var base32NoPadding = base32.StdEncoding.WithPadding(base32.NoPadding)

type Credentials struct {
	Secret     string
	OtpauthURL string
}

// Generate crea un secreto nuevo para una cuenta. issuer es el nombre que la
// app autenticadora muestra como emisor (ej. "Discordia") y account lo que
// muestra debajo para distinguir cuentas (el email).
func Generate(issuer string, account string) (*Credentials, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: account,
		Period:      Period,
		SecretSize:  secretSize,
		Digits:      Digits,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return nil, err
	}
	return &Credentials{Secret: key.Secret(), OtpauthURL: key.URL()}, nil
}

func OtpauthURL(secret string, issuer string, account string) (string, error) {
	raw, err := decodeSecret(secret)
	if err != nil {
		return "", err
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: account,
		Period:      Period,
		SecretSize:  secretSize,
		Secret:      raw,
		Digits:      Digits,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", err
	}
	return key.URL(), nil
}

func Validate(code string, secret string, now time.Time) (step int64, valid bool) {
	for offset := -Skew; offset <= Skew; offset++ {
		at := now.Add(time.Duration(offset) * Period * time.Second)

		candidate, err := totp.GenerateCodeCustom(secret, at, totp.ValidateOpts{
			Period:    Period,
			Skew:      0,
			Digits:    Digits,
			Algorithm: otp.AlgorithmSHA1,
		})
		if err != nil {
			return 0, false
		}

		if subtle.ConstantTimeCompare([]byte(candidate), []byte(code)) == 1 {
			return at.Unix() / Period, true
		}
	}
	return 0, false
}

func decodeSecret(secret string) ([]byte, error) {
	raw, err := base32NoPadding.DecodeString(secret)
	if err != nil {
		return nil, ErrInvalidSecret
	}
	return raw, nil
}
