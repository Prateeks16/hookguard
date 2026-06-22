package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"testing"
	"time"
)

func shopifySign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func TestShopifyVerifier(t *testing.T) {
	const secret = "shopsecret"
	v := ShopifyVerifier{Secret: []byte(secret)}
	body := []byte(`{"id":820982911946154508,"financial_status":"paid"}`)

	hdr := func(sig string) http.Header {
		h := http.Header{}
		h.Set("X-Shopify-Hmac-SHA256", sig)
		return h
	}

	valid := hdr(shopifySign(secret, body))

	cases := []struct {
		name    string
		body    []byte
		h       http.Header
		wantErr bool
	}{
		{"valid base64", body, valid, false},
		{"tampered body", []byte(`{"id":1,"financial_status":"refunded"}`), valid, true},
		{"missing header", body, http.Header{}, true},
		{"hex not base64", body, hdr("616263"), true},
		{"wrong secret", body, hdr(shopifySign("wrong", body)), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := v.Verify(c.body, c.h, time.Time{}); (err != nil) != c.wantErr {
				t.Fatalf("wantErr=%v got %v", c.wantErr, err)
			}
		})
	}
}
