package server

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"time"

	"hookguard/web/internal/store"
)

// gatewayLiveness answers "connected" per DESIGN.md §6.1: within 60s of the
// last recorded ingest. lastIngestAt is a unix-ms timestamp read from
// settings ("last_ingest_at", written by the ingest batcher on every flush);
// zero (never set) is always stale.
const livenessWindow = 60 * time.Second

func gatewayConnected(now time.Time, lastIngestAtMS int64) bool {
	if lastIngestAtMS == 0 {
		return false
	}
	return now.Sub(time.UnixMilli(lastIngestAtMS)) <= livenessWindow
}

// dashboardStatus reads last_ingest_at from settings and evaluates liveness
// against now, filling in the status-strip fields of a pageData (DESIGN.md
// §6.1). A missing setting (fresh install, no gateway traffic yet) is not an
// error — it's the expected "no signal" state, so the zero value is correct.
// Read fresh per-request in each /dashboard/* handler, the same way
// CSRFToken/Version already are, rather than adding new middleware.
func (s *Server) dashboardStatus() (connected bool, lastIngestAt int64, lastEventAgo string) {
	v, err := s.Store.GetSetting("last_ingest_at")
	if err != nil {
		return false, 0, ""
	}
	ms, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return false, 0, ""
	}
	now := s.Now()
	return gatewayConnected(now, ms), ms, humanAgo(now.Sub(time.UnixMilli(ms)))
}

// humanAgo renders a coarse "Ns ago"/"Nm ago"/"Nh ago" string for the status
// strip (DESIGN.md §6.1 example: "last event 3s ago") — deliberately coarse,
// this isn't a precision timestamp.
func humanAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

// overviewWindowHours maps the ?window= querystring value to an hour count
// (DESIGN.md §6.2's 24h/7d toggle, §7.4's window param). Anything else
// defaults to 24h.
func overviewWindowHours(r *http.Request) (hours int, window string) {
	if r.URL.Query().Get("window") == "7d" {
		return 24 * 7, "7d"
	}
	return 24, "24h"
}

// rejectedRow is a template-ready view of one rejected event — the raw
// unix-ms ReceivedAt is formatted in Go, matching how middleware.go and
// handlers_reset.go already do all time formatting/arithmetic in Go rather
// than in html/template (no funcmap exists in this codebase; adding one for
// a single time.Format call isn't worth the new plumbing).
type rejectedRow struct {
	Time     string
	Provider string
	Path     string
	Reason   string
	RemoteIP string
}

type overviewData struct {
	pageData
	Accepted       int
	Rejected       int
	AcceptRatePct  string // "42%" or "—" when there's no data yet
	P50LatencyMS   string // "12ms" or "—"
	ChartSVG       template.HTML
	RecentRejected []rejectedRow
	Window         string
	HasAnyEvent    bool
}

// handleOverview is the real GET /dashboard route (M4c): stat cards, chart
// and recent-rejections table are now backed by real store queries —
// replacing M2's placeholder zeros (DESIGN.md §6.2, §10 M4).
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r)
	sess := sessionFromContext(r)

	hours, window := overviewWindowHours(r)
	now := s.Now()

	hasAny, err := s.Store.HasAnyEvent()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	connected, lastIngestAt, lastEventAgo := s.dashboardStatus()
	data := overviewData{
		pageData: pageData{
			User: u, CSRFToken: sess.CSRFToken, Version: s.Version, Active: "overview",
			Connected: connected, LastIngestAt: lastIngestAt, LastEventAgo: lastEventAgo,
		},
		Window:      window,
		HasAnyEvent: hasAny,
	}

	if hasAny {
		summary, err := s.Store.SummaryWindow(now, hours)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		buckets, err := s.Store.HourlyCountsWindow(now, hours)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		recent, err := s.Store.RecentRejected(10)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		data.Accepted = summary.Accepted
		data.Rejected = summary.Rejected
		data.AcceptRatePct = formatAcceptRate(summary)
		data.P50LatencyMS = fmt.Sprintf("%dms", summary.P50LatencyMS)
		data.ChartSVG = template.HTML(renderHourlyChart(buckets))
		data.RecentRejected = toRejectedRows(recent)
	}

	s.render(w, "overview.html", data)
}

// formatAcceptRate renders "—" when there's no data at all (Accepted +
// Rejected == 0) rather than "0%" — a fresh instance hasn't rejected
// everything, it just hasn't seen anything yet.
func formatAcceptRate(sum store.Summary) string {
	if sum.Accepted+sum.Rejected == 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", sum.AcceptRate*100)
}

func toRejectedRows(events []store.Event) []rejectedRow {
	rows := make([]rejectedRow, len(events))
	for i, e := range events {
		rows[i] = rejectedRow{
			Time:     time.UnixMilli(e.ReceivedAt).UTC().Format("2006-01-02 15:04:05"),
			Provider: e.Provider,
			Path:     e.Path,
			Reason:   e.Reason,
			RemoteIP: e.RemoteIP,
		}
	}
	return rows
}

// statsSummaryResponse is the JSON body for GET /api/v1/stats/summary
// (DESIGN.md §7.4) — the shape htmx polling swaps into the stat cards.
type statsSummaryResponse struct {
	Accepted     int     `json:"accepted"`
	Rejected     int     `json:"rejected"`
	AcceptRate   float64 `json:"accept_rate"`
	P50LatencyMS int64   `json:"p50_latency_ms"`
	Window       string  `json:"window"`
}

// handleStatsSummary is GET /api/v1/stats/summary?window=24h|7d
// (DESIGN.md §7.4) — session-authed (behind requireAuth in Router, NOT the
// ingest route's gatewaysig auth), polled by htmx every 30s to refresh the
// Overview stat cards without a full page reload.
func (s *Server) handleStatsSummary(w http.ResponseWriter, r *http.Request) {
	hours, window := overviewWindowHours(r)
	summary, err := s.Store.SummaryWindow(s.Now(), hours)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(statsSummaryResponse{
		Accepted:     summary.Accepted,
		Rejected:     summary.Rejected,
		AcceptRate:   summary.AcceptRate,
		P50LatencyMS: summary.P50LatencyMS,
		Window:       window,
	})
}

// handleDashboardPlaceholder serves the minimal "coming in a later milestone"
// page for Providers (Live Logs is a separate M4 follow-up) — nav must not
// 404, but that page's logic doesn't belong here yet.
func (s *Server) handleDashboardPlaceholder(active, title string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userFromContext(r)
		sess := sessionFromContext(r)
		connected, lastIngestAt, lastEventAgo := s.dashboardStatus()
		s.render(w, "placeholder.html", pageData{
			User: u, CSRFToken: sess.CSRFToken, Version: s.Version, Active: active, Flash: title,
			Connected: connected, LastIngestAt: lastIngestAt, LastEventAgo: lastEventAgo,
		})
	}
}
