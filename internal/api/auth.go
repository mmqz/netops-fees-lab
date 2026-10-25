package api

// auth.go — the two-tier write gate.
//
// History: v0.11-R13 introduced the opt-in single-token write gate
// (--auth-token). Issue #9 (v0.18) added the second tier and the
// default-safe bind check, keeping every existing behavior intact:
//
//   - reads stay open: GET/HEAD on /api never require a token. The
//     product's read side is the demo/analysis surface (and the MCP
//     read-only red line has the same shape); the write side is where
//     audit identity belongs.
//   - /api/health and /api/openapi.json are always open (both GET-only):
//     discovery must work before authentication can be configured.
//   - the SPA (/, assets) is never gated — the browser shell always loads.
//
// THE TWO TIERS (issue #9 方案 A):
//
//	--auth-token  T  申报级 (declarer): import/analyze/operate writes.
//	--admin-token A  管理级 (admin):    everything, incl. the Admin routes.
//
// The privilege matrix — evaluated per request, each row inert until its
// configuration row exists:
//
//	gate state                        reads   writes          Admin routes
//	─────────────────────────────────────────────────────────────────────
//	both tokens empty (default)       open    open (loopback    open
//	                                        bind enforced at CLI)
//	only --auth-token                 open    auth OR admin     auth token
//	                                       token accepted       holds admin
//	                                                            (single-operator
//	                                                            compat: v0.11
//	                                                            behavior)
//	only --admin-token                open    admin token only  admin token
//	both configured                   open    either token      ADMIN ONLY —
//	                                                            declarer gets
//	                                                            403 forbidden
//
// The single-operator compat row is deliberate: activating the two-tier
// model requires BOTH tokens, so an existing one-token deployment keeps
// working exactly as before (the one write token is its own admin — you
// cannot lock the operator out of destructive routes by forgetting the
// second flag). A deployment that wants declarers to NOT delete must set
// both tokens; the CLI rejects --admin-token == --auth-token so the
// "declarer" tier can never silently hold admin rights.
//
// Which routes are Admin: the `Admin` flag of the single route table
// (routes.go) — the destructive deletes/rollbacks and the human-channel
// audit confirmations (裁定/关联确认/驳回 = the 管理岗's job in the
// branch-office model of issue #9). The enforcement wrapper (requireAdmin)
// and the OpenAPI 403 documentation both derive from that one flag.
//
// AUDIT (issue #9 方案 C): every Admin-route success (2xx) emits one slog
// Info "audit" line — method, path, source address, response status — so a
// destructive operation is always traceable in the server log even though
// tokens are shared per tier, not per person.
//
// Comparisons are constant-time (crypto/subtle): both tokens are static
// shared secrets, the brute-forceable strings of the surface. 401 uses
// code "unauthorized" (missing/unknown token); 403 uses "forbidden"
// (valid token, insufficient tier) — the standard envelope both.

import (
	"crypto/subtle"
	"net"
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

// gateOn reports whether ANY token tier is configured (the gate exists at
// all). With no tokens the API is wide open — the loopback-only default
// enforced at the CLI boundary (issue #9: a non-loopback bind without a
// token refuses to start).
func (s *Server) gateOn() bool { return s.AuthToken != "" || s.AdminToken != "" }

// tokenMatches reports whether tok equals secret in constant time; an
// empty secret never matches (an unset tier cannot be unlocked by an
// empty header value).
func tokenMatches(tok, secret string) bool {
	if secret == "" || tok == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(tok), []byte(secret)) == 1
}

// tokenIsDeclarer reports whether tok opens the declarer tier.
func (s *Server) tokenIsDeclarer(tok string) bool { return tokenMatches(tok, s.AuthToken) }

// tokenIsAdmin reports whether tok opens the admin tier: the admin token,
// OR — single-operator compat — the auth token when no admin token is
// configured (the v0.11 single-token behavior is preserved: that one
// token could always do everything).
func (s *Server) tokenIsAdmin(tok string) bool {
	if s.AdminToken != "" {
		return tokenMatches(tok, s.AdminToken)
	}
	return tokenMatches(tok, s.AuthToken)
}

// authMiddleware enforces the FIRST tier of the write gate: every mutating
// /api request must carry a token valid for EITHER tier (401 unauthorized
// otherwise). The per-route admin enforcement (403) lives in requireAdmin,
// applied at registration time to the Admin-flagged routes. The middleware
// runs INSIDE the middleware chain (after panic recovery wraps it — a 401
// must not skip logging).
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.gateOn() {
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
		if !s.tokenIsDeclarer(tok) && !s.tokenIsAdmin(tok) {
			respondError(w, http.StatusUnauthorized, "unauthorized",
				"写操作需要访问令牌（Authorization: Bearer <token>）；只读请求不受影响")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireAdmin wraps an Admin-route handler with the second tier of the
// gate (issue #9): the caller's token must open the ADMIN tier —
// otherwise 403 forbidden (a valid declarer token that passed the 401
// layer but may not delete/rollback/confirm). With the gate off the
// wrapper is a pass-through. On success (2xx) it emits the privileged
// operation audit line (方案 C): the shared-token world cannot attribute
// per-person, so the trace records WHAT happened from WHERE.
func (s *Server) requireAdmin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.gateOn() {
			h(w, r)
			return
		}
		tok := bearerToken(r)
		if !s.tokenIsAdmin(tok) {
			respondError(w, http.StatusForbidden, "forbidden",
				"该操作属于管理级（删除/回滚/审核），需要管理令牌（--admin-token）；申报令牌仅有导入与查询类写权限")
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h(rec, r)
		if rec.status >= 200 && rec.status < 300 {
			s.log.Info("audit: admin op",
				"method", r.Method,
				"path", r.URL.Path,
				"remote", remoteAddrHost(r),
				"status", rec.status,
			)
		}
	}
}

// remoteAddrHost strips the port of r.RemoteAddr ("host:port" → "host");
// the raw form when the split fails (non-network test transports).
func remoteAddrHost(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
