package server

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"hookguard/web/internal/auth"
	"hookguard/web/internal/store"
	"hookguard/web/ui"
)

func newTestServer(t *testing.T, allowSignup bool) (*Server, *httptest.Server) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "console.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	srv, err := New(st, ui.TemplatesFS, allowSignup, "test", []byte(testInternalSecret), t.TempDir())
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(srv.Router(ui.StaticFS))
	t.Cleanup(ts.Close)
	return srv, ts
}

func newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func signupForm(email, password string) url.Values {
	v := url.Values{}
	v.Set("email", email)
	v.Set("password", password)
	v.Set("password_confirm", password)
	return v
}

func loginForm(email, password string) url.Values {
	v := url.Values{}
	v.Set("email", email)
	v.Set("password", password)
	return v
}

// 1. Signup -> login -> logout roundtrip succeeds.
func TestSignupLoginLogoutRoundtrip(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client := newClient(t)

	resp, err := client.PostForm(ts.URL+"/signup", signupForm("alice@example.com", "correct-horse-battery"))
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("signup status = %d, want 303", resp.StatusCode)
	}

	u, _ := url.Parse(ts.URL)
	var sessionCookie *http.Cookie
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == auth.SessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("no session cookie after signup")
	}

	resp, err = client.Get(ts.URL + "/dashboard")
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard status = %d, want 200", resp.StatusCode)
	}

	// Look the CSRF token up via the store directly, mirroring how a real
	// page would read it from a hidden form input — M1 has no dashboard
	// page yet to source it from.
	sess, err := srv.Store.GetSessionByTokenHash(auth.HashToken(sessionCookie.Value))
	if err != nil {
		t.Fatalf("lookup session: %v", err)
	}

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/logout", nil)
	req.Header.Set(auth.CSRFHeader, sess.CSRFToken)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout status = %d, want 303", resp.StatusCode)
	}

	resp, err = client.Get(ts.URL + "/dashboard")
	if err != nil {
		t.Fatalf("dashboard after logout: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("dashboard after logout status = %d, want 303 redirect to login", resp.StatusCode)
	}
}

// 2. Second signup attempt when signup is closed -> 403.
func TestSignupClosedReturns403(t *testing.T) {
	_, ts := newTestServer(t, false)
	client := newClient(t)

	resp, err := client.PostForm(ts.URL+"/signup", signupForm("bob@example.com", "correct-horse-battery"))
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("signup status = %d, want 403", resp.StatusCode)
	}
}

// 3. Wrong password -> generic "Invalid email or password" error, same
// message for unknown users.
func TestLoginGenericErrorMessage(t *testing.T) {
	_, ts := newTestServer(t, true)
	client := newClient(t)

	resp, _ := client.PostForm(ts.URL+"/signup", signupForm("carol@example.com", "correct-horse-battery"))
	resp.Body.Close()

	wrongPwClient := newClient(t)
	resp, err := wrongPwClient.PostForm(ts.URL+"/login", loginForm("carol@example.com", "totally-wrong-password"))
	if err != nil {
		t.Fatalf("login wrong password: %v", err)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "Invalid email or password.") {
		t.Fatalf("expected generic error for wrong password, got: %s", body)
	}

	unknownClient := newClient(t)
	resp, err = unknownClient.PostForm(ts.URL+"/login", loginForm("nobody@example.com", "whatever-password"))
	if err != nil {
		t.Fatalf("login unknown user: %v", err)
	}
	body = readBody(t, resp)
	if !strings.Contains(body, "Invalid email or password.") {
		t.Fatalf("expected generic error for unknown user, got: %s", body)
	}
}

// 4. 11th login attempt within 15 minutes for the same IP+email -> 429 with
// Retry-After.
func TestLoginRateLimited(t *testing.T) {
	_, ts := newTestServer(t, true)
	setup := newClient(t)
	resp, _ := setup.PostForm(ts.URL+"/signup", signupForm("dave@example.com", "correct-horse-battery"))
	resp.Body.Close()

	client := newClient(t)
	var last *http.Response
	for i := 0; i < 11; i++ {
		r, err := client.PostForm(ts.URL+"/login", loginForm("dave@example.com", "wrong-password-here"))
		if err != nil {
			t.Fatalf("login attempt %d: %v", i, err)
		}
		if i < 10 {
			r.Body.Close()
		}
		last = r
	}
	defer last.Body.Close()

	if last.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("11th attempt status = %d, want 429", last.StatusCode)
	}
	if last.Header.Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header on 429")
	}
}

// 5. Session cookie has HttpOnly, Secure, SameSite=Lax flags set.
func TestSessionCookieFlags(t *testing.T) {
	_, ts := newTestServer(t, true)
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.PostForm(ts.URL+"/signup", signupForm("erin@example.com", "correct-horse-battery"))
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	defer resp.Body.Close()

	var found *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookieName {
			found = c
		}
	}
	if found == nil {
		t.Fatal("no session cookie set")
	}
	if !found.HttpOnly {
		t.Error("session cookie missing HttpOnly")
	}
	if !found.Secure {
		t.Error("session cookie missing Secure")
	}
	if found.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie SameSite = %v, want Lax", found.SameSite)
	}
}

// 6. A non-GET request without a valid CSRF token -> 403.
func TestLogoutWithoutCSRFForbidden(t *testing.T) {
	_, ts := newTestServer(t, true)
	client := newClient(t)

	resp, _ := client.PostForm(ts.URL+"/signup", signupForm("frank@example.com", "correct-horse-battery"))
	resp.Body.Close()

	resp, err := client.Post(ts.URL+"/logout", "application/x-www-form-urlencoded", strings.NewReader(""))
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("logout without CSRF status = %d, want 403", resp.StatusCode)
	}
}

// 7. After a session is revoked directly via the store, the next
// authenticated request redirects to login.
func TestRevokedSessionRedirectsToLogin(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client := newClient(t)

	resp, _ := client.PostForm(ts.URL+"/signup", signupForm("grace@example.com", "correct-horse-battery"))
	resp.Body.Close()

	resp, err := client.Get(ts.URL + "/dashboard")
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard before revoke status = %d, want 200", resp.StatusCode)
	}

	u, _ := url.Parse(ts.URL)
	var tokenHash []byte
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == auth.SessionCookieName {
			tokenHash = auth.HashToken(c.Value)
		}
	}
	if tokenHash == nil {
		t.Fatal("no session cookie")
	}
	sess, err := srv.Store.GetSessionByTokenHash(tokenHash)
	if err != nil {
		t.Fatalf("lookup session: %v", err)
	}
	if err := srv.Store.DeleteSession(sess.ID); err != nil {
		t.Fatalf("revoke session: %v", err)
	}

	resp, err = client.Get(ts.URL + "/dashboard")
	if err != nil {
		t.Fatalf("dashboard after revoke: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("dashboard after revoke status = %d, want 303 redirect to login", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/login") {
		t.Fatalf("redirect location = %q, want /login prefix", loc)
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	buf := make([]byte, 8192)
	n, _ := resp.Body.Read(buf)
	return string(buf[:n])
}
