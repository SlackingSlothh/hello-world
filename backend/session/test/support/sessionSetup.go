package support

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"hello-world/session"
)

func NewService(t testing.TB) (*session.Service, *Store, *Clock) {
	t.Helper()
	store := NewStore()
	clock := NewClock()
	cipher, err := session.NewIdentifierCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	service, err := session.NewService(store, session.DefaultConfig(), cipher, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	return service, store, clock
}

func Cookie(t testing.TB, issued session.Issued) *http.Cookie {
	t.Helper()
	writer := httptest.NewRecorder()
	if err := (session.Cookies{}).Write(writer, issued); err != nil {
		t.Fatal(err)
	}
	cookies := writer.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one session cookie, got %d", len(cookies))
	}
	return cookies[0]
}

func Identifier(t testing.TB, cookie *http.Cookie) session.Identifier {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(cookie)
	identifier, err := (session.Cookies{}).Read(request)
	if err != nil {
		t.Fatal(err)
	}
	return identifier
}

func Create(t testing.TB, service *session.Service, userID string) (session.Identifier, *http.Cookie) {
	t.Helper()
	issued, err := service.Create(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	cookie := Cookie(t, issued)
	return Identifier(t, cookie), cookie
}
