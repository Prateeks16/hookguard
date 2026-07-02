package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"hookguard/web/internal/gwconfig"
	"hookguard/web/internal/store"
)

type endpointsData struct {
	pageData
	Endpoints []store.Endpoint
}

type endpointFormData struct {
	pageData
	Endpoint  store.Endpoint
	IsNew     bool
	FormError string
}

func (s *Server) handleEndpointsList(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r)
	sess := sessionFromContext(r)

	endpoints, err := s.Store.ListEndpoints()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, "endpoints.html", endpointsData{
		pageData:  pageData{User: u, CSRFToken: sess.CSRFToken, Version: s.Version, Active: "endpoints"},
		Endpoints: endpoints,
	})
}

func (s *Server) handleEndpointNewForm(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r)
	sess := sessionFromContext(r)
	s.render(w, "endpoint_form.html", endpointFormData{
		pageData: pageData{User: u, CSRFToken: sess.CSRFToken, Version: s.Version, Active: "endpoints"},
		Endpoint: store.Endpoint{Provider: "stripe", ReplayWindow: "5m", Active: true},
		IsNew:    true,
	})
}

func (s *Server) handleEndpointEditForm(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r)
	sess := sessionFromContext(r)

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid endpoint id", http.StatusBadRequest)
		return
	}
	ep, err := s.Store.GetEndpointByID(id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	s.render(w, "endpoint_form.html", endpointFormData{
		pageData: pageData{User: u, CSRFToken: sess.CSRFToken, Version: s.Version, Active: "endpoints"},
		Endpoint: *ep,
		IsNew:    false,
	})
}

// endpointFromForm reads the shared create/edit form fields into a
// store.Endpoint. id is 0 for create.
func endpointFromForm(r *http.Request, id int64) store.Endpoint {
	e := store.Endpoint{
		ID:           id,
		Path:         r.FormValue("path"),
		Provider:     r.FormValue("provider"),
		UpstreamURL:  r.FormValue("upstream_url"),
		ReplayWindow: r.FormValue("replay_window"),
		SecretEnv:    r.FormValue("secret_env"),
		WebhookID:    r.FormValue("webhook_id"),
		Active:       true,
	}
	// Only the shape-relevant field for the chosen provider is kept — the
	// other stays empty so the CHECK constraint's XOR shape holds even if a
	// client-side toggle glitch submitted both.
	if e.Provider == "paypal" {
		e.SecretEnv = ""
	} else {
		e.WebhookID = ""
	}
	return e
}

// handleEndpointCreate validates the submission against the same
// per-provider rules as root verifier.go's buildVerifier (via
// gwconfig.Validate) BEFORE touching the store, so a bad submission comes
// back as a clear inline form error rather than a raw DB constraint
// violation (DESIGN.md §10 M3: "handle the DB error gracefully too, but the
// primary UX path should catch it before hitting the DB").
func (s *Server) handleEndpointCreate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFromContext(r)
	if !requireCSRF(w, r, sess) {
		return
	}
	u := userFromContext(r)

	e := endpointFromForm(r, 0)
	if formErr := validateEndpointForm(e); formErr != "" {
		s.renderEndpointFormError(w, u, sess, e, true, formErr)
		return
	}

	now := s.Now().UnixMilli()
	e.CreatedAt, e.UpdatedAt = now, now
	if _, err := s.Store.CreateEndpoint(e); err != nil {
		s.renderEndpointFormError(w, u, sess, e, true, dbErrorToFormMessage(err))
		return
	}
	http.Redirect(w, r, "/dashboard/endpoints", http.StatusSeeOther)
}

func (s *Server) handleEndpointUpdate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFromContext(r)
	if !requireCSRF(w, r, sess) {
		return
	}
	u := userFromContext(r)

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid endpoint id", http.StatusBadRequest)
		return
	}
	existing, err := s.Store.GetEndpointByID(id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	e := endpointFromForm(r, id)
	e.Active = existing.Active // active toggle is a separate action, not part of the edit form
	if formErr := validateEndpointForm(e); formErr != "" {
		s.renderEndpointFormError(w, u, sess, e, false, formErr)
		return
	}

	e.CreatedAt = existing.CreatedAt
	e.UpdatedAt = s.Now().UnixMilli()
	if err := s.Store.UpdateEndpoint(e); err != nil {
		s.renderEndpointFormError(w, u, sess, e, false, dbErrorToFormMessage(err))
		return
	}
	http.Redirect(w, r, "/dashboard/endpoints", http.StatusSeeOther)
}

// handleEndpointDelete removes an endpoint. The confirm step (DESIGN.md
// §6.2 "type-the-path confirm modal") is enforced client-side via a JS
// confirm() dialog that requires typing the path before the DELETE request
// is even issued — there is no durable server-side "pending delete" state to
// protect here (a bare DELETE at the HTTP layer is a legitimate API call,
// same as any other REST delete route; the confirm step is a UX guard
// against misclicks, not an authorization boundary — CSRF + session auth
// already gate this route).
func (s *Server) handleEndpointDelete(w http.ResponseWriter, r *http.Request) {
	sess := sessionFromContext(r)
	if !requireCSRF(w, r, sess) {
		return
	}

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid endpoint id", http.StatusBadRequest)
		return
	}
	if err := s.Store.DeleteEndpoint(id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// htmx issues this as a fetch, not a browser navigation; a redirect
	// response still lets hx-boost-free callers (plain fetch) see 303 and
	// the endpoints.html row template removes the row via an out-of-band
	// swap is more machinery than this milestone needs — simplest correct
	// behavior is a normal redirect the browser follows.
	http.Redirect(w, r, "/dashboard/endpoints", http.StatusSeeOther)
}

func (s *Server) handleEndpointToggleActive(w http.ResponseWriter, r *http.Request) {
	sess := sessionFromContext(r)
	if !requireCSRF(w, r, sess) {
		return
	}

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid endpoint id", http.StatusBadRequest)
		return
	}
	ep, err := s.Store.GetEndpointByID(id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := s.Store.SetEndpointActive(id, !ep.Active, s.Now().UnixMilli()); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard/endpoints", http.StatusSeeOther)
}

type endpointExportData struct {
	pageData
	JSON string
}

// handleEndpointExportPreview shows the JSON that GET .../export would
// download, per DESIGN.md §6.2's practical reading of the "diff view" —
// there is no "current file" concept inside the Console itself, so this is
// simply the DB-derived config rendered before the download.
func (s *Server) handleEndpointExportPreview(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r)
	sess := sessionFromContext(r)

	endpoints, err := s.Store.ListActiveEndpoints()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out, err := gwconfig.Marshal(gwconfig.Export(endpoints))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, "endpoint_export.html", endpointExportData{
		pageData: pageData{User: u, CSRFToken: sess.CSRFToken, Version: s.Version, Active: "endpoints"},
		JSON:     string(out),
	})
}

// handleEndpointExportDownload streams the exported config.json as an
// attachment (DESIGN.md §7.4, §6.2).
func (s *Server) handleEndpointExportDownload(w http.ResponseWriter, r *http.Request) {
	endpoints, err := s.Store.ListActiveEndpoints()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out, err := gwconfig.Marshal(gwconfig.Export(endpoints))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="config.json"`)
	w.Write(out)
}

// validateEndpointForm mirrors root verifier.go's buildVerifier rules
// (DESIGN.md §10 M3): HMAC providers need a non-empty secret_env; paypal
// needs a non-empty webhook_id and no secret_env; a non-empty replay_window
// must parse via time.ParseDuration. Returns "" when the submission is
// valid.
func validateEndpointForm(e store.Endpoint) string {
	if e.Path == "" {
		return "Path is required."
	}
	if e.Path[0] != '/' {
		return "Path must start with /."
	}
	if e.UpstreamURL == "" {
		return "Upstream URL is required."
	}
	switch e.Provider {
	case "paypal":
		if e.WebhookID == "" {
			return "PayPal requires a webhook ID."
		}
	case "stripe", "github", "shopify":
		if e.SecretEnv == "" {
			return "This provider requires the name of a secret environment variable."
		}
		if e.Provider == "stripe" && e.ReplayWindow != "" {
			if _, err := time.ParseDuration(e.ReplayWindow); err != nil {
				return "Replay window must be a Go duration like \"5m\" (or left blank)."
			}
		}
	default:
		return "Unknown provider."
	}
	return ""
}

func (s *Server) renderEndpointFormError(w http.ResponseWriter, u *store.User, sess *store.Session, e store.Endpoint, isNew bool, msg string) {
	w.WriteHeader(http.StatusUnprocessableEntity)
	s.render(w, "endpoint_form.html", endpointFormData{
		pageData:  pageData{User: u, CSRFToken: sess.CSRFToken, Version: s.Version, Active: "endpoints"},
		Endpoint:  e,
		IsNew:     isNew,
		FormError: msg,
	})
}

// dbErrorToFormMessage turns a raw CHECK/UNIQUE constraint violation into a
// message a user can act on — the last line of defense behind
// validateEndpointForm (DESIGN.md §10 M3: "handle the DB error gracefully
// too").
func dbErrorToFormMessage(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "UNIQUE constraint failed: endpoints.path"):
		return "An endpoint with that path already exists."
	case strings.Contains(msg, "CHECK constraint failed"):
		return "That combination of provider, secret_env and webhook_id isn't valid."
	default:
		return "Could not save endpoint."
	}
}
