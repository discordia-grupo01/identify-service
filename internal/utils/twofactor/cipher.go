package twofactor

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

var ErrEmptyEncryptionKey = errors.New("falta la clave de cifrado del segundo factor")
var ErrDecrypt = errors.New("no se pudo descifrar el secreto del segundo factor")

// Cipher cifra el secreto TOTP antes de guardarlo.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher deriva la clave AES-256 desde key. Se pasa por SHA-256 para que
// cualquier string de configuracion sirva como clave, sin exigir que mida
// exactamente 32 bytes.
func NewCipher(key string) (*Cipher, error) {
	if key == "" {
		return nil, ErrEmptyEncryptionKey
	}

	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (c *Cipher) Decrypt(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", ErrDecrypt
	}

	nonceSize := c.aead.NonceSize()
	if len(raw) < nonceSize {
		return "", ErrDecrypt
	}

	plaintext, err := c.aead.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return "", ErrDecrypt
	}
	return string(plaintext), nil
}
