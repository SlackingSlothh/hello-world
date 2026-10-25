package session_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"hello-world/session"
	"hello-world/session/test/support"
)

func TestSessionIdleAndAbsoluteExpiration(t *testing.T) {
	t.Run("idle boundary", func(t *testing.T) {
		service, _, clock := support.NewService(t)
		identifier, _ := support.Create(t, service, "u1")
		clock.Advance(30 * time.Minute)
		if _, err := service.Validate(context.Background(), identifier); !errors.Is(err, session.ErrInvalid) {
			t.Fatal("idle session accepted at deadline")
		}
	})
	t.Run("activity resets idle but not absolute", func(t *testing.T) {
		service, store, clock := support.NewService(t)
		identifier, _ := support.Create(t, service, "u1")
		originalExpiry := store.Records()[0].ExpiresAt
		for elapsed := 10 * time.Minute; elapsed < 8*time.Hour; elapsed += 10 * time.Minute {
			clock.Advance(10 * time.Minute)
			result, err := service.Validate(context.Background(), identifier)
			if err != nil {
				t.Fatalf("active session expired at %v: %v", elapsed, err)
			}
			if result.Replacement != nil {
				identifier = support.Identifier(t, support.Cookie(t, *result.Replacement))
			}
			if !store.Records()[0].ExpiresAt.Equal(originalExpiry) {
				t.Fatal("activity or rotation extended absolute lifetime")
			}
		}
		clock.Advance(10 * time.Minute)
		if _, err := service.Validate(context.Background(), identifier); !errors.Is(err, session.ErrInvalid) {
			t.Fatal("absolute deadline ignored")
		}
	})
}

func TestRotationGraceAndLostResponse(t *testing.T) {
	service, store, clock := support.NewService(t)
	original, originalCookie := support.Create(t, service, "u1")
	before := store.Records()[0]
	clock.Advance(14 * time.Minute)
	result, err := service.Validate(context.Background(), original)
	if err != nil || result.Replacement != nil {
		t.Fatal("session rotated before interval")
	}
	clock.Advance(time.Minute)
	result, err = service.Validate(context.Background(), original)
	if err != nil || result.Replacement == nil {
		t.Fatal("rotation missing at interval")
	}
	replacement := support.Cookie(t, *result.Replacement)
	if replacement.Value == originalCookie.Value || replacement.Name != originalCookie.Name || !replacement.Expires.Equal(originalCookie.Expires) {
		t.Fatal("incorrect rotated cookie")
	}
	current := support.Identifier(t, replacement)
	after := store.Records()[0]
	if after.ID != before.ID || !after.CreatedAt.Equal(before.CreatedAt) || !after.ExpiresAt.Equal(before.ExpiresAt) {
		t.Fatal("rotation changed stable session metadata")
	}
	clock.Advance(29 * time.Second)
	retry, err := service.Validate(context.Background(), original)
	if err != nil || retry.Replacement == nil {
		t.Fatal("lost response could not be recovered")
	}
	if support.Cookie(t, *retry.Replacement).Value != replacement.Value {
		t.Fatal("old alias caused another rotation")
	}
	clock.Advance(time.Second)
	if _, err := service.Validate(context.Background(), original); !errors.Is(err, session.ErrInvalid) {
		t.Fatal("grace period extended on retry")
	}
	if _, err := service.Validate(context.Background(), current); err != nil {
		t.Fatal(err)
	}
}

func TestLoginRotationAccountSwitchAndPrivilegeChange(t *testing.T) {
	service, store, clock := support.NewService(t)
	original, originalCookie := support.Create(t, service, "u1")
	clock.Advance(time.Minute)
	issued, err := service.StartAfterLogin(context.Background(), "u1", original)
	if err != nil {
		t.Fatal(err)
	}
	cookie := support.Cookie(t, issued)
	if cookie.Value == originalCookie.Value || len(store.Records()) != 1 {
		t.Fatal("same-user login did not rotate existing session")
	}
	current := support.Identifier(t, cookie)
	if err := service.RequireRotation(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}
	result, err := service.Validate(context.Background(), current)
	if err != nil || result.Replacement == nil {
		t.Fatal("privilege increase did not rotate")
	}
	current = support.Identifier(t, support.Cookie(t, *result.Replacement))
	if store.Records()[0].RotationRequired {
		t.Fatal("rotation flag not cleared")
	}
	switched, err := service.StartAfterLogin(context.Background(), "u2", current)
	if err != nil {
		t.Fatal(err)
	}
	for _, identifier := range []session.Identifier{original, support.Identifier(t, cookie), current} {
		if _, err := service.Validate(context.Background(), identifier); !errors.Is(err, session.ErrInvalid) {
			t.Fatal("old identity survived account switch")
		}
	}
	result, err = service.Validate(context.Background(), support.Identifier(t, support.Cookie(t, switched)))
	if err != nil || result.UserID != "u2" {
		t.Fatal("new account session invalid")
	}
}

func TestLogoutAndUserRevocation(t *testing.T) {
	service, _, _ := support.NewService(t)
	old, _ := support.Create(t, service, "u1")
	result, err := service.Rotate(context.Background(), old)
	if err != nil || result.Replacement == nil {
		t.Fatal("rotation failed")
	}
	current := support.Identifier(t, support.Cookie(t, *result.Replacement))
	if err := service.Logout(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Validate(context.Background(), current); !errors.Is(err, session.ErrInvalid) {
		t.Fatal("logout via alias did not revoke active identifier")
	}
	if err := service.Logout(context.Background(), old); err != nil {
		t.Fatal("logout not idempotent")
	}
	if err := service.Logout(context.Background(), session.Identifier{}); err != nil {
		t.Fatal(err)
	}
	first, _ := support.Create(t, service, "u1")
	second, _ := support.Create(t, service, "u1")
	other, _ := support.Create(t, service, "u2")
	if err := service.RevokeUser(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}
	for _, identifier := range []session.Identifier{first, second} {
		if _, err := service.Validate(context.Background(), identifier); !errors.Is(err, session.ErrInvalid) {
			t.Fatal("user session not revoked")
		}
	}
	if _, err := service.Validate(context.Background(), other); err != nil {
		t.Fatal("another user was revoked")
	}
}

func TestConcurrentRequestsReceiveSameSession(t *testing.T) {
	service, _, clock := support.NewService(t)
	identifier, _ := support.Create(t, service, "u1")
	clock.Advance(15 * time.Minute)
	var results [2]session.Validated
	var failures [2]error
	var requests sync.WaitGroup
	start := make(chan struct{})
	for index := range results {
		requests.Add(1)
		go func() {
			defer requests.Done()
			<-start
			results[index], failures[index] = service.Validate(context.Background(), identifier)
		}()
	}
	close(start)
	requests.Wait()
	for index := range results {
		if failures[index] != nil || results[index].Replacement == nil {
			t.Fatalf("request %d did not receive a session replacement: %v", index, failures[index])
		}
	}
	first := support.Cookie(t, *results[0].Replacement)
	second := support.Cookie(t, *results[1].Replacement)
	if first.Value != second.Value {
		t.Fatal("concurrent requests received different sessions")
	}
}

func TestFailedRotationDoesNotChangeSession(t *testing.T) {
	service, store, _ := support.NewService(t)
	identifier, _ := support.Create(t, service, "u1")
	before := store.Records()[0].TokenDigest
	store.CommitError = errors.New("storage unavailable")
	result, err := service.Rotate(context.Background(), identifier)
	if !errors.Is(err, session.ErrUnavailable) || result.Replacement != nil || result.UserID != "" {
		t.Fatal("failed rotation returned a usable session")
	}
	if store.Records()[0].TokenDigest != before {
		t.Fatal("failed rotation changed the stored session")
	}
}
