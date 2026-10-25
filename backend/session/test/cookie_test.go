package session_test

import (
	"context"
	"errors"
	"hello-world/session"
	"hello-world/session/test/support"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSessionCookie(t *testing.T) {
	service, _, clock := support.NewService(t)
	issued, err := service.Create(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name     string
		insecure bool
	}{
		{"httpsCookie", false}, {"localHttpCookie", true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cookies := session.Cookies{AllowInsecureLocalHTTP: testCase.insecure}
			response := httptest.NewRecorder()
			if err := cookies.Write(response, issued); err != nil {
				t.Fatal(err)
			}
			written := response.Result().Cookies()
			if len(written) != 1 {
				t.Fatal("expected one session cookie")
			}
			cookie := written[0]
			if cookie.Name != session.CookieName || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
				t.Fatal("incorrect session cookie attributes")
			}
			if cookie.Secure != !testCase.insecure {
				t.Fatal("incorrect Secure attribute")
			}
			if !cookie.Expires.Equal(clock.Now().Add(8 * time.Hour)) {
				t.Fatal("incorrect absolute expiry")
			}
			if response.Body.Len() != 0 {
				t.Fatal("session should only be sent through cookie")
			}
			request := httptest.NewRequest("GET", "/", nil)
			request.AddCookie(cookie)
			identifier, err := cookies.Read(request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Validate(context.Background(), identifier); err != nil {
				t.Fatal(err)
			}
			response = httptest.NewRecorder()
			cookies.Clear(response)
			cleared := response.Result().Cookies()
			if len(cleared) != 1 || cleared[0].Value != "" || cleared[0].MaxAge != -1 || cleared[0].Path != cookie.Path {
				t.Fatal("session cookie not cleared")
			}
		})
	}
}

func TestSessionCookieRejectsMissingOrInvalidValue(t *testing.T) {
	for _, value := range []string{"", "invalid"} {
		request := httptest.NewRequest("GET", "/", nil)
		if value != "" {
			request.AddCookie(&http.Cookie{Name: session.CookieName, Value: value})
		}
		if _, err := (session.Cookies{}).Read(request); !errors.Is(err, session.ErrInvalid) {
			t.Fatal("invalid cookie accepted")
		}
	}
}
