// Command upstream is a sample protected application. It trusts nothing on the
// network: it accepts a request only if the Gateway signature verifies against
// the shared INTERNAL_SECRET. A real upstream reimplements this one check (in
// any language) — replacing the six bespoke Provider verifications it would
// otherwise need.
package main

import (
	"io"
	"log"
	"net/http"
	"os"

	"hookguard/internal/gatewaysig"
)

func main() {
	secret := []byte(os.Getenv("INTERNAL_SECRET"))
	if len(secret) == 0 {
		log.Fatal("INTERNAL_SECRET not set")
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		provider := r.Header.Get(gatewaysig.ProviderHeader)
		if err := gatewaysig.Verify(secret, provider, body, r.Header.Get(gatewaysig.Header)); err != nil {
			log.Printf("REJECT %s: %v", r.URL.Path, err)
			http.Error(w, "gateway signature invalid", http.StatusUnauthorized)
			return
		}
		log.Printf("ACCEPT %s [%s] %d bytes", r.URL.Path, provider, len(body))
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "ok\n")
	})

	const addr = ":8080"
	log.Printf("sample upstream listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
