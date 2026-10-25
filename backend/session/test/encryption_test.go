package session_test

import (
	"bytes"
	"crypto/sha256"
	"hello-world/session"
	"hello-world/session/test/support"
	"testing"
)

func TestSessionIdentifierEncryption(t *testing.T) {
	service, _, _ := support.NewService(t)
	identifier, cookie := support.Create(t, service, "u1")
	digest := session.Digest(sha256.Sum256([]byte(cookie.Value)))
	cipher, err := session.NewIdentifierCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt("session1", identifier)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := cipher.Decrypt("session1", digest, encrypted)
	if err != nil || decoded != identifier {
		t.Fatal("cannot decrypt session identifier")
	}
	wrongKey, err := session.NewIdentifierCipher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrongKey.Decrypt("session1", digest, encrypted); err == nil {
		t.Fatal("wrong key accepted")
	}
	if _, err := cipher.Decrypt("otherSession", digest, encrypted); err == nil {
		t.Fatal("ciphertext accepted for another session")
	}
	encrypted[len(encrypted)-1] ^= 1
	if _, err := cipher.Decrypt("session1", digest, encrypted); err == nil {
		t.Fatal("modified ciphertext accepted")
	}
}
