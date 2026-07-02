package server

import (
	"io"
	"net/http"

	"hookguard/web/internal/ingest"
	"hookguard/web/internal/store"
)

// handleIngest is POST /api/v1/ingest (DESIGN.md §7.4) — Gateway-signature
// auth, deliberately not behind requireAuth/session middleware; the caller
// is the gateway's eventEmitter, not a browser. Verify before decode before
// persist: a bad signature must never cause a write, and a malformed body
// with a valid signature over those exact bytes must 400 without writing.
func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	if err := ingest.CheckProviderHeader(r.Header.Get(ingest.ProviderHeader)); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := ingest.Verify(s.InternalSecret, body, r.Header.Get(ingest.SignatureHeader)); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ev, err := ingest.Decode(body)
	if err != nil {
		http.Error(w, "malformed event", http.StatusBadRequest)
		return
	}

	s.Ingest.Enqueue(store.Event{
		ReceivedAt:     ev.Timestamp.UnixMilli(),
		Path:           ev.Path,
		Provider:       ev.Provider,
		Verdict:        ev.Verdict,
		Reason:         ev.Reason,
		UpstreamStatus: ev.UpstreamStatus,
		LatencyMS:      ev.LatencyMS,
		BodyBytes:      ev.BodyBytes,
		BodySHA256:     ev.BodySHA256,
		RemoteIP:       ev.RemoteIP,
	})

	w.WriteHeader(http.StatusAccepted)
}
