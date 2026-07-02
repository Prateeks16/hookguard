// Package ingest decodes gateway verdict events (DESIGN.md §7.3), verifies
// their Gateway signature, and batches them into the store on a 100ms
// ticker (§8.1) rather than one INSERT per HTTP request.
package ingest

import (
	"encoding/json"
	"errors"
	"time"

	"hookguard/internal/gatewaysig"
)

// providerLabel is the fixed X-HookGuard-Provider value the gateway's
// eventEmitter signs with (root events.go's post()) — not a real webhook
// provider name, just the ingest route's identity in the shared HMAC scheme.
const providerLabel = "console-ingest"

// Event mirrors root events.go's verifyEvent field-for-field; the JSON tags
// must match exactly since that struct is what's on the wire.
type Event struct {
	Timestamp      time.Time `json:"ts"`
	Path           string    `json:"path"`
	Provider       string    `json:"provider"`
	Verdict        string    `json:"verdict"`
	Reason         string    `json:"reason"`
	UpstreamStatus int       `json:"upstream_status"`
	LatencyMS      int64     `json:"latency_ms"`
	BodyBytes      int       `json:"body_bytes"`
	BodySHA256     string    `json:"body_sha256"`
	RemoteIP       string    `json:"remote_ip"`
}

// Verify checks the request body against the Gateway signature headers,
// using the same primitive the gateway signs with — no reimplementation.
func Verify(secret []byte, body []byte, sigHeader string) error {
	return gatewaysig.Verify(secret, providerLabel, body, sigHeader)
}

// ProviderHeader/SignatureHeader re-export the header names the handler
// reads, so callers outside this package don't import gatewaysig directly
// just to know the header names.
const (
	ProviderHeader  = gatewaysig.ProviderHeader
	SignatureHeader = gatewaysig.Header
)

// ExpectedProvider is the X-HookGuard-Provider value the ingest route
// requires; a request carrying any other value is rejected before the HMAC
// is even checked.
const ExpectedProvider = providerLabel

var errWrongProvider = errors.New("ingest: unexpected provider header")

// CheckProviderHeader rejects a request outright if it doesn't claim to be
// the console-ingest sender, independent of whether its signature verifies.
func CheckProviderHeader(got string) error {
	if got != ExpectedProvider {
		return errWrongProvider
	}
	return nil
}

// Decode parses one event JSON body. Returns an error for malformed JSON —
// the caller (handler) turns that into 400.
func Decode(body []byte) (Event, error) {
	var ev Event
	if err := json.Unmarshal(body, &ev); err != nil {
		return Event{}, err
	}
	return ev, nil
}
