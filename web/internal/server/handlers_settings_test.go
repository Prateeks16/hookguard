package server

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"hookguard/web/internal/auth"
)

// sessionCookieAndCSRF signs up (or must already have signed up) and returns
// the session cookie value + its CSRF token, mirroring how a page would read
// the token from its hidden form input.
func sessionCookieAndCSRF(t *testing.T, srv *Server, ts string, client *http.Client) (token, csrf string) {
	t.Helper()
	u, _ := url.Parse(ts)
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == auth.SessionCookieName {
			token = c.Value
		}
	}
	if token == "" {
		t.Fatal("no session cookie")
	}
	sess, err := srv.Store.GetSessionByTokenHash(auth.HashToken(token))
	if err != nil {
		t.Fatalf("lookup session: %v", err)
	}
	return token, sess.CSRFToken
}

// 1. Unauthenticated GET on every new dashboard sub-route redirects to /login.
func TestDashboardSubRoutesRequireAuth(t *testing.T) {
	_, ts := newTestServer(t, true)
	client := newClient(t)

	routes := []string{
		"/dashboard",
		"/dashboard/endpoints",
		"/dashboard/logs",
		"/dashboard/providers",
		"/dashboard/settings",
	}
	for _, path := range routes {
		resp, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("%s status = %d, want 303", path, resp.StatusCode)
		}
		loc := resp.Header.Get("Location")
		if !strings.HasPrefix(loc, "/login") {
			t.Errorf("%s redirect location = %q, want /login prefix", path, loc)
		}
	}
}

// 2. Password change with correct current password succeeds and rotates the
// session: the old cookie's session row is gone.
func TestPasswordChangeRotatesSession(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client := newClient(t)

	resp, _ := client.PostForm(ts.URL+"/signup", signupForm("heidi@example.com", "correct-horse-battery"))
	resp.Body.Close()

	oldToken, csrfToken := sessionCookieAndCSRF(t, srv, ts.URL, client)

	form := url.Values{}
	form.Set("csrf_token", csrfToken)
	form.Set("current_password", "correct-horse-battery")
	form.Set("new_password", "new-correct-horse-battery")
	form.Set("new_password_confirm", "new-correct-horse-battery")

	resp, err := client.PostForm(ts.URL+"/dashboard/settings/password", form)
	if err != nil {
		t.Fatalf("password change: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("password change status = %d, want 303", resp.StatusCode)
	}

	if _, err := srv.Store.GetSessionByTokenHash(auth.HashToken(oldToken)); err == nil {
		t.Fatal("old session token still valid after password change")
	}

	newToken, _ := sessionCookieAndCSRF(t, srv, ts.URL, client)
	if newToken == oldToken {
		t.Fatal("session token did not rotate")
	}

	// New password must actually work.
	freshClient := newClient(t)
	resp, err = freshClient.PostForm(ts.URL+"/login", loginForm("heidi@example.com", "new-correct-horse-battery"))
	if err != nil {
		t.Fatalf("login with new password: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login with new password status = %d, want 303", resp.StatusCode)
	}
}

// 3. Password change with wrong current password is rejected and the
// session is NOT rotated.
func TestPasswordChangeWrongCurrentPasswordRejected(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client := newClient(t)

	resp, _ := client.PostForm(ts.URL+"/signup", signupForm("ivan@example.com", "correct-horse-battery"))
	resp.Body.Close()

	oldToken, csrfToken := sessionCookieAndCSRF(t, srv, ts.URL, client)

	form := url.Values{}
	form.Set("csrf_token", csrfToken)
	form.Set("current_password", "totally-wrong-password")
	form.Set("new_password", "new-correct-horse-battery")
	form.Set("new_password_confirm", "new-correct-horse-battery")

	resp, err := client.PostForm(ts.URL+"/dashboard/settings/password", form)
	if err != nil {
		t.Fatalf("password change: %v", err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("password change status = %d, want 200 (re-rendered form with error)", resp.StatusCode)
	}
	if !strings.Contains(body, "Current password is incorrect.") {
		t.Fatalf("expected current-password error, got: %s", body)
	}

	if _, err := srv.Store.GetSessionByTokenHash(auth.HashToken(oldToken)); err != nil {
		t.Fatal("session was rotated/destroyed despite wrong current password")
	}
}

// 4. Revoking another session invalidates it; the revoking session still
// works. Simulates two browsers via two cookie jars for the same user.
func TestRevokeOtherSessionInvalidatesIt(t *testing.T) {
	srv, ts := newTestServer(t, true)
	browserA := newClient(t)

	resp, _ := browserA.PostForm(ts.URL+"/signup", signupForm("judy@example.com", "correct-horse-battery"))
	resp.Body.Close()

	browserB := newClient(t)
	resp, err := browserB.PostForm(ts.URL+"/login", loginForm("judy@example.com", "correct-horse-battery"))
	if err != nil {
		t.Fatalf("browser B login: %v", err)
	}
	resp.Body.Close()

	tokenB, _ := sessionCookieAndCSRF(t, srv, ts.URL, browserB)
	sessB, err := srv.Store.GetSessionByTokenHash(auth.HashToken(tokenB))
	if err != nil {
		t.Fatalf("lookup session B: %v", err)
	}

	_, csrfA := sessionCookieAndCSRF(t, srv, ts.URL, browserA)
	form := url.Values{}
	form.Set("csrf_token", csrfA)
	resp, err = browserA.PostForm(ts.URL+"/dashboard/settings/sessions/"+strconv.FormatInt(sessB.ID, 10)+"/revoke", form)
	if err != nil {
		t.Fatalf("revoke session B: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoke status = %d, want 303", resp.StatusCode)
	}

	// Browser B's next authenticated request must redirect to login.
	resp, err = browserB.Get(ts.URL + "/dashboard")
	if err != nil {
		t.Fatalf("browser B dashboard after revoke: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("browser B dashboard status = %d, want 303 redirect to login", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/login") {
		t.Fatalf("browser B redirect location = %q, want /login prefix", loc)
	}

	// Browser A (the revoker) still works.
	resp, err = browserA.Get(ts.URL + "/dashboard")
	if err != nil {
		t.Fatalf("browser A dashboard: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("browser A dashboard status = %d, want 200", resp.StatusCode)
	}
}

// 5. Non-admin hitting an admin-only Settings route gets 403 — both GET
// (list users) and POST (create user), server-side, independent of the UI.
func TestNonAdminSettingsUsersRoutesForbidden(t *testing.T) {
	srv, ts := newTestServer(t, true)

	// First signup becomes admin; second becomes a plain member.
	admin := newClient(t)
	resp, _ := admin.PostForm(ts.URL+"/signup", signupForm("admin@example.com", "correct-horse-battery"))
	resp.Body.Close()

	member := newClient(t)
	resp, err := member.PostForm(ts.URL+"/signup", signupForm("member@example.com", "correct-horse-battery"))
	if err != nil {
		t.Fatalf("member signup: %v", err)
	}
	resp.Body.Close()

	u, err := srv.Store.GetUserByEmail("member@example.com")
	if err != nil {
		t.Fatalf("lookup member: %v", err)
	}
	if u.Role != "member" {
		t.Fatalf("expected second signup to be role=member, got %q", u.Role)
	}

	resp, err = member.Get(ts.URL + "/dashboard/settings/users")
	if err != nil {
		t.Fatalf("member GET users: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member GET /dashboard/settings/users status = %d, want 403", resp.StatusCode)
	}

	_, csrfToken := sessionCookieAndCSRF(t, srv, ts.URL, member)
	form := url.Values{}
	form.Set("csrf_token", csrfToken)
	form.Set("email", "newuser@example.com")
	form.Set("password", "correct-horse-battery")
	resp, err = member.PostForm(ts.URL+"/dashboard/settings/users", form)
	if err != nil {
		t.Fatalf("member POST users: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member POST /dashboard/settings/users status = %d, want 403", resp.StatusCode)
	}

	// Sanity: the admin CAN do both.
	_, adminCSRF := sessionCookieAndCSRF(t, srv, ts.URL, admin)
	resp, err = admin.Get(ts.URL + "/dashboard/settings/users")
	if err != nil {
		t.Fatalf("admin GET users: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin GET /dashboard/settings/users status = %d, want 200", resp.StatusCode)
	}

	adminForm := url.Values{}
	adminForm.Set("csrf_token", adminCSRF)
	adminForm.Set("email", "newuser@example.com")
	adminForm.Set("password", "correct-horse-battery")
	resp, err = admin.PostForm(ts.URL+"/dashboard/settings/users", adminForm)
	if err != nil {
		t.Fatalf("admin POST users: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("admin POST /dashboard/settings/users status = %d, want 303", resp.StatusCode)
	}
}

// A user cannot revoke another user's session by guessing its ID.
func TestRevokeSessionCrossUserForbidden(t *testing.T) {
	srv, ts := newTestServer(t, true)

	victim := newClient(t)
	resp, _ := victim.PostForm(ts.URL+"/signup", signupForm("laura@example.com", "correct-horse-battery"))
	resp.Body.Close()
	victimToken, _ := sessionCookieAndCSRF(t, srv, ts.URL, victim)
	victimSess, err := srv.Store.GetSessionByTokenHash(auth.HashToken(victimToken))
	if err != nil {
		t.Fatalf("lookup victim session: %v", err)
	}

	attacker := newClient(t)
	resp, err = attacker.PostForm(ts.URL+"/signup", signupForm("mallory@example.com", "correct-horse-battery"))
	if err != nil {
		t.Fatalf("attacker signup: %v", err)
	}
	resp.Body.Close()
	_, attackerCSRF := sessionCookieAndCSRF(t, srv, ts.URL, attacker)

	form := url.Values{}
	form.Set("csrf_token", attackerCSRF)
	resp, err = attacker.PostForm(ts.URL+"/dashboard/settings/sessions/"+strconv.FormatInt(victimSess.ID, 10)+"/revoke", form)
	if err != nil {
		t.Fatalf("cross-user revoke: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-user revoke status = %d, want 404", resp.StatusCode)
	}

	if _, err := srv.Store.GetSessionByTokenHash(auth.HashToken(victimToken)); err != nil {
		t.Fatal("victim session was deleted by another user's request")
	}
}

// Unmatched routes render the 404 page.
func TestUnmatchedRouteRenders404(t *testing.T) {
	_, ts := newTestServer(t, true)
	client := newClient(t)

	resp, err := client.Get(ts.URL + "/this/route/does/not/exist")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if !strings.Contains(body, "isn't in my config") {
		t.Fatalf("expected 404 page body, got: %s", body)
	}
}

// 6. CSRF-less POST to password-change or session-revoke -> 403.
func TestSettingsMutationsRequireCSRF(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client := newClient(t)

	resp, _ := client.PostForm(ts.URL+"/signup", signupForm("kim@example.com", "correct-horse-battery"))
	resp.Body.Close()

	resp, err := client.PostForm(ts.URL+"/dashboard/settings/password", url.Values{
		"current_password":     {"correct-horse-battery"},
		"new_password":         {"new-correct-horse-battery"},
		"new_password_confirm": {"new-correct-horse-battery"},
	})
	if err != nil {
		t.Fatalf("password change without csrf: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("password change without CSRF status = %d, want 403", resp.StatusCode)
	}

	token, _ := sessionCookieAndCSRF(t, srv, ts.URL, client)
	sess, err := srv.Store.GetSessionByTokenHash(auth.HashToken(token))
	if err != nil {
		t.Fatalf("lookup session: %v", err)
	}

	resp, err = client.PostForm(ts.URL+"/dashboard/settings/sessions/"+strconv.FormatInt(sess.ID, 10)+"/revoke", url.Values{})
	if err != nil {
		t.Fatalf("revoke without csrf: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("revoke without CSRF status = %d, want 403", resp.StatusCode)
	}
}
