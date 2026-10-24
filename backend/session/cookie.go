package session

import (
	"net/http"
	"time"
)

const CookieName = "session_id"

type Cookies struct {
	AllowInsecureLocalHTTP bool
}

func (Cookies) Read(request *http.Request) (Identifier, error) {
	var value string
	count := 0
	for _, cookie := range request.Cookies() {
		if cookie.Name == CookieName {
			count++
			value = cookie.Value
		}
	}
	if count != 1 {
		return Identifier{}, ErrInvalid
	}
	return parseIdentifier(value)
}

func (cookies Cookies) Write(writer http.ResponseWriter, issued Issued) error {
	if _, err := issued.identifier.digest(); err != nil || issued.expiresAt.IsZero() {
		return ErrInvalid
	}
	writer.Header().Set("Cache-Control", "no-store")
	http.SetCookie(writer, &http.Cookie{
		Name: CookieName, Value: issued.identifier.value, Path: "/",
		Expires: issued.expiresAt.UTC(), HttpOnly: true,
		Secure: !cookies.AllowInsecureLocalHTTP, SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (cookies Cookies) Clear(writer http.ResponseWriter) {
	writer.Header().Set("Cache-Control", "no-store")
	http.SetCookie(writer, &http.Cookie{
		Name: CookieName, Value: "", Path: "/",
		Expires: time.Unix(1, 0).UTC(), MaxAge: -1, HttpOnly: true,
		Secure: !cookies.AllowInsecureLocalHTTP, SameSite: http.SameSiteLaxMode,
	})
}
