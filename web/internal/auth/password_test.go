package auth

import "testing"

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !VerifyPassword("correct-horse-battery", hash) {
		t.Error("expected correct password to verify")
	}
	if VerifyPassword("wrong-password-here", hash) {
		t.Error("expected wrong password to fail verification")
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword("short"); err == nil {
		t.Error("expected error for short password")
	}
	if err := ValidatePassword("this-is-a-fine-password"); err != nil {
		t.Errorf("expected valid password to pass: %v", err)
	}
}
