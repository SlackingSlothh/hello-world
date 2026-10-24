package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"time"
)

type Identifier struct {
	value string
}

func (Identifier) String() string   { return "Secret" }
func (Identifier) GoString() string { return "Secret" }

type Issued struct {
	identifier Identifier
	expiresAt  time.Time
}

func (Issued) String() string   { return "Secret" }
func (Issued) GoString() string { return "Secret" }

func newIdentifier() (Identifier, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return Identifier{}, ErrUnavailable
	}
	return Identifier{value: base64.RawURLEncoding.EncodeToString(bytes[:])}, nil
}

func parseIdentifier(raw string) (Identifier, error) {
	if len(raw) != 43 { // 32 bytes encoded as unpadded base64url.
		return Identifier{}, ErrInvalid
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != raw {
		return Identifier{}, ErrInvalid
	}
	return Identifier{value: raw}, nil
}

func (identifier Identifier) digest() (Digest, error) {
	if _, err := parseIdentifier(identifier.value); err != nil {
		return Digest{}, err
	}
	return Digest(sha256.Sum256([]byte(identifier.value))), nil
}
