// Package security contains credential-at-rest and API-key primitives.
package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

type Encryptor struct{ aead cipher.AEAD }

// NewEncryptor expects a base64-encoded 32-byte key. The key is supplied by
// the deployment secret manager and must never be stored alongside ciphertext.
func NewEncryptor(encodedKey string) (*Encryptor, error) {
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("decode credential encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("credential encryption key must decode to exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Encryptor{aead: aead}, nil
}

// Encrypt returns nonce || authenticated ciphertext. Associated data should
// bind the ciphertext to its owning record (for example account ID + field).
func (e *Encryptor) Encrypt(plaintext, associatedData []byte) ([]byte, error) {
	nonce := make([]byte, e.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return e.aead.Seal(nonce, nonce, plaintext, associatedData), nil
}

func (e *Encryptor) Decrypt(ciphertext, associatedData []byte) ([]byte, error) {
	nonceSize := e.aead.NonceSize()
	if len(ciphertext) < nonceSize+e.aead.Overhead() {
		return nil, fmt.Errorf("ciphertext is truncated")
	}
	nonce, payload := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := e.aead.Open(nil, nonce, payload, associatedData)
	if err != nil {
		return nil, fmt.Errorf("decrypt credential: %w", err)
	}
	return plaintext, nil
}

// HashAPIKey stores a one-way digest rather than the raw gateway key.
func HashAPIKey(key string) []byte { digest := sha256.Sum256([]byte(key)); return digest[:] }

func VerifyAPIKey(key string, expectedHash []byte) bool {
	actual := HashAPIKey(key)
	if len(actual) != len(expectedHash) {
		return false
	}
	var diff byte
	for i := range actual {
		diff |= actual[i] ^ expectedHash[i]
	}
	return diff == 0
}
