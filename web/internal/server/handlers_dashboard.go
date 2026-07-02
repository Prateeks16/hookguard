package server

import "net/http"

// handleDashboardStub is a placeholder for GET /dashboard: enough to prove
// the auth-gate redirect (missing/expired session -> 303 /login?next=...)
// works end-to-end. The real dashboard shell is M2 scope.
func (s *Server) handleDashboardStub(w http.ResponseWriter, r *http.Request) {
	if userFromContext(r) == nil {
		http.Redirect(w, r, "/login?next="+r.URL.Path, http.StatusSeeOther)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("dashboard placeholder"))
}
