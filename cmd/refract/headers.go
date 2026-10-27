package main

import (
	"net/http"

	"github.com/ponytojas/Refract/internal/users"
)

// csp allows only the app's own scripts and connections. Inline styles stay
// allowed: CodeMirror and the UI library inject <style> elements. Images and
// fonts may be data: or blob: URLs (the PDF viewer and editor make them).
const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; worker-src 'self' blob:; " +
	"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// securityHeaders hardens every response. Handlers serving project files
// replace the CSP with a stricter "sandbox".
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY") // for browsers without frame-ancestors
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		if users.Secure(r) {
			h.Set("Strict-Transport-Security", "max-age=15552000") // 180 days, this host only
		}
		next.ServeHTTP(w, r)
	})
}
