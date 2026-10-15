package api

// auth.go — the opt-in write gate (v0.11-R13): serve --auth-token T turns
// every MUTATING /api request (POST / PATCH / DELETE / PUT) into a
// Bearer-token check. The design keeps three deliberate asymmetries:
//
//   - reads stay open: GET/HEAD on /api never require the token. The
//     product's read side is the demo/大屏/AI-analysis surface (and the
//     MCP read-only red line has the same shape); the write side is where
//     audit identity belongs. A full-fence mode can come later — the
//     middleware is one boolean away;
//   - /api/health and /api/openapi.json are always open (both GET-only
//     anyway): discovery must work before authentication can be
//     configured, and the OpenAPI document is what tells an integrator
//     the 401 contract exists;
//   - the SPA (/, assets) is never gated — the browser shell always
//     loads, then decides whether to ask for a token.
//
// The comparison is constant-time (crypto/subtle): the token is a static
// shared secret, the one brute-forceable string in the surface. The 401
// envelope uses the standard error shape with code "unauthorized", so the
// frontend's ApiError path and the /dev reference page describe it like
// any other contract. An empty AuthToken field is the OFF channel (the
// default: v0.10 behavior, zero-diff for existing users).

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// bearerToken extracts the token of an Authorization: Bearer <token>
// header; "" when the header is absent or not a Bearer scheme.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	const scheme = "Bearer "
	if len(h) <= len(scheme) || !strings.EqualFold(h[:len(scheme)], scheme) {
		return ""
	}
	return strings.TrimSpace(h[len(scheme):])
}

// isPublicAPIPath reports whether the path is exempt from the gate.
func isPublicAPIPath(path string) bool {
	return path == "/api/health" || path == "/api/openapi.json"
}

// authMiddleware enforces the write gate. It runs INSIDE the middleware
// chain (after panic recovery wraps it — a 401 must not skip logging).
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.AuthToken == "" {
			next.ServeHTTP(w, r)
			return
		}
		// Off the gate: reads, discovery, and everything non-/api (the SPA).
		isRead := r.Method == http.MethodGet || r.Method == http.MethodHead
		isAPI := strings.HasPrefix(r.URL.Path, "/api/")
		if !isAPI || isRead || isPublicAPIPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		tok := bearerToken(r)
		if tok == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(s.AuthToken)) != 1 {
			respondError(w, http.StatusUnauthorized, "unauthorized",
				"写操作需要访问令牌（Authorization: Bearer <token>）；只读请求不受影响")
			return
		}
		next.ServeHTTP(w, r)
	})
}
