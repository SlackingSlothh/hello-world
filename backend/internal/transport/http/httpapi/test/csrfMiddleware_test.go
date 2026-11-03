package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireSameOrigin(t *testing.T) {
	handler := newAuthenticationHandler(t, nil, sessionStub{})
	cases := []struct {
		name, method, origin, site string
		status                     int
	}{
		{"sameOriginPost", "POST", "https://app.example", "", 204},
		{"sameOriginPut", "PUT", "https://app.example", "", 204},
		{"sameOriginPatch", "PATCH", "https://app.example", "", 204},
		{"sameOriginDelete", "DELETE", "https://app.example", "", 204},
		{"foreignOrigin", "POST", "https://evil.example", "", 403},
		{"missingOrigin", "POST", "", "", 403},
		{"sameOriginMetadata", "POST", "", "same-origin", 204},
		{"crossSiteMetadata", "POST", "", "cross-site", 403},
		{"getWithoutOrigin", "GET", "", "", 204},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			})
			request := httptest.NewRequest(testCase.method, "https://app.example/resource", nil)
			if testCase.origin != "" {
				request.Header.Set("Origin", testCase.origin)
			}
			if testCase.site != "" {
				request.Header.Set("Sec-Fetch-Site", testCase.site)
			}
			response := httptest.NewRecorder()
			handler.RequireSameOrigin(next).ServeHTTP(response, request)
			assertStatus(t, response, testCase.status)
			if called != (testCase.status == http.StatusNoContent) {
				t.Fatal("incorrect handler invocation")
			}
		})
	}
}
