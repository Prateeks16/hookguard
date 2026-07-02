package server

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hookguard/web/internal/auth"
	"hookguard/web/internal/store"
)

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	if !s.AllowSignup {
		w.WriteHeader(http.StatusForbidden)
		s.render(w, "403.html", pageData{Error: "Ask your admin for an invite."})
		return
	}

	ip := clientIP(r)
	if allowed, retryAfter := s.SignupLimiter.Allow(ip, s.Now()); !allowed {
		writeRateLimited(w, retryAfter)
		return
	}

	email := normalizeEmail(r.FormValue("email"))
	password := r.FormValue("password")
	confirm := r.FormValue("password_confirm")

	if email == "" || password != confirm {
		s.render(w, "signup.html", pageData{Error: "Invalid email or password."})
		return
	}
	if err := auth.ValidatePassword(password); err != nil {
		s.render(w, "signup.html", pageData{Error: err.Error()})
		return
	}

	// Signup with an existing email returns the same generic path as any
	// other signup failure — DESIGN.md §5.1 enumeration defense.
	if _, err := s.Store.GetUserByEmail(email); err == nil {
		s.render(w, "signup.html", pageData{Error: "Invalid email or password."})
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	count, err := s.Store.CountUsers()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	role := "member"
	if count == 0 {
		role = "admin"
	}

	userID, err := s.Store.CreateUser(email, hash, role, s.Now().UnixMilli())
	if err != nil {
		s.render(w, "signup.html", pageData{Error: "Invalid email or password."})
		return
	}
	s.Store.InsertAuthEvent(store.AuthEvent{At: s.Now().UnixMilli(), UserID: &userID, Email: email, Kind: store.AuthEventUserCreate, IP: ip})

	s.startSession(w, r, userID)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	email := normalizeEmail(r.FormValue("email"))
	password := r.FormValue("password")
	ip := clientIP(r)
	limitKey := ip + "|" + email

	if allowed, retryAfter := s.LoginLimiter.Allow(limitKey, s.Now()); !allowed {
		writeRateLimited(w, retryAfter)
		return
	}

	next := safeNext(r.FormValue("next"))
	genericErr := pageData{Error: "Invalid email or password.", Next: next}

	user, err := s.Store.GetUserByEmail(email)
	if err != nil {
		// Unknown email: still run Argon2id so timing doesn't leak account
		// existence — DESIGN.md §5.1.
		auth.VerifyPassword(password, auth.DummyHash())
		s.Store.InsertAuthEvent(store.AuthEvent{At: s.Now().UnixMilli(), Email: email, Kind: store.AuthEventLoginFail, IP: ip})
		s.render(w, "login.html", genericErr)
		return
	}

	if !user.Active || !auth.VerifyPassword(password, user.PasswordHash) {
		s.Store.InsertAuthEvent(store.AuthEvent{At: s.Now().UnixMilli(), UserID: &user.ID, Email: email, Kind: store.AuthEventLoginFail, IP: ip})
		s.render(w, "login.html", genericErr)
		return
	}

	s.Store.InsertAuthEvent(store.AuthEvent{At: s.Now().UnixMilli(), UserID: &user.ID, Email: email, Kind: store.AuthEventLoginOK, IP: ip})
	s.startSession(w, r, user.ID)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	sess := sessionFromContext(r)
	if !requireCSRF(w, r, sess) {
		return
	}

	s.Store.DeleteSession(sess.ID)
	user := userFromContext(r)
	email := ""
	if user != nil {
		email = user.Email
	}
	s.Store.InsertAuthEvent(store.AuthEvent{At: s.Now().UnixMilli(), UserID: &sess.UserID, Email: email, Kind: store.AuthEventLogout, IP: clientIP(r)})

	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// startSession creates a fresh session (rotating the token, per DESIGN.md
// §5.1's session-fixation defense — a new token is minted on every login,
// including re-login by an already-authenticated browser) and sets the
// cookie.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, userID int64) {
	token, hash, err := auth.NewSessionToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	csrfToken, err := auth.NewCSRFToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	now := s.Now()
	_, err = s.Store.CreateSession(store.Session{
		TokenHash:  hash,
		UserID:     userID,
		CSRFToken:  csrfToken,
		CreatedAt:  now.UnixMilli(),
		LastSeenAt: now.UnixMilli(),
		ExpiresAt:  now.Add(auth.SessionAbsoluteTimeout).UnixMilli(),
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(auth.SessionAbsoluteTimeout.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	secs := int(retryAfter.Seconds())
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	w.WriteHeader(http.StatusTooManyRequests)
	w.Write([]byte("Too many attempts. Try again later."))
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
