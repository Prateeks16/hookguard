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

	client := &http.Client{Timeout: 30 * time.Second}
	mux := http.NewServeMux()
	for _, r := range cfg.Routes {
		v, err := buildVerifier(r)
		if err != nil {
			log.Fatalf("route %s: %v", r.Path, err)
		}
		mux.HandleFunc(r.Path, makeHandler(r, v, internalSecret, client))
		log.Printf("route %s [%s] -> %s", r.Path, r.Provider, r.Upstream)
	}

	log.Println("hookguard listening on :9000")
	log.Fatal(http.ListenAndServe(":9000", mux))
}

// makeHandler buffers the raw request body, verifies the Provider signature, and
// forwards the unaltered bytes upstream with a Gateway signature attached. The
// body stays the exact bytes received — never parsed or re-serialized — so the
// HMAC computed here matches the bytes the upstream sees.
func makeHandler(r Route, v Verifier, internalSecret []byte, client *http.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		if err := v.Verify(body, req.Header, time.Now()); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		forward(w, r, body, internalSecret, client)
	}
}

func forward(w http.ResponseWriter, r Route, body, internalSecret []byte, client *http.Client) {
	out, err := http.NewRequest(http.MethodPost, r.Upstream, bytes.NewReader(body))
	if err != nil {
		http.Error(w, "build upstream request", http.StatusBadGateway)
		return
	}
	out.Header.Set("Content-Type", "application/json")
	// Attach the Gateway signature: one internal HMAC the upstream verifies
	// instead of re-running the provider's verification.
	out.Header.Set(gatewaysig.ProviderHeader, r.Provider)
	out.Header.Set(gatewaysig.Header, gatewaysig.Sign(internalSecret, r.Provider, body))

	resp, err := client.Do(out)
	if err != nil {
		http.Error(w, "upstream unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	rb, _ := io.ReadAll(resp.Body)
	w.WriteHeader(resp.StatusCode)
	w.Write(rb)
}
