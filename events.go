package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"hookguard/internal/gatewaysig"
)

// verifyEvent is the JSON contract POSTed to EVENTS_URL after each verify
// decision (web/DESIGN.md §7.3) — the Console's ingest endpoint decodes this
// exact shape. Changing field names/types is a breaking change for the Console.
type verifyEvent struct {
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

// eventEmitter posts verifyEvents to EVENTS_URL, best-effort, off the request
// path. A nil *eventEmitter (or one built with an empty url) is a no-op: no
// goroutine runs, record does one string check and returns — the gateway's
// behavior with EVENTS_URL unset stays exactly what it was before this file
// existed.
type eventEmitter struct {
	url    string
	secret []byte
	client *http.Client
	ch     chan verifyEvent

	failCount int
	lastLog   time.Time
}

// newEventEmitter constructs an emitter and, only if url is non-empty, starts
// the single background goroutine that drains ch and posts events. The
// channel is sized 256 with drop-oldest-on-overflow (see emit): telemetry
// must never apply backpressure to the hot path.
func newEventEmitter(url string, secret []byte) *eventEmitter {
	e := &eventEmitter{
		url:    url,
		secret: secret,
		client: &http.Client{Timeout: 2 * time.Second},
		ch:     make(chan verifyEvent, 256),
	}
	if url != "" {
		go e.run()
	}
	return e
}

func (e *eventEmitter) enabled() bool {
	return e != nil && e.url != ""
}

// record builds and enqueues an event for one verify decision. Cheap to call
// unconditionally from makeHandler: the enabled check happens before any of
// the sha256/time work, so a disabled emitter costs one branch per request.
func (e *eventEmitter) record(r Route, body []byte, req *http.Request, verdict, reason string, upstreamStatus int, latency time.Duration) {
	if !e.enabled() {
		return
	}
	sum := sha256.Sum256(body)
	e.emit(verifyEvent{
		Timestamp:      time.Now().UTC(),
		Path:           r.Path,
		Provider:       r.Provider,
		Verdict:        verdict,
		Reason:         reason,
		UpstreamStatus: upstreamStatus,
		LatencyMS:      latency.Milliseconds(),
		BodyBytes:      len(body),
		BodySHA256:     hex.EncodeToString(sum[:]),
		RemoteIP:       remoteIP(req),
	})
}

// emit is a non-blocking send that drops the oldest queued event to make room
// for the newest one when the channel is full, rather than blocking the
// caller (a request-handling goroutine). Under concurrent overflow this is
// best-effort, not exact — acceptable for telemetry that must never
// backpressure verification.
func (e *eventEmitter) emit(ev verifyEvent) {
	select {
	case e.ch <- ev:
		return
	default:
	}
	select {
	case <-e.ch:
	default:
	}
	select {
	case e.ch <- ev:
	default:
	}
}

func (e *eventEmitter) run() {
	for ev := range e.ch {
		e.post(ev)
	}
}

// post is only ever called from the single run() goroutine, so it needs no
// locking even though it mutates failCount/lastLog.
func (e *eventEmitter) post(ev verifyEvent) {
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, e.url, bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(gatewaysig.ProviderHeader, "console-ingest")
	req.Header.Set(gatewaysig.Header, gatewaysig.Sign(e.secret, "console-ingest", data))

	resp, err := e.client.Do(req)
	if err != nil {
		e.logFailure(err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		e.logFailure(nil)
	}
}

// logFailure rate-limits delivery-failure logging to once a minute so a
// downed Console doesn't spam the gateway's log at request volume.
func (e *eventEmitter) logFailure(err error) {
	e.failCount++
	if time.Since(e.lastLog) < time.Minute {
		return
	}
	if err != nil {
		log.Printf("events: %d delivery failure(s) in the last interval, most recent: %v", e.failCount, err)
	} else {
		log.Printf("events: %d delivery failure(s) in the last interval (non-2xx response)", e.failCount)
	}
	e.failCount = 0
	e.lastLog = time.Now()
}

func remoteIP(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	return host
}

// classifyReason maps a Verifier's rejection error to the small stable
// taxonomy the Console's event contract expects (web/DESIGN.md §7.3).
// Verifiers return free-form errors.New/fmt.Errorf messages with no
// error-type hierarchy (stripe.go, github.go, shopify.go, paypal.go), so this
// is a substring classifier over the exact strings those files can return
// today — events_test.go enumerates every one of them so drift in a
// verifier's error text breaks a test instead of silently misclassifying.
// Cases that don't cleanly fit the taxonomy (a malformed cert response body,
// an RSA-key-type mismatch, a network fetch failure) land in "other" rather
// than being forced into a misleading bucket.
func classifyReason(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "missing") && strings.Contains(msg, "header"):
		return "missing header"
	case strings.Contains(msg, "outside replay window"):
		return "stale timestamp"
	case strings.Contains(msg, "not a trusted PayPal host"),
		strings.Contains(msg, "must be https"),
		strings.Contains(msg, "invalid paypal-cert-url"):
		return "cert host rejected"
	case strings.Contains(msg, "certificate chain"):
		return "cert chain invalid"
	case strings.Contains(msg, "unsupported paypal-auth-algo"):
		return "unsupported algorithm"
	case strings.Contains(msg, "signature mismatch"),
		strings.Contains(msg, "no matching signature"):
		return "signature mismatch"
	case strings.Contains(msg, "encoding"),
		strings.Contains(msg, "malformed"),
		strings.Contains(msg, "invalid timestamp"),
		strings.Contains(msg, "parse certificate"),
		strings.Contains(msg, "no certificate found"):
		return "bad encoding"
	default:
		return "other"
	}
}
