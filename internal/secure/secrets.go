package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
)

type Vault struct{ cipher.AEAD }

func New(key string) (*Vault, error) {
	b, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(b) != 32 {
		return nil, errors.New("ENCRYPTION_KEY deve contenere 32 byte in base64")
	}
	block, err := aes.NewCipher(b)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead}, nil
}
func (v *Vault) Encrypt(value []byte, context string) ([]byte, error) {
	nonce := make([]byte, v.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return v.Seal(nonce, nonce, value, []byte(context)), nil
}
func (v *Vault) Decrypt(value []byte, context string) ([]byte, error) {
	n := v.NonceSize()
	if len(value) < n {
		return nil, errors.New("credenziali cifrate non valide")
	}
	return v.Open(nil, value[:n], value[n:], []byte(context))
}
