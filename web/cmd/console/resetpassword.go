package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"hookguard/web/internal/auth"
	"hookguard/web/internal/store"
)

// resetTokenTTL bounds how long a printed reset URL stays valid — DESIGN.md
// §5.2's operator-run reset flow, no SMTP involved.
const resetTokenTTL = time.Hour

// runResetPassword implements `console reset-password <email>`: mints a
// one-time token, stores its hash under settings("pwreset:<user_id>") with
// an expiry, and prints the URL an operator hands to the user out-of-band.
func runResetPassword(args []string) {
	if len(args) != 1 {
		log.Fatal("usage: console reset-password <email>")
	}
	email := args[0]

	cfg := loadConfig()
	st, err := store.Open(filepath.Join(cfg.DataDir, "console.db"))
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	user, err := st.GetUserByEmail(email)
	if err != nil {
		log.Fatalf("no such user: %s", email)
	}

	token, hash, err := auth.NewResetToken()
	if err != nil {
		log.Fatalf("generate token: %v", err)
	}

	expiresAt := time.Now().Add(resetTokenTTL).UnixMilli()
	settingKey := fmt.Sprintf("pwreset:%d", user.ID)
	settingVal := fmt.Sprintf("%x:%d", hash, expiresAt)
	if err := st.SetSetting(settingKey, settingVal); err != nil {
		log.Fatalf("store reset token: %v", err)
	}

	base := os.Getenv("CONSOLE_PUBLIC_URL")
	if base == "" {
		base = "http://localhost" + loadConfig().Addr
	}
	fmt.Printf("%s/reset-password?token=%s&uid=%d\n", base, token, user.ID)
}
