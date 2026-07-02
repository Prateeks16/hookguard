package server

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"hookguard/web/internal/auth"
)

// loginAsFreshUser signs up a new user (first signup becomes admin) and
// returns its session's CSRF token alongside the client.
func loginAsFreshUser(t *testing.T, srv *Server, ts string, email string) (*http.Client, string) {
	t.Helper()
	client := newClient(t)
	resp, err := client.PostForm(ts+"/signup", signupForm(email, "correct-horse-battery"))
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	resp.Body.Close()
	_, csrf := sessionCookieAndCSRF(t, srv, ts, client)
	return client, csrf
}

func endpointForm(csrf string, fields map[string]string) url.Values {
	v := url.Values{}
	v.Set("csrf_token", csrf)
	for k, val := range fields {
		v.Set(k, val)
	}
	return v
}

// 3. POST /dashboard/endpoints with provider=paypal and no webhook_id -> a
// clear 4xx validation error, not a 500, and no row created.
func TestCreateEndpointPayPalWithoutWebhookIDRejectedCleanly(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client, csrf := loginAsFreshUser(t, srv, ts.URL, "paypal-form@example.com")

	form := endpointForm(csrf, map[string]string{
		"path":         "/hook/paypal",
		"provider":     "paypal",
		"upstream_url": "http://localhost:8080/paypal",
		"webhook_id":   "",
	})
	resp, err := client.PostForm(ts.URL+"/dashboard/endpoints", form)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	body := readBody(t, resp)
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Fatalf("status = %d, want a 4xx validation error", resp.StatusCode)
	}
	if !strings.Contains(body, "webhook") {
		t.Fatalf("expected a webhook-related validation message, got: %s", body)
	}

	if _, err := srv.Store.GetEndpointByPath("/hook/paypal"); err == nil {
		t.Fatal("endpoint row was created despite invalid submission")
	}
}

// 4. HMAC provider with empty secret_env -> same treatment.
func TestCreateEndpointHMACWithoutSecretEnvRejectedCleanly(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client, csrf := loginAsFreshUser(t, srv, ts.URL, "hmac-form@example.com")

	form := endpointForm(csrf, map[string]string{
		"path":         "/hook/stripe",
		"provider":     "stripe",
		"upstream_url": "http://localhost:8080/stripe",
		"secret_env":   "",
	})
	resp, err := client.PostForm(ts.URL+"/dashboard/endpoints", form)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	body := readBody(t, resp)
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Fatalf("status = %d, want a 4xx validation error", resp.StatusCode)
	}
	if !strings.Contains(body, "secret") {
		t.Fatalf("expected a secret_env-related validation message, got: %s", body)
	}

	if _, err := srv.Store.GetEndpointByPath("/hook/stripe"); err == nil {
		t.Fatal("endpoint row was created despite invalid submission")
	}
}

// A valid Stripe submission succeeds and appears in the list.
func TestCreateEndpointStripeSucceeds(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client, csrf := loginAsFreshUser(t, srv, ts.URL, "stripe-ok@example.com")

	form := endpointForm(csrf, map[string]string{
		"path":          "/hook/stripe",
		"provider":      "stripe",
		"upstream_url":  "http://localhost:8080/stripe",
		"secret_env":    "STRIPE_SECRET",
		"replay_window": "5m",
	})
	resp, err := client.PostForm(ts.URL+"/dashboard/endpoints", form)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}

	ep, err := srv.Store.GetEndpointByPath("/hook/stripe")
	if err != nil {
		t.Fatalf("expected endpoint to exist: %v", err)
	}
	if ep.SecretEnv != "STRIPE_SECRET" || ep.WebhookID != "" {
		t.Fatalf("endpoint = %+v", ep)
	}
}

// A valid PayPal submission succeeds too.
func TestCreateEndpointPayPalSucceeds(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client, csrf := loginAsFreshUser(t, srv, ts.URL, "paypal-ok@example.com")

	form := endpointForm(csrf, map[string]string{
		"path":         "/hook/paypal",
		"provider":     "paypal",
		"upstream_url": "http://localhost:8080/paypal",
		"webhook_id":   "WH-ABC",
	})
	resp, err := client.PostForm(ts.URL+"/dashboard/endpoints", form)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}

	ep, err := srv.Store.GetEndpointByPath("/hook/paypal")
	if err != nil {
		t.Fatalf("expected endpoint to exist: %v", err)
	}
	if ep.WebhookID != "WH-ABC" || ep.SecretEnv != "" {
		t.Fatalf("endpoint = %+v", ep)
	}
}

// 5. Creating two endpoints with the same path is rejected cleanly (not a
// 500) and the second row is never created.
func TestCreateEndpointDuplicatePathRejectedCleanly(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client, csrf := loginAsFreshUser(t, srv, ts.URL, "dup-path@example.com")

	first := endpointForm(csrf, map[string]string{
		"path": "/hook/github", "provider": "github",
		"upstream_url": "http://localhost:8080/github", "secret_env": "GITHUB_SECRET",
	})
	resp, err := client.PostForm(ts.URL+"/dashboard/endpoints", first)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("first create status = %d, want 303", resp.StatusCode)
	}

	second := endpointForm(csrf, map[string]string{
		"path": "/hook/github", "provider": "github",
		"upstream_url": "http://localhost:8080/github-2", "secret_env": "GITHUB_SECRET_2",
	})
	resp, err = client.PostForm(ts.URL+"/dashboard/endpoints", second)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	body := readBody(t, resp)
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Fatalf("second create status = %d, want a 4xx error", resp.StatusCode)
	}
	if !strings.Contains(body, "already exists") {
		t.Fatalf("expected duplicate-path message, got: %s", body)
	}

	all, err := srv.Store.ListEndpoints()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("endpoint count = %d, want 1", len(all))
	}
}

// 6. Delete via DELETE succeeds at the HTTP layer (confirmation is a UI/JS
// concern — see endpoints.js's type-the-path prompt — not something the
// server enforces beyond auth+CSRF, same as any other REST delete route).
func TestDeleteEndpointSucceeds(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client, csrf := loginAsFreshUser(t, srv, ts.URL, "delete-me@example.com")

	create := endpointForm(csrf, map[string]string{
		"path": "/hook/shopify", "provider": "shopify",
		"upstream_url": "http://localhost:8080/shopify", "secret_env": "SHOPIFY_SECRET",
	})
	resp, err := client.PostForm(ts.URL+"/dashboard/endpoints", create)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	resp.Body.Close()

	ep, err := srv.Store.GetEndpointByPath("/hook/shopify")
	if err != nil {
		t.Fatalf("lookup created endpoint: %v", err)
	}

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/dashboard/endpoints/"+strconv.FormatInt(ep.ID, 10), nil)
	req.Header.Set(auth.CSRFHeader, csrf)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete status = %d, want 303", resp.StatusCode)
	}

	if _, err := srv.Store.GetEndpointByID(ep.ID); err == nil {
		t.Fatal("endpoint still present after delete")
	}
}

// Delete without a CSRF token is rejected.
func TestDeleteEndpointWithoutCSRFForbidden(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client, csrf := loginAsFreshUser(t, srv, ts.URL, "delete-nocsrf@example.com")

	create := endpointForm(csrf, map[string]string{
		"path": "/hook/shopify", "provider": "shopify",
		"upstream_url": "http://localhost:8080/shopify", "secret_env": "SHOPIFY_SECRET",
	})
	resp, err := client.PostForm(ts.URL+"/dashboard/endpoints", create)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	resp.Body.Close()

	ep, err := srv.Store.GetEndpointByPath("/hook/shopify")
	if err != nil {
		t.Fatalf("lookup created endpoint: %v", err)
	}

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/dashboard/endpoints/"+strconv.FormatInt(ep.ID, 10), nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("delete without CSRF status = %d, want 403", resp.StatusCode)
	}

	if _, err := srv.Store.GetEndpointByID(ep.ID); err != nil {
		t.Fatal("endpoint was deleted despite missing CSRF token")
	}
}

// 7. Export download returns valid JSON that round-trips through gwconfig
// and contains the created endpoint.
func TestExportDownloadReturnsValidConfig(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client, csrf := loginAsFreshUser(t, srv, ts.URL, "export@example.com")

	create := endpointForm(csrf, map[string]string{
		"path": "/hook/github", "provider": "github",
		"upstream_url": "http://localhost:8080/github", "secret_env": "GITHUB_SECRET",
	})
	resp, err := client.PostForm(ts.URL+"/dashboard/endpoints", create)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	resp.Body.Close()

	resp, err = client.Get(ts.URL + "/dashboard/endpoints/export/download")
	if err != nil {
		t.Fatalf("export download: %v", err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export status = %d, want 200: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	if !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("content-disposition = %q, want attachment", resp.Header.Get("Content-Disposition"))
	}
	if !strings.Contains(body, `"/hook/github"`) || !strings.Contains(body, `"GITHUB_SECRET"`) {
		t.Fatalf("exported body missing expected fields: %s", body)
	}
	_ = srv
}

// 8. Unauthenticated requests to every new endpoint route redirect to login.
func TestEndpointRoutesRequireAuth(t *testing.T) {
	_, ts := newTestServer(t, true)
	client := newClient(t)

	getRoutes := []string{
		"/dashboard/endpoints",
		"/dashboard/endpoints/new",
		"/dashboard/endpoints/1/edit",
		"/dashboard/endpoints/export",
		"/dashboard/endpoints/export/download",
	}
	for _, path := range getRoutes {
		resp, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("%s status = %d, want 303", path, resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/login") {
			t.Errorf("%s redirect location = %q, want /login prefix", path, loc)
		}
	}

	resp, err := client.PostForm(ts.URL+"/dashboard/endpoints", url.Values{})
	if err != nil {
		t.Fatalf("POST create: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("POST create status = %d, want 303", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/dashboard/endpoints/1", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("DELETE status = %d, want 303", resp.StatusCode)
	}
}

// Editing an endpoint via PUT updates its fields.
func TestUpdateEndpointSucceeds(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client, csrf := loginAsFreshUser(t, srv, ts.URL, "edit@example.com")

	create := endpointForm(csrf, map[string]string{
		"path": "/hook/github", "provider": "github",
		"upstream_url": "http://localhost:8080/github", "secret_env": "GITHUB_SECRET",
	})
	resp, err := client.PostForm(ts.URL+"/dashboard/endpoints", create)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	resp.Body.Close()

	ep, err := srv.Store.GetEndpointByPath("/hook/github")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	form := endpointForm(csrf, map[string]string{
		"path": "/hook/github", "provider": "github",
		"upstream_url": "http://localhost:8080/github-v2", "secret_env": "GITHUB_SECRET_V2",
	})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/dashboard/endpoints/"+strconv.FormatInt(ep.ID, 10), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("update status = %d, want 303", resp.StatusCode)
	}

	updated, err := srv.Store.GetEndpointByID(ep.ID)
	if err != nil {
		t.Fatalf("lookup updated: %v", err)
	}
	if updated.UpstreamURL != "http://localhost:8080/github-v2" || updated.SecretEnv != "GITHUB_SECRET_V2" {
		t.Fatalf("updated = %+v", updated)
	}
}

// Toggling active flips the flag without touching other fields.
func TestToggleEndpointActive(t *testing.T) {
	srv, ts := newTestServer(t, true)
	client, csrf := loginAsFreshUser(t, srv, ts.URL, "toggle@example.com")

	create := endpointForm(csrf, map[string]string{
		"path": "/hook/github", "provider": "github",
		"upstream_url": "http://localhost:8080/github", "secret_env": "GITHUB_SECRET",
	})
	resp, err := client.PostForm(ts.URL+"/dashboard/endpoints", create)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	resp.Body.Close()

	ep, err := srv.Store.GetEndpointByPath("/hook/github")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !ep.Active {
		t.Fatalf("expected new endpoint to default active, got %+v", ep)
	}

	form := url.Values{}
	form.Set("csrf_token", csrf)
	resp, err = client.PostForm(ts.URL+"/dashboard/endpoints/"+strconv.FormatInt(ep.ID, 10)+"/toggle-active", form)
	if err != nil {
		t.Fatalf("toggle: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("toggle status = %d, want 303", resp.StatusCode)
	}

	toggled, err := srv.Store.GetEndpointByID(ep.ID)
	if err != nil {
		t.Fatalf("lookup toggled: %v", err)
	}
	if toggled.Active {
		t.Fatal("expected endpoint to be inactive after toggle")
	}
}
