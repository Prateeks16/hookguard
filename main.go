package main

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"hookguard/internal/gatewaysig"
)

func main() {
	cfg, err := LoadConfig("config.json")
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	internalSecret := []byte(os.Getenv("INTERNAL_SECRET"))
	if len(internalSecret) == 0 {
		log.Fatal("INTERNAL_SECRET not set")
	}

	// EVENTS_URL is optional. Unset (the default): newEventEmitter starts no
	// goroutine and every record() call is a single no-op branch — today's
	// behavior, unchanged.
	events := newEventEmitter(os.Getenv("EVENTS_URL"), internalSecret)

	client := &http.Client{Timeout: 30 * time.Second}
	mux := http.NewServeMux()
	for _, r := range cfg.Routes {
		// Resolve the secret from the environment here, then hand the pure
		// factory already-resolved values.
		v, err := buildVerifier(r, os.Getenv(r.SecretEnv), verifierDeps{Client: client})
		if err != nil {
			log.Fatalf("route %s (secret env %s): %v", r.Path, r.SecretEnv, err)
		}
		mux.HandleFunc(r.Path, makeHandler(r, v, internalSecret, client, events))
		log.Printf("route %s [%s] -> %s", r.Path, r.Provider, r.Upstream)
	}

	log.Println("hookguard listening on :9000")
	log.Fatal(http.ListenAndServe(":9000", mux))
}

// makeHandler buffers the raw request body, verifies the Provider signature, and
// forwards the unaltered bytes upstream with a Gateway signature attached. The
// body stays the exact bytes received — never parsed or re-serialized — so the
// HMAC computed here matches the bytes the upstream sees. events is a nil-safe
// *eventEmitter; a nil or disabled emitter costs one branch per request.
func makeHandler(r Route, v Verifier, internalSecret []byte, client *http.Client, events *eventEmitter) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		if verr := v.Verify(body, req.Header, time.Now()); verr != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			events.record(r, body, req, "rejected", classifyReason(verr), 0, time.Since(start))
			return
		}
		status := forward(w, r, body, internalSecret, client)
		events.record(r, body, req, "accepted", "", status, time.Since(start))
	}
}

// forward returns the upstream's status code (0 if the request never reached
// or completed against the upstream) so the caller can report it in a verify
// event.
func forward(w http.ResponseWriter, r Route, body, internalSecret []byte, client *http.Client) int {
	out, err := http.NewRequest(http.MethodPost, r.Upstream, bytes.NewReader(body))
	if err != nil {
		http.Error(w, "build upstream request", http.StatusBadGateway)
		return 0
	}
	out.Header.Set("Content-Type", "application/json")
	// Attach the Gateway signature: one internal HMAC the upstream verifies
	// instead of re-running the provider's verification.
	out.Header.Set(gatewaysig.ProviderHeader, r.Provider)
	out.Header.Set(gatewaysig.Header, gatewaysig.Sign(internalSecret, r.Provider, body))

	resp, err := client.Do(out)
	if err != nil {
		http.Error(w, "upstream unreachable", http.StatusBadGateway)
		return 0
	}
	defer resp.Body.Close()

	rb, _ := io.ReadAll(resp.Body)
	w.WriteHeader(resp.StatusCode)
	w.Write(rb)
	return resp.StatusCode
}
