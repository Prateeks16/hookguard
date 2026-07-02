package server

import (
	"context"
	"net/http"
	"time"

	"hookguard/web/internal/auth"
	"hookguard/web/internal/store"
)

type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxSession
)

// withSecurityHeaders sets the fixed response headers DESIGN.md §5.4
// requires on every response — CSP with no exceptions, since htmx is
// configured via attributes and the theme toggle is an external file.
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// withSession loads the session named by the hg_session cookie (if any) and
// injects the user + session into the request context. It never blocks a
// request — route-level checks decide what requires auth.
func withSession(s *Server, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(auth.SessionCookieName)
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}

		sess, err := s.Store.GetSessionByTokenHash(auth.HashToken(c.Value))
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		now := s.Now()
		if now.After(time.UnixMilli(sess.ExpiresAt)) || now.After(time.UnixMilli(sess.LastSeenAt).Add(auth.SessionIdleTimeout)) {
			s.Store.DeleteSession(sess.ID)
			next.ServeHTTP(w, r)
			return
		}

		// Sliding idle window, throttled to once/minute so we don't write on
		// every request (DESIGN.md §5.3).
		if now.Sub(time.UnixMilli(sess.LastSeenAt)) > time.Minute {
			s.Store.TouchSession(sess.ID, now.UnixMilli())
		}

		user, err := s.Store.GetUserByID(sess.UserID)
		if err != nil || !user.Active {
			next.ServeHTTP(w, r)
			return
		}

		ctx := context.WithValue(r.Context(), ctxSession, sess)
		ctx = context.WithValue(ctx, ctxUser, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func userFromContext(r *http.Request) *store.User {
	u, _ := r.Context().Value(ctxUser).(*store.User)
	return u
}

func sessionFromContext(r *http.Request) *store.Session {
	s, _ := r.Context().Value(ctxSession).(*store.Session)
	return s
}

// requireCSRF verifies the session's synchronizer token against the request
// on every non-GET route (DESIGN.md §5.1). SameSite=Lax is the backstop, not
// the mechanism, so this check is mandatory even though the cookie helps too.
func requireCSRF(w http.ResponseWriter, r *http.Request, sess *store.Session) bool {
	got := r.Header.Get(auth.CSRFHeader)
	if got == "" {
		got = r.FormValue(auth.CSRFFormField)
	}
	if sess == nil || !auth.CheckCSRF(sess.CSRFToken, got) {
		http.Error(w, "invalid or missing CSRF token", http.StatusForbidden)
		return false
	}
	return true
}

// requireAuth wraps a /dashboard/* handler so every such route redirects an
// unauthenticated request to /login?next=... uniformly (DESIGN.md §5.3, M2
// verify criteria) instead of each handler re-implementing the check.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if userFromContext(r) == nil {
			http.Redirect(w, r, "/login?next="+r.URL.Path, http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// requireAdmin additionally rejects non-admin users with 403 — server-side
// authorization for the admin-only Users section, independent of whether the
// UI hides the link (DESIGN.md M2 scope).
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if userFromContext(r).Role != "admin" {
			w.WriteHeader(http.StatusForbidden)
			s.render(w, "403.html", pageData{User: userFromContext(r), Error: "Admins only."})
			return
		}
		next(w, r)
	})
}
