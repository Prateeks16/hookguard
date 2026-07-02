package main

import "os"

type consoleConfig struct {
	Addr        string
	DataDir     string
	AllowSignup bool
}

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
		Addr:        ":" + port,
		DataDir:     dataDir,
		AllowSignup: os.Getenv("CONSOLE_ALLOW_SIGNUP") == "true",
	}
}
