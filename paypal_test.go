package main

// There is no official PayPal Go library, so the differential-harness
// approach used for Stripe/GitHub (diff_test.go) does not apply here. These
// tests instead cover what's possible without one: the RSA-SHA256 message
// verification in isolation (a generated keypair signing exactly as PayPal
// documents), the SSRF host-pin in isolation, and that an untrusted
// certificate fails chain validation. The full pipeline — a live fetch from a
// real PayPal cert URL through chain validation against a real PayPal-issued
// certificate — needs a PayPal sandbox account and webhook simulator capture;
// that is documented as a manual step in docs/REPORT.md, not exercised here.

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func genRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

// signPaypal reproduces PayPal's signing side: SHA256 over
// "transmissionId|transmissionTime|webhookId|crc32(body)", RSA-PKCS1v15
// signed, base64-encoded — exactly what verifyPaypalSignature checks.
func signPaypal(t *testing.T, priv *rsa.PrivateKey, transmissionID, transmissionTime, webhookID string, body []byte) string {
	t.Helper()
	digest := sha256.Sum256([]byte(paypalSigMessage(transmissionID, transmissionTime, webhookID, body)))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return base64.StdEncoding.EncodeToString(sig)
}

// TestVerifyPaypalSignatureRoundtrip is the roundtrip genuineness test: sign
// exactly as PayPal does with a locally generated keypair, assert verify
// passes, then assert every kind of tamper (body, key, webhook id,
// transmission id, signature encoding) is rejected.
func TestVerifyPaypalSignatureRoundtrip(t *testing.T) {
	priv := genRSAKey(t)
	other := genRSAKey(t)

	const (
		transmissionID   = "tx-123"
		transmissionTime = "2026-06-24T00:00:00Z"
		webhookID        = "WH-ABC"
	)
	body := []byte(`{"id":"WH-EVT-1","event_type":"PAYMENT.SALE.COMPLETED"}`)
	sig := signPaypal(t, priv, transmissionID, transmissionTime, webhookID, body)

	if err := verifyPaypalSignature(&priv.PublicKey, transmissionID, transmissionTime, webhookID, body, sig); err != nil {
		t.Fatalf("valid signature should verify: %v", err)
	}

	cases := []struct {
		name string
		pub  *rsa.PublicKey
		tID  string
		tT   string
		wID  string
		body []byte
		sig  string
	}{
		{"tampered body", &priv.PublicKey, transmissionID, transmissionTime, webhookID,
			[]byte(`{"id":"WH-EVT-1","event_type":"PAYMENT.SALE.REFUNDED"}`), sig},
		{"wrong key", &other.PublicKey, transmissionID, transmissionTime, webhookID, body, sig},
		{"wrong webhook id", &priv.PublicKey, transmissionID, transmissionTime, "WH-OTHER", body, sig},
		{"wrong transmission id", &priv.PublicKey, "tx-456", transmissionTime, webhookID, body, sig},
		{"malformed sig encoding", &priv.PublicKey, transmissionID, transmissionTime, webhookID, body, "not-base64!!"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := verifyPaypalSignature(c.pub, c.tID, c.tT, c.wID, c.body, c.sig); err == nil {
				t.Fatal("expected verification to fail")
			}
		})
	}
}

// TestCheckPaypalCertHost proves the SSRF guard rejects any cert URL whose
// host is not PayPal-owned, before any network fetch would happen —
// paypal-cert-url is attacker-controlled input, so this is the single most
// important check in the whole provider.
func TestCheckPaypalCertHost(t *testing.T) {
	cases := []struct {
		url     string
		wantErr bool
	}{
		{"https://api.paypal.com/v1/notifications/certs/CERT-1", false},
		{"https://api.sandbox.paypal.com/v1/notifications/certs/CERT-1", false},
		{"https://PayPal.com/cert.pem", false},
		{"http://api.paypal.com/cert.pem", true},       // not https
		{"https://evil.com/cert.pem", true},            // not paypal at all
		{"https://paypal.com.evil.com/cert.pem", true}, // suffix-confusion attempt
		{"https://notpaypal.com/cert.pem", true},       // substring, not suffix
		{"not-a-url", true},
	}
	for _, c := range cases {
		t.Run(c.url, func(t *testing.T) {
			err := checkPaypalCertHost(c.url)
			if (err != nil) != c.wantErr {
				t.Fatalf("checkPaypalCertHost(%q): wantErr=%v got %v", c.url, c.wantErr, err)
			}
		})
	}
}

// TestParsePaypalCertChainRejectsUntrusted proves the chain-validation step
// actually rejects a certificate that does not chain to a trusted root. A
// self-signed certificate stands in for an attacker-supplied one — a real
// PayPal-issued certificate isn't available outside a live sandbox capture.
func TestParsePaypalCertChainRejectsUntrusted(t *testing.T) {
	priv := genRSAKey(t)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "not-paypal.example"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	if _, err := parsePaypalCertChain(certPEM); err == nil {
		t.Fatal("self-signed certificate should not chain to a trusted root")
	}
}

// TestPaypalCertCache proves the cache serves a fresh entry and reports a
// miss once it has aged past the TTL, with no network call involved.
func TestPaypalCertCache(t *testing.T) {
	c := newPaypalCertCache()
	cert := &x509.Certificate{}
	c.set("https://api.paypal.com/cert", cert)

	if got, ok := c.get("https://api.paypal.com/cert"); !ok || got != cert {
		t.Fatal("expected cache hit on freshly-set entry")
	}

	c.certs["https://api.paypal.com/cert"] = paypalCachedCert{cert: cert, fetchedAt: time.Now().Add(-2 * paypalCertTTL)}
	if _, ok := c.get("https://api.paypal.com/cert"); ok {
		t.Fatal("expected cache miss once TTL has elapsed")
	}
}
