package server

import "net/http"

// handleOverview is the real GET /dashboard route (M2): stat cards are
// placeholder zeros since the events table has no writer until M4 — DESIGN.md
// §6.2, §10 M2.
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r)
	sess := sessionFromContext(r)
	s.render(w, "overview.html", pageData{User: u, CSRFToken: sess.CSRFToken, Version: s.Version, Active: "overview"})
}

// handleDashboardPlaceholder serves the minimal "coming in a later milestone"
// page for Endpoints/Live Logs/Providers — nav must not 404, but M3/M4 CRUD
// and streaming logic don't belong here yet.
func (s *Server) handleDashboardPlaceholder(active, title string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userFromContext(r)
		sess := sessionFromContext(r)
		s.render(w, "placeholder.html", pageData{User: u, CSRFToken: sess.CSRFToken, Version: s.Version, Active: active, Flash: title})
	}
}
