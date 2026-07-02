package server

import (
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hookguard/web/internal/auth"
	"hookguard/web/internal/store"
)

// handleResetPasswordForm and handleResetPassword consume the one-time URL
// printed by `console reset-password <email>` (DESIGN.md §5.2). The token's
// hash and expiry live in settings("pwreset:<user_id>"), set by the CLI.
func (s *Server) handleResetPasswordForm(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	uid := r.URL.Query().Get("uid")
	if token == "" || uid == "" {
		s.render(w, "404.html", pageData{})
		return
	}
	s.render(w, "reset_password.html", pageData{ResetToken: token, ResetUID: uid})
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("token")
	uidStr := r.FormValue("uid")
	password := r.FormValue("password")
	confirm := r.FormValue("password_confirm")

	uid, err := strconv.ParseInt(uidStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid reset link", http.StatusBadRequest)
		return
	}

	if !s.consumeResetToken(uid, token) {
		http.Error(w, "invalid or expired reset link", http.StatusBadRequest)
		return
	}
	if password != confirm {
		http.Error(w, "passwords do not match", http.StatusBadRequest)
		return
	}
	if err := auth.ValidatePassword(password); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := s.Store.UpdatePasswordHash(uid, hash); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.Store.DeleteSetting(resetSettingKey(uid))
	s.Store.InsertAuthEvent(store.AuthEvent{At: s.Now().UnixMilli(), UserID: &uid, Kind: store.AuthEventPasswordChange, IP: clientIP(r)})

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func resetSettingKey(userID int64) string {
	return "pwreset:" + strconv.FormatInt(userID, 10)
}

// consumeResetToken validates token against the stored hash+expiry for
// userID and, if valid, deletes it so it cannot be reused.
func (s *Server) consumeResetToken(userID int64, token string) bool {
	val, err := s.Store.GetSetting(resetSettingKey(userID))
	if err != nil {
		return false
	}
	parts := strings.SplitN(val, ":", 2)
	if len(parts) != 2 {
		return false
	}
	storedHash, err := hex.DecodeString(parts[0])
	if err != nil {
		return false
	}
	expiresAtMs, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return false
	}
	if s.Now().After(time.UnixMilli(expiresAtMs)) {
		s.Store.DeleteSetting(resetSettingKey(userID))
		return false
	}

	gotHash := auth.HashToken(token)
	if subtle.ConstantTimeCompare(gotHash, storedHash) != 1 {
		return false
	}
	s.Store.DeleteSetting(resetSettingKey(userID))
	return true
}
