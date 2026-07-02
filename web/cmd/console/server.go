package main

import (
	"net/http"
	"path/filepath"

	"hookguard/web/internal/server"
	"hookguard/web/internal/store"
	"hookguard/web/ui"
)

func buildServer(cfg consoleConfig) (*http.Server, func(), error) {
	st, err := store.Open(filepath.Join(cfg.DataDir, "console.db"))
	if err != nil {
		return nil, nil, err
	}

	srv, err := server.New(st, ui.TemplatesFS, cfg.AllowSignup, version)
	if err != nil {
		st.Close()
		return nil, nil, err
	}

	httpSrv := &http.Server{
		Addr:    cfg.Addr,
		Handler: srv.Router(ui.StaticFS),
	}
	return httpSrv, func() { st.Close() }, nil
}
