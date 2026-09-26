package twofactor

import (
	"crypto/rand"
	"math/big"
	"strings"
)

const (
	RecoveryCodeCount = 10

	// recoveryCodeGroups y recoveryGroupSize dan codigos de 10 caracteres
	// partidos en dos mitades ("A1B2C-D3E4F"), mas faciles de leer y tipear.
	recoveryCodeGroups = 2
	recoveryGroupSize  = 5

	// recoveryAlphabet es Crockford base32 sin las letras que se confunden a
	// mano: no hay I, L, O ni U. Ademas no es solo numerico a proposito, asi
	// el paso de verificacion puede distinguir "esto es un codigo de
	// recuperacion" de "esto es un TOTP de 6 digitos" sin preguntar.
	recoveryAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
)

func GenerateRecoveryCodes() ([]string, error) {
	codes := make([]string, 0, RecoveryCodeCount)
	for i := 0; i < RecoveryCodeCount; i++ {
		code, err := generateRecoveryCode()
		if err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, nil
}

func NormalizeRecoveryCode(code string) string {
	var normalized strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(code)) {
		if strings.ContainsRune(recoveryAlphabet, r) {
			normalized.WriteRune(r)
		}
	}
	return normalized.String()
}

func generateRecoveryCode() (string, error) {
	groups := make([]string, 0, recoveryCodeGroups)
	for g := 0; g < recoveryCodeGroups; g++ {
		var group strings.Builder
		for i := 0; i < recoveryGroupSize; i++ {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(recoveryAlphabet))))
			if err != nil {
				return "", err
			}
			group.WriteByte(recoveryAlphabet[n.Int64()])
		}
		groups = append(groups, group.String())
	}
	return strings.Join(groups, "-"), nil
}
