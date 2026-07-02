// Command console serves the HookGuard web console: landing page, auth, and
// the dashboard shell + settings. It is a separate binary from the gateway,
// built from the nested hookguard/web module so its dependencies never enter
// the gateway's build.
package main

import (
	"log"
	"os"

	_ "hookguard/internal/gatewaysig"
)

const version = "0.1.0-m3"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "reset-password" {
		runResetPassword(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "seed-config" {
		runSeedConfig(os.Args[2:])
		return
	}

	cfg := loadConfig()
	srv, cleanup, err := buildServer(cfg)
	if err != nil {
		log.Fatalf("console: %v", err)
	}
	defer cleanup()

	log.Printf("console listening on %s", cfg.Addr)
	log.Fatal(srv.ListenAndServe())
}
