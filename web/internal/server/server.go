// Package server wires the Console's HTTP surface: routing, session/CSRF/
// security-header middleware, and the public + auth handlers (DESIGN.md
// §7.4, §9).
package server

import (
	"embed"
	"html/template"
	"net/http"
	"time"

	"hookguard/web/internal/auth"
	"hookguard/web/internal/store"
)

// Server holds everything a handler needs: the store, parsed templates, and
// the auth primitives (rate limiters are per-server so they survive across
// requests).
type Server struct {
	Store       *store.Store
	Templates   *template.Template
	AllowSignup bool
	Version     string

	LoginLimiter  *auth.Limiter
	SignupLimiter *auth.Limiter

	Now func() time.Time
}

// New builds a Server. templatesFS is passed in from main via go:embed
// (web/ui) so the binary is self-contained.
func New(st *store.Store, templatesFS embed.FS, allowSignup bool, version string) (*Server, error) {
	tmpl, err := template.ParseFS(templatesFS, "templates/layouts/*.html", "templates/pages/*.html", "templates/partials/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{
		Store:         st,
		Templates:     tmpl,
		AllowSignup:   allowSignup,
		Version:       version,
		LoginLimiter:  auth.NewLimiter(10, 15*time.Minute),
		SignupLimiter: auth.NewLimiter(5, time.Hour),
		Now:           time.Now,
	}, nil
}

// Router builds the full handler chain. staticFS must be rooted so that
// "static/css/tokens.css" etc. resolve directly (web/ui.StaticFS).
func (s *Server) Router(staticFS embed.FS) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /{$}", s.handleLanding)
	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("GET /signup", s.handleSignupForm)
	mux.HandleFunc("POST /signup", s.handleSignup)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("GET /reset-password", s.handleResetPasswordForm)
	mux.HandleFunc("POST /reset-password", s.handleResetPassword)

	mux.HandleFunc("GET /dashboard", s.requireAuth(s.handleOverview))
	mux.HandleFunc("GET /dashboard/endpoints", s.requireAuth(s.handleDashboardPlaceholder("endpoints", "Endpoints")))
	mux.HandleFunc("GET /dashboard/logs", s.requireAuth(s.handleDashboardPlaceholder("logs", "Live Logs")))
	mux.HandleFunc("GET /dashboard/providers", s.requireAuth(s.handleDashboardPlaceholder("providers", "Providers")))
	mux.HandleFunc("GET /dashboard/settings", s.requireAuth(s.handleSettings))
	mux.HandleFunc("POST /dashboard/settings/password", s.requireAuth(s.handlePasswordChange))
	mux.HandleFunc("POST /dashboard/settings/sessions/{id}/revoke", s.requireAuth(s.handleSessionRevoke))
	mux.HandleFunc("POST /dashboard/settings/sessions/revoke-others", s.requireAuth(s.handleSessionRevokeAllOthers))
	mux.HandleFunc("GET /dashboard/settings/users", s.requireAdmin(s.handleSettings))
	mux.HandleFunc("POST /dashboard/settings/users", s.requireAdmin(s.handleUserCreate))
	mux.HandleFunc("POST /dashboard/settings/users/{id}/deactivate", s.requireAdmin(s.handleUserDeactivate))

	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS)))

	mux.HandleFunc("/", s.handleNotFound)

	return withSecurityHeaders(withSession(s, mux))
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	s.render(w, "404.html", pageData{User: userFromContext(r)})
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok","version":"` + s.Version + `"}`))
}
