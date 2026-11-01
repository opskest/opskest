// Package secret encrypts values that must never be stored or logged in
// plaintext: secret headers, basic auth passwords, bearer tokens and
// channel configs (webhook URLs with embedded tokens).
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
)

// keyLen is the required AES-256 key size in bytes.
const keyLen = 32

// Box seals and opens values with AES-256-GCM. The nonce is generated
// fresh for every Seal and prepended to the ciphertext.
type Box struct {
	aead cipher.AEAD
}

// New builds a Box from a 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != keyLen {
		return nil, fmt.Errorf("secret: key must be %d bytes, got %d", keyLen, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secret: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secret: new gcm: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plain and returns nonce||ciphertext.
func (b *Box) Seal(plain []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secret: read nonce: %w", err)
	}
	sealed := b.aead.Seal(nonce, nonce, plain, nil)
	return sealed, nil
}

// Open decrypts a value produced by Seal.
func (b *Box) Open(sealed []byte) ([]byte, error) {
	nonceSize := b.aead.NonceSize()
	if len(sealed) < nonceSize {
		return nil, errors.New("secret: ciphertext too short")
	}
	nonce, ciphertext := sealed[:nonceSize], sealed[nonceSize:]
	plain, err := b.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("secret: open: %w", err)
	}
	return plain, nil
}

// SealJSON marshals v to JSON and seals it.
func (b *Box) SealJSON(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("secret: marshal: %w", err)
	}
	return b.Seal(data)
}

// OpenJSON opens sealed and unmarshals it into v.
func (b *Box) OpenJSON(sealed []byte, v any) error {
	data, err := b.Open(sealed)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("secret: unmarshal: %w", err)
	}
	return nil
}
