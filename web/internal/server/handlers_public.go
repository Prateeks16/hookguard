package server

import (
	"net/http"

	"hookguard/web/internal/store"
)

type pageData struct {
	Next       string
	Error      string
	Flash      string
	CSRFToken  string
	Version    string
	ResetToken string
	ResetUID   string
	User       *store.User
	Active     string // sidebar nav highlight; unused outside /dashboard/*

	// Status strip fields (DESIGN.md §6.1), embedded here rather than in a
	// separate struct so every page — not just Overview — can render
	// dashboard-topbar/dashboard-statusbar without each handler needing its
	// own copy of the liveness plumbing.
	Connected    bool
	LastIngestAt int64  // unix ms, 0 if never
	LastEventAgo string // human string for the template, "" if never
}

func (s *Server) handleLanding(w http.ResponseWriter, r *http.Request) {
	s.render(w, "landing.html", pageData{Version: s.Version})
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, "login.html", pageData{Next: safeNext(r.URL.Query().Get("next"))})
}

func (s *Server) handleSignupForm(w http.ResponseWriter, r *http.Request) {
	if !s.AllowSignup {
		w.WriteHeader(http.StatusForbidden)
		s.render(w, "403.html", pageData{Error: "Ask your admin for an invite."})
		return
	}
	s.render(w, "signup.html", pageData{})
}

// render accepts pageData or any struct embedding it (e.g. settingsData) —
// templates only ever need field access, not a shared static type.
func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.Templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// safeNext only allows a local, absolute path — never an external host —
// so ?next= can't be used as an open redirect.
func safeNext(next string) string {
	if len(next) == 0 || next[0] != '/' || (len(next) > 1 && next[1] == '/') {
		return "/dashboard"
	}
	return next
}
