package main

import "os"

type consoleConfig struct {
	Addr           string
	DataDir        string
	AllowSignup    bool
	InternalSecret []byte
}

// loadConfig reads INTERNAL_SECRET the same way the root gateway does
// (main.go: os.Getenv("INTERNAL_SECRET")) — the same shared secret, since
// it authenticates the same Gateway signature on both sides of the ingest
// POST. Unset means /api/v1/ingest can never verify a request, which is a
// safe default (no silent accept-everything mode), not a startup failure:
// the Console is still useful as an auth/endpoints admin UI without ingest.
func loadConfig() consoleConfig {
	port := os.Getenv("CONSOLE_PORT")
	if port == "" {
		port = "7000"
	}
	dataDir := os.Getenv("CONSOLE_DATA_DIR")
	if dataDir == "" {
		dataDir = "."
	}
	return consoleConfig{
		Addr:           ":" + port,
		DataDir:        dataDir,
		AllowSignup:    os.Getenv("CONSOLE_ALLOW_SIGNUP") == "true",
		InternalSecret: []byte(os.Getenv("INTERNAL_SECRET")),
	}
}
