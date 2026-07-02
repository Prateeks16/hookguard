package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"hookguard/web/internal/gwconfig"
	"hookguard/web/internal/store"
)

// runSeedConfig implements `console seed-config <path>`: reads a
// config.json-shaped file (the gateway's own format, DESIGN.md §8.2) and
// inserts each route as an endpoints row, so an operator can point a fresh
// Console at an existing gateway deployment's config instead of re-entering
// every route by hand.
func runSeedConfig(args []string) {
	if len(args) != 1 {
		log.Fatal("usage: console seed-config <path>")
	}
	path := args[0]

	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read %s: %v", path, err)
	}

	endpoints, err := gwconfig.Import(data)
	if err != nil {
		log.Fatalf("parse %s: %v", path, err)
	}

	cfg := loadConfig()
	st, err := store.Open(filepath.Join(cfg.DataDir, "console.db"))
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().UnixMilli()
	created := 0
	for _, e := range endpoints {
		if existing, err := st.GetEndpointByPath(e.Path); err == nil {
			log.Printf("skip %s: already exists (id %d)", e.Path, existing.ID)
			continue
		}
		e.CreatedAt, e.UpdatedAt = now, now
		if _, err := st.CreateEndpoint(e); err != nil {
			log.Fatalf("insert %s: %v", e.Path, err)
		}
		created++
	}
	log.Printf("seeded %d endpoint(s) from %s", created, path)
}
