package server

import (
	"net/http"
)

type pageData struct {
	Next       string
	Error      string
	CSRFToken  string
	Version    string
	ResetToken string
	ResetUID   string
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

func (s *Server) render(w http.ResponseWriter, name string, data pageData) {
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
