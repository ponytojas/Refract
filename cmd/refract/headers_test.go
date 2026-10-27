package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeaders(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/raw" {
			w.Header().Set("Content-Security-Policy", "sandbox") // like project files
		}
	}))
	get := func(path string, https bool) http.Header {
		r := httptest.NewRequest("GET", path, nil)
		if https {
			r.Header.Set("X-Forwarded-Proto", "https")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Header()
	}
	page := get("/", false)
	if page.Get("Content-Security-Policy") != csp || page.Get("X-Frame-Options") != "DENY" || page.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers %v", page)
	}
	if page.Get("Strict-Transport-Security") != "" {
		t.Fatal("HSTS over plain http")
	}
	if get("/", true).Get("Strict-Transport-Security") == "" {
		t.Fatal("no HSTS over https")
	}
	if got := get("/raw", false).Get("Content-Security-Policy"); got != "sandbox" {
		t.Fatalf("a handler's stricter CSP was overridden: %q", got)
	}
}
