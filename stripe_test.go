package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
	"time"
)

func stripeSign(secret, ts string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestStripeVerifier(t *testing.T) {
	const secret = "whsec_test"
	body := []byte(`{"id":"evt_1","amount":100.00}`)
	now := time.Unix(1700000000, 0)
	v := StripeVerifier{Secret: []byte(secret), ReplayWindow: 5 * time.Minute}

	hdr := func(ts, sig string) http.Header {
		h := http.Header{}
		h.Set("Stripe-Signature", "t="+ts+",v1="+sig)
		return h
	}

	valid := hdr("1700000000", stripeSign(secret, "1700000000", body))

	cases := []struct {
		name    string
		body    []byte
		h       http.Header
		now     time.Time
		wantErr bool
	}{
		{"valid", body, valid, now, false},
		{"tampered body", []byte(`{"id":"evt_1","amount":999.00}`), valid, now, true},
		{"stale timestamp", body, valid, now.Add(10 * time.Minute), true},
		{"fresh within window", body, valid, now.Add(4 * time.Minute), false},
		{"missing header", body, http.Header{}, now, true},
		{"wrong secret sig", body, hdr("1700000000", stripeSign("wrong", "1700000000", body)), now, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := v.Verify(c.body, c.h, c.now)
			if (err != nil) != c.wantErr {
				t.Fatalf("wantErr=%v got %v", c.wantErr, err)
			}
		})
	}
}
