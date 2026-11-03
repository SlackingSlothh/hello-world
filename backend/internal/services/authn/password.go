package authn

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"

	"golang.org/x/crypto/argon2"
)

type PasswordHash struct {
	salt   [16]byte
	digest [32]byte
	valid  bool
}

// Optional
// func (PasswordHash) String() string   { return "Rahasia" }
// func (PasswordHash) GoString() string { return "Rahasia" }

func (hash PasswordHash) Encoded() (string, error) {
	if !hash.valid {
		return "", errors.New("invalid password hash")
	}
	return "$argon2id$v=19$m=19456,t=2,p=1$" +
		base64.RawStdEncoding.EncodeToString(hash.salt[:]) + "$" +
		base64.RawStdEncoding.EncodeToString(hash.digest[:]), nil
}

func ParsePasswordHash(encoded string) (PasswordHash, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" ||
		parts[2] != "v=19" || parts[3] != "m=19456,t=2,p=1" {
		return PasswordHash{}, errors.New("unsupported password hash format")
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) != 16 {
		return PasswordHash{}, errors.New("invalid password salt")
	}
	digest, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(digest) != 32 {
		return PasswordHash{}, errors.New("invalid password digest")
	}
	var hash PasswordHash
	copy(hash.salt[:], salt)
	copy(hash.digest[:], digest)
	hash.valid = true
	return hash, nil
}

func HashPassword(password string) (PasswordHash, error) {
	if password == "" {
		return PasswordHash{}, errors.New("password must not be empty")
	}
	var hash PasswordHash
	if _, err := rand.Read(hash.salt[:]); err != nil {
		return PasswordHash{}, errors.New("cannot generate password salt")
	}
	copy(hash.digest[:], argon2.IDKey([]byte(password), hash.salt[:], 2, 19*1024, 1, 32))
	hash.valid = true
	return hash, nil
}

func (hash PasswordHash) Matches(password string) bool {
	if !hash.valid {
		return false
	}
	digest := argon2.IDKey([]byte(password), hash.salt[:], 2, 19*1024, 1, 32)
	return subtle.ConstantTimeCompare(hash.digest[:], digest) == 1
}
