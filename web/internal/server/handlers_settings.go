package server

import (
	"net/http"
	"strconv"

	"hookguard/web/internal/auth"
	"hookguard/web/internal/store"
)

type settingsData struct {
	pageData
	Sessions       []store.Session
	CurrentSessID  int64
	Users          []store.User
	AuthEvents     []store.AuthEvent
	PasswordError  string
	RetentionDays  int
	RetentionError string
	DataDir        string
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r)
	sess := sessionFromContext(r)

	sessions, err := s.Store.ListSessionsForUser(u.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var users []store.User
	if u.Role == "admin" {
		users, err = s.Store.ListUsers()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	events, err := s.Store.ListAuthEvents(50)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	retentionDays, err := s.Store.GetRetentionDays()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	connected, lastIngestAt, lastEventAgo := s.dashboardStatus()
	s.render(w, "settings.html", settingsData{
		pageData: pageData{
			User: u, CSRFToken: sess.CSRFToken, Version: s.Version, Active: "settings",
			Connected: connected, LastIngestAt: lastIngestAt, LastEventAgo: lastEventAgo,
		},
		Sessions:      sessions,
		CurrentSessID: sess.ID,
		Users:         users,
		AuthEvents:    events,
		RetentionDays: retentionDays,
		DataDir:       s.DataDir,
	})
}

// handleRetentionChange updates the Instance retention window (DESIGN.md
// §6.2 Settings → Instance). The nightly job reads retention_days fresh on
// each tick, so no restart is needed for this to take effect.
func (s *Server) handleRetentionChange(w http.ResponseWriter, r *http.Request) {
	sess := sessionFromContext(r)
	if !requireCSRF(w, r, sess) {
		return
	}
	u := userFromContext(r)

	days, err := strconv.Atoi(r.FormValue("retention_days"))
	if err != nil || days < 1 {
		s.renderRetentionError(w, r, u, sess, "Retention days must be a positive number.")
		return
	}

	if err := s.Store.SetRetentionDays(days); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard/settings", http.StatusSeeOther)
}

// handlePasswordChange requires the current password and, on success,
// rehashes with Argon2id and rotates the session — exactly the login
// rotation behavior, reused via startSession — DESIGN.md §10 M2 verify
// criteria: "password change rotates session."
func (s *Server) handlePasswordChange(w http.ResponseWriter, r *http.Request) {
	sess := sessionFromContext(r)
	if !requireCSRF(w, r, sess) {
		return
	}
	u := userFromContext(r)

	current := r.FormValue("current_password")
	next := r.FormValue("new_password")
	confirm := r.FormValue("new_password_confirm")

	if !auth.VerifyPassword(current, u.PasswordHash) {
		s.renderSettingsError(w, r, u, sess, "Current password is incorrect.")
		return
	}
	if next != confirm {
		s.renderSettingsError(w, r, u, sess, "New passwords do not match.")
		return
	}
	if err := auth.ValidatePassword(next); err != nil {
		s.renderSettingsError(w, r, u, sess, err.Error())
		return
	}

	hash, err := auth.HashPassword(next)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := s.Store.UpdatePasswordHash(u.ID, hash); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Old token stays valid until this DeleteSession; startSession below
	// mints a brand-new one and cookie, so the pre-change session is dead.
	s.Store.DeleteSession(sess.ID)
	s.Store.InsertAuthEvent(store.AuthEvent{At: s.Now().UnixMilli(), UserID: &u.ID, Email: u.Email, Kind: store.AuthEventPasswordChange, IP: clientIP(r)})

	s.startSession(w, r, u.ID)
	http.Redirect(w, r, "/dashboard/settings", http.StatusSeeOther)
}

// handleSessionRevoke deletes one of the current user's own sessions. If it's
// the current session the cookie is cleared too (logs the user out); if it's
// another session, that session's next authenticated request redirects to
// login (DESIGN.md §10 M2 verify criteria).
func (s *Server) handleSessionRevoke(w http.ResponseWriter, r *http.Request) {
	sess := sessionFromContext(r)
	if !requireCSRF(w, r, sess) {
		return
	}
	u := userFromContext(r)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}

	target, err := findOwnedSession(s, u.ID, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	s.Store.DeleteSession(target.ID)
	s.Store.InsertAuthEvent(store.AuthEvent{At: s.Now().UnixMilli(), UserID: &u.ID, Email: u.Email, Kind: store.AuthEventSessionRevoke, IP: clientIP(r)})

	if target.ID == sess.ID {
		http.SetCookie(w, &http.Cookie{
			Name: auth.SessionCookieName, Value: "", Path: "/", MaxAge: -1,
			HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/dashboard/settings", http.StatusSeeOther)
}

// handleSessionRevokeAllOthers revokes every session for the current user
// except the one making the request.
func (s *Server) handleSessionRevokeAllOthers(w http.ResponseWriter, r *http.Request) {
	sess := sessionFromContext(r)
	if !requireCSRF(w, r, sess) {
		return
	}
	u := userFromContext(r)

	if err := s.Store.DeleteSessionsForUserExcept(u.ID, sess.ID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.Store.InsertAuthEvent(store.AuthEvent{At: s.Now().UnixMilli(), UserID: &u.ID, Email: u.Email, Kind: store.AuthEventSessionRevoke, IP: clientIP(r)})
	http.Redirect(w, r, "/dashboard/settings", http.StatusSeeOther)
}

// handleUserCreate is admin-only (gated by requireAdmin in the router).
// Signup is closed by default (DESIGN.md §5.2); this is how admins add
// teammates directly.
func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFromContext(r)
	if !requireCSRF(w, r, sess) {
		return
	}

	email := normalizeEmail(r.FormValue("email"))
	password := r.FormValue("password")
	role := r.FormValue("role")
	if role != "admin" {
		role = "member"
	}

	if email == "" {
		http.Error(w, "email required", http.StatusBadRequest)
		return
	}
	if err := auth.ValidatePassword(password); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := s.Store.GetUserByEmail(email); err == nil {
		http.Error(w, "a user with that email already exists", http.StatusConflict)
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	newID, err := s.Store.CreateUser(email, hash, role, s.Now().UnixMilli())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.Store.InsertAuthEvent(store.AuthEvent{At: s.Now().UnixMilli(), UserID: &newID, Email: email, Kind: store.AuthEventUserCreate, IP: clientIP(r)})

	http.Redirect(w, r, "/dashboard/settings", http.StatusSeeOther)
}

// handleUserDeactivate toggles a user's active flag; admin-only.
func (s *Server) handleUserDeactivate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFromContext(r)
	if !requireCSRF(w, r, sess) {
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}
	target, err := s.Store.GetUserByID(id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := s.Store.SetUserActive(id, !target.Active); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard/settings", http.StatusSeeOther)
}

func findOwnedSession(s *Server, userID, sessionID int64) (*store.Session, error) {
	sessions, err := s.Store.ListSessionsForUser(userID)
	if err != nil {
		return nil, err
	}
	for i := range sessions {
		if sessions[i].ID == sessionID {
			return &sessions[i], nil
		}
	}
	return nil, store.ErrNotFound
}

func (s *Server) renderSettingsError(w http.ResponseWriter, r *http.Request, u *store.User, sess *store.Session, msg string) {
	sessions, _ := s.Store.ListSessionsForUser(u.ID)
	var users []store.User
	if u.Role == "admin" {
		users, _ = s.Store.ListUsers()
	}
	events, _ := s.Store.ListAuthEvents(50)
	retentionDays, _ := s.Store.GetRetentionDays()
	connected, lastIngestAt, lastEventAgo := s.dashboardStatus()
	s.render(w, "settings.html", settingsData{
		pageData: pageData{
			User: u, CSRFToken: sess.CSRFToken, Version: s.Version, Active: "settings",
			Connected: connected, LastIngestAt: lastIngestAt, LastEventAgo: lastEventAgo,
		},
		Sessions:      sessions,
		CurrentSessID: sess.ID,
		Users:         users,
		AuthEvents:    events,
		PasswordError: msg,
		RetentionDays: retentionDays,
		DataDir:       s.DataDir,
	})
}

func (s *Server) renderRetentionError(w http.ResponseWriter, r *http.Request, u *store.User, sess *store.Session, msg string) {
	sessions, _ := s.Store.ListSessionsForUser(u.ID)
	var users []store.User
	if u.Role == "admin" {
		users, _ = s.Store.ListUsers()
	}
	events, _ := s.Store.ListAuthEvents(50)
	connected, lastIngestAt, lastEventAgo := s.dashboardStatus()
	s.render(w, "settings.html", settingsData{
		pageData: pageData{
			User: u, CSRFToken: sess.CSRFToken, Version: s.Version, Active: "settings",
			Connected: connected, LastIngestAt: lastIngestAt, LastEventAgo: lastEventAgo,
		},
		Sessions:       sessions,
		CurrentSessID:  sess.ID,
		Users:          users,
		AuthEvents:     events,
		RetentionError: msg,
		DataDir:        s.DataDir,
	})
}
