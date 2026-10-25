package session

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
)

type IdentifierCipher struct {
	aead cipher.AEAD
}

func NewIdentifierCipher(key []byte) (*IdentifierCipher, error) {
	if len(key) != 32 {
		return nil, errors.New("session encryption key must contain 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrUnavailable
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, ErrUnavailable
	}
	return &IdentifierCipher{aead: aead}, nil
}

func (identifierCipher *IdentifierCipher) Encrypt(sessionID string, identifier Identifier) ([]byte, error) {
	if identifierCipher == nil || identifierCipher.aead == nil || sessionID == "" {
		return nil, ErrUnavailable
	}
	digest, err := identifier.digest()
	if err != nil {
		return nil, ErrInvalid
	}
	additionalData := append([]byte("session-identifier:v1:"+sessionID), digest[:]...)
	return identifierCipher.aead.Seal(nil, nil, []byte(identifier.value), additionalData), nil
}

func (identifierCipher *IdentifierCipher) Decrypt(sessionID string, digest Digest, encrypted []byte) (Identifier, error) {
	if identifierCipher == nil || identifierCipher.aead == nil || sessionID == "" {
		return Identifier{}, ErrUnavailable
	}
	if len(encrypted) != 43+identifierCipher.aead.Overhead() {
		return Identifier{}, ErrUnavailable
	}
	additionalData := append([]byte("session-identifier:v1:"+sessionID), digest[:]...)
	plaintext, err := identifierCipher.aead.Open(nil, nil, encrypted, additionalData)
	if err != nil {
		return Identifier{}, ErrUnavailable
	}
	identifier, err := parseIdentifier(string(plaintext))
	if err != nil {
		return Identifier{}, ErrUnavailable
	}
	actualDigest, err := identifier.digest()
	if err != nil || actualDigest != digest {
		return Identifier{}, ErrUnavailable
	}
	return identifier, nil
}
