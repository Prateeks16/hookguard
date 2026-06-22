# HookGuard — Build Guide (from scratch)

This document walks through how HookGuard was built, step by step, as if you were
writing it yourself from an empty directory. Every file and every non-obvious
line is explained, along with the *why* behind it — the security reasoning that
makes each decision the correct one.

It covers the work through Day 3 (scaffold → Stripe verifier → GitHub verifier).
Read it top to bottom; each step builds on the last.

---

## 0. What we are building and why

A **webhook** is an HTTP POST that a provider (Stripe, GitHub, Shopify, …) sends
to *your* server the moment something happens — a payment succeeds, code is
pushed, an order is placed. The problem: your webhook URL is public, so anyone on
the internet can POST to it. Without a check, your server cannot tell a real
Stripe event from a forged one an attacker `curl`ed in. That forged event might
unlock a subscription, ship goods, or trigger a deploy.

The defence every provider uses is a **cryptographic signature**. The provider
and you share a secret key. The provider computes a signature over the request
body using that key and sends it in a header. You recompute the signature with
your copy of the key and compare. If they match, the body is authentic and
untampered. If not, you drop the request.

The catch: **every provider does this differently** — different header names,
different algorithms, different encodings, different rules. Verifying six
providers means six bespoke implementations, and getting any of them subtly wrong
silently disables the security.

**HookGuard** is a small gateway that sits in front of your application. It
receives the webhook, verifies the signature using the right provider's rules,
and forwards only verified traffic to your app. Your app then trusts one thing
instead of implementing six.

Design constraints we committed to:

- **Go** — fast, single static binary, great standard library for crypto and HTTP.
- **Zero external dependencies** — the binary uses only Go's standard library.
  This is a deliberate selling point: nothing to audit, nothing to break.
- **Never parse the body** — explained in Step 1; this is the single most
  important rule in the whole project.

---

## 1. Project setup

Create an empty directory and initialise a Go module:

```sh
mkdir hookguard && cd hookguard
go mod init hookguard
```

`go mod init hookguard` writes a `go.mod` file:

```
module hookguard

go 1.26.3
```

`go.mod` declares the module's name (`hookguard`, used for internal imports) and
the Go version. Crucially, it has **no `require` block** — that is how you can
tell at a glance that we depend on nothing but the standard library. Keep it that
way; if `require` ever appears, a dependency sneaked in.

---

## 2. The raw-body rule (the most important concept)

Here is the trap that breaks the majority of real-world webhook integrations.

A signature is computed over the **exact bytes** of the request body. Change a
single byte — a space, the order of two JSON keys, `100.00` becoming `100.0` —
and the signature no longer matches, because cryptographic hashes have the
"avalanche effect": one bit different in the input flips roughly half the bits of
the output.

Modern web frameworks (Express, Django, Spring, …) helpfully read the incoming
JSON, parse it into an object, and hand you that object. If you then re-serialize
that object back into a string to verify the signature, the bytes you produce are
**not** the bytes the provider signed. The whitespace differs, the key order may
differ, a float may be reformatted. The signature check fails even though nothing
malicious happened.

**The rule: capture the raw body bytes once, verify against those exact bytes,
and forward those exact bytes. Never parse, never re-serialize.**

HookGuard is built around honouring this rule.

---

## 3. Configuration

We need to know, for each inbound path, which provider it is, where to forward
verified traffic, and which environment variable holds the secret. We store that
in a JSON file (JSON, not YAML, because parsing JSON is in the standard library —
YAML would add a dependency and break our zero-dependency rule).

`config.json`:

```json
{
  "routes": [
    {
      "path": "/hook/stripe",
      "provider": "stripe",
      "upstream": "http://localhost:8080/stripe",
      "replay_window": "5m",
      "secret_env": "STRIPE_SECRET"
    },
    {
      "path": "/hook/github",
      "provider": "github",
      "upstream": "http://localhost:8080/github",
      "secret_env": "GITHUB_SECRET"
    }
  ]
}
```

Notice the **secret itself is not in the file** — only the *name* of the
environment variable that holds it (`secret_env`). Secrets in a committed config
file end up in git history forever. Secrets in environment variables do not. This
is standard "twelve-factor" practice.

`config.go` loads it:

```go
package main

import (
	"encoding/json"
	"os"
)

// Route binds an inbound path to one Provider verifier, an Upstream URL, a
// replay window, and the env var naming that Provider's secret.
type Route struct {
	Path         string `json:"path"`
	Provider     string `json:"provider"`
	Upstream     string `json:"upstream"`
	ReplayWindow string `json:"replay_window"` // parsed later (time.ParseDuration)
	SecretEnv    string `json:"secret_env"`
}

type Config struct {
	Routes []Route `json:"routes"`
}

func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
```

How this works:

- The `` `json:"path"` `` parts are **struct tags**. They tell `encoding/json`
  which JSON key maps to which Go field. Without them Go would look for a JSON key
  named `Path` (capitalised), which does not exist.
- `os.ReadFile` reads the whole file into a byte slice.
- `json.Unmarshal(b, &c)` parses those bytes into our `Config` struct. We pass
  `&c` (a pointer) so the function can fill in our variable.
- Every step returns an `error` we check immediately. In Go you handle errors
  where they happen; you do not let them propagate silently.

`ReplayWindow` is kept as a string here and parsed into a real duration later,
because only some providers use it.

---

## 4. The gateway: receive → (verify) → forward

Now the core. `main.go` starts an HTTP server, registers one handler per
configured route, and forwards verified bytes upstream.

```go
package main

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"time"
)

func main() {
	cfg, err := LoadConfig("config.json")
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	mux := http.NewServeMux()
	for _, r := range cfg.Routes {
		v, err := buildVerifier(r)
		if err != nil {
			log.Fatalf("route %s: %v", r.Path, err)
		}
		mux.HandleFunc(r.Path, makeHandler(r, v, client))
		log.Printf("route %s [%s] -> %s", r.Path, r.Provider, r.Upstream)
	}

	log.Println("hookguard listening on :9000")
	log.Fatal(http.ListenAndServe(":9000", mux))
}
```

Step by step:

- `LoadConfig` reads the routes. If the file is missing or malformed we
  `log.Fatalf` — there is no sensible way to run without config, so we stop
  immediately rather than limping on.
- `http.Client{Timeout: 30s}` is the client we use to forward requests upstream.
  Setting a timeout matters: without one, a hung upstream would tie up the
  request forever.
- `http.NewServeMux()` is Go's request router. For each route we build its
  verifier (Step 5) and register a handler at its path.
- `buildVerifier` failing is fatal — if a route names a provider we have not
  implemented, or its secret env var is unset, we refuse to start. It is far
  better to crash at boot with a clear message than to start up and silently fail
  to protect a route.
- `ListenAndServe(":9000", mux)` blocks and serves forever on port 9000.

The handler is where the raw-body rule lives:

```go
// makeHandler buffers the raw request body, verifies the Provider signature, and
// forwards the unaltered bytes upstream. The body stays the exact bytes received
// — never parsed or re-serialized — so the HMAC computed here matches the bytes
// the upstream sees.
func makeHandler(r Route, v Verifier, client *http.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		if err := v.Verify(body, req.Header, time.Now()); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		forward(w, r, body, client)
	}
}
```

This is the heart of HookGuard, and it is deliberately tiny:

1. `io.ReadAll(req.Body)` reads the **raw bytes** into `body`. We never call a
   JSON parser. `body` is the untouched payload.
2. `v.Verify(body, req.Header, time.Now())` checks the signature against those
   exact bytes (Step 5). On failure we return `401 Unauthorized` and stop — the
   forged or corrupted request never reaches your app.
3. Only on success do we `forward` the **same** `body` bytes upstream.

`makeHandler` is a function that *returns* a handler. We do this so each route
can close over its own `Route` and `Verifier`. This pattern — a function that
builds and returns a function — is how you give each handler its own
configuration without globals.

Forwarding:

```go
func forward(w http.ResponseWriter, r Route, body []byte, client *http.Client) {
	out, err := http.NewRequest(http.MethodPost, r.Upstream, bytes.NewReader(body))
	if err != nil {
		http.Error(w, "build upstream request", http.StatusBadGateway)
		return
	}
	out.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(out)
	if err != nil {
		http.Error(w, "upstream unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	rb, _ := io.ReadAll(resp.Body)
	w.WriteHeader(resp.StatusCode)
	w.Write(rb)
}
```

- We build a new POST to the upstream URL whose body is `bytes.NewReader(body)` —
  the same verified bytes, unchanged.
- `client.Do(out)` sends it. If the upstream is unreachable we return
  `502 Bad Gateway`.
- `defer resp.Body.Close()` guarantees we close the response body when the
  function returns, even on an early exit — `defer` runs the call at function
  end. Forgetting this leaks connections.
- We copy the upstream's status code and body back to the original caller, so the
  provider sees the real result.

---

## 5. The Verifier seam

We have three providers to support and more in future. Each verifies differently,
but the gateway should not care *how* — it should just ask "is this valid?" and
get yes or no. That is exactly what an **interface** gives us.

`verifier.go`:

```go
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

// Verifier authenticates a raw webhook body against one Provider's signature
// shape. Verify returns nil iff the signature is valid and — where the shape
// carries a timestamp — fresh within the replay window. rawBody must be the
// exact bytes received; never parse or re-serialize it before verifying.
type Verifier interface {
	Verify(rawBody []byte, h http.Header, now time.Time) error
}
```

This interface is small but it hides a lot: header parsing, the HMAC
computation, encoding (hex vs base64), constant-time comparison, and replay
checks all live *behind* it. A small interface in front of substantial behaviour
is what makes a module worth having — the gateway depends on three lines, not on
the messy details. Adding a new provider means writing a new type that satisfies
this interface; the gateway code does not change at all.

Returning `error` (rather than a `bool`) is idiomatic Go and lets each verifier
explain *why* it rejected something, which is invaluable when debugging.

The factory turns a configured route into a concrete verifier:

```go
// buildVerifier constructs the Verifier for a Route, reading its secret from the
// environment. Fails fast on missing secret, bad replay window, or a provider
// with no implementation yet.
func buildVerifier(r Route) (Verifier, error) {
	secret := os.Getenv(r.SecretEnv)
	if secret == "" {
		return nil, fmt.Errorf("missing secret env %s", r.SecretEnv)
	}
	window, err := parseWindow(r.ReplayWindow)
	if err != nil {
		return nil, fmt.Errorf("replay_window: %w", err)
	}

	switch r.Provider {
	case "stripe":
		return StripeVerifier{Secret: []byte(secret), ReplayWindow: window}, nil
	case "github":
		return GitHubVerifier{Secret: []byte(secret)}, nil
	default:
		return nil, fmt.Errorf("unknown provider %q", r.Provider)
	}
}

func parseWindow(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}
```

- `os.Getenv(r.SecretEnv)` reads the secret from the environment by the name the
  config gave. Empty means the operator forgot to set it — fatal.
- `parseWindow` turns `"5m"` into a real `time.Duration`. An empty string means
  the provider has no replay concept, so we return `0` (meaning "no window").
- The `switch` is the one place that maps a provider name to an implementation.
  An unknown name is an error, so a typo in config crashes at boot, not at the
  worst moment in production.

---

## 6. Stripe verifier

Stripe's rules:

- Header `Stripe-Signature` looks like `t=1700000000,v1=abc123...` — a Unix
  timestamp `t` and one or more signatures `v1`.
- The signed message is the timestamp, a literal dot, then the raw body:
  `"<t>.<body>"`.
- Algorithm: HMAC-SHA256, output as hex.
- A timestamp far from "now" is rejected to stop **replay attacks** (an attacker
  re-sending a captured-but-valid request).

`stripe.go`:

```go
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// StripeVerifier implements the Stripe signature shape: a Stripe-Signature
// header of the form "t=<unix>,v1=<hex>[,v1=<hex>...]", where the HMAC-SHA256 is
// computed over "<t>.<rawBody>". A timestamp outside ReplayWindow is rejected
// even when the HMAC matches.
type StripeVerifier struct {
	Secret       []byte
	ReplayWindow time.Duration
}

func (v StripeVerifier) Verify(rawBody []byte, h http.Header, now time.Time) error {
	header := h.Get("Stripe-Signature")
	if header == "" {
		return errors.New("missing Stripe-Signature header")
	}
	ts, sigs := parseStripeSig(header)
	if ts == "" || len(sigs) == 0 {
		return errors.New("malformed Stripe-Signature header")
	}

	tsInt, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return errors.New("invalid timestamp")
	}
	if v.ReplayWindow > 0 {
		delta := now.Sub(time.Unix(tsInt, 0))
		if delta < 0 {
			delta = -delta
		}
		if delta > v.ReplayWindow {
			return errors.New("timestamp outside replay window")
		}
	}

	mac := hmac.New(sha256.New, v.Secret)
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(rawBody)
	expected := mac.Sum(nil)

	for _, s := range sigs {
		got, err := hex.DecodeString(s)
		if err != nil {
			continue
		}
		if hmac.Equal(got, expected) {
			return nil
		}
	}
	return errors.New("no matching signature")
}
```

Reading the body of `Verify`:

1. **Get the header.** Missing → reject.
2. **Parse it** into the timestamp and the list of `v1` signatures (helper below).
3. **Replay check.** Convert the timestamp to a time, take the absolute
   difference from `now`, and reject if it exceeds the window. We use the
   *absolute* difference so both a stale request (too old) and an
   impossibly-future one (clock games) are rejected. This runs *before* the HMAC
   check; an old-but-valid replayed request must still be refused.
4. **Compute the HMAC.** `hmac.New(sha256.New, secret)` creates an HMAC keyed on
   the secret. We write the timestamp, then `"."`, then the raw body — exactly
   Stripe's `"<t>.<body>"`. `mac.Sum(nil)` finalises and returns the digest bytes.
5. **Compare in constant time.** For each `v1`, hex-decode it and compare with
   `hmac.Equal`. (Why `hmac.Equal` and not `==` — see the box below.) Stripe may
   send several `v1` values during secret rotation, so we accept if *any* match.

The parser:

```go
func parseStripeSig(header string) (ts string, sigs []string) {
	for _, part := range strings.Split(header, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			ts = kv[1]
		case "v1":
			sigs = append(sigs, kv[1])
		}
	}
	return ts, sigs
}
```

Split the header on commas, split each piece once on `=` into key/value, and
collect `t` and every `v1`. `SplitN(..., 2)` splits into at most 2 parts so a `=`
inside the value is preserved.

> **Why constant-time comparison (`hmac.Equal`)?**
>
> A normal string compare (`==`) returns the moment it finds the first differing
> byte. That means it returns slightly faster when the first byte is wrong than
> when the first ten bytes are right. An attacker who can measure response times
> precisely can send many forged signatures and use those tiny timing
> differences to discover the correct signature one byte at a time — a **timing
> attack**. `hmac.Equal` always compares the whole length regardless of where a
> difference is, so the time taken reveals nothing. Always compare secrets and
> signatures with a constant-time function.

---

## 7. GitHub verifier

GitHub's rules are simpler:

- Header `X-Hub-Signature-256` looks like `sha256=abc123...` — the algorithm name,
  an `=`, then the hex signature.
- The signed message is just the raw body (no timestamp).
- Algorithm: HMAC-SHA256, hex.
- No replay window (there is no timestamp to check).

`github.go`:

```go
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
)

// GitHubVerifier implements the GitHub signature shape: an X-Hub-Signature-256
// header of the form "sha256=<hex>", where the HMAC-SHA256 is computed over the
// raw body bytes. GitHub carries no timestamp, so there is no replay window.
type GitHubVerifier struct {
	Secret []byte
}

func (v GitHubVerifier) Verify(rawBody []byte, h http.Header, _ time.Time) error {
	header := h.Get("X-Hub-Signature-256")
	if header == "" {
		return errors.New("missing X-Hub-Signature-256 header")
	}
	hexSig, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return errors.New("malformed X-Hub-Signature-256 header")
	}
	got, err := hex.DecodeString(hexSig)
	if err != nil {
		return errors.New("invalid signature encoding")
	}

	mac := hmac.New(sha256.New, v.Secret)
	mac.Write(rawBody)
	expected := mac.Sum(nil)

	if !hmac.Equal(got, expected) {
		return errors.New("signature mismatch")
	}
	return nil
}
```

Notes:

- The third parameter is `_ time.Time` — we must accept it to satisfy the
  `Verifier` interface, but GitHub has no timestamp so we ignore it. Naming an
  unused parameter `_` says "intentionally unused".
- `strings.CutPrefix(header, "sha256=")` strips the `sha256=` prefix and tells us
  (via `ok`) whether the prefix was actually there. No prefix → malformed.
- The signed message is just `rawBody` — note we write *only* the body, no
  timestamp, unlike Stripe. This difference between providers is the whole reason
  the `Verifier` interface exists.
- Same constant-time `hmac.Equal` comparison.

> **The GitHub UTF-8 trap.** GitHub commit messages often contain emoji and other
> multi-byte UTF-8 characters. The HMAC is over the raw UTF-8 *bytes*. If your
> code reads the stream as ASCII or re-encodes it, the bytes change and the check
> fails. Because we read raw bytes with `io.ReadAll` and never decode them, we
> are immune — and our test proves it with an emoji payload.

---

## 8. Testing: prove each rule

Two kinds of test guard the project.

### 8a. The raw-body invariant (the gateway)

`main_test.go` proves bytes pass through untouched. The payload is deliberately
hostile to naive parsing — odd spacing, unsorted keys, a trailing-zero float, an
emoji — exactly the things a parse-and-reserialize would mangle:

```go
func TestRawBodyPassthrough(t *testing.T) {
	payload := []byte("{ \"b\":1,\"a\":  100.00, \"msg\":\"héllo 🚀\" }")

	var got []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	route := Route{Path: "/hook/test", Upstream: upstream.URL}
	gw := httptest.NewServer(makeHandler(route, passVerifier{}, &http.Client{Timeout: 5 * time.Second}))
	defer gw.Close()

	resp, err := http.Post(gw.URL, "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()

	if !bytes.Equal(got, payload) {
		t.Fatalf("byte mismatch:\n sent: %q\n recv: %q", payload, got)
	}
}
```

`httptest.NewServer` spins up a real (in-process) HTTP server. We stand up a fake
upstream that records what bytes it received, point a gateway handler at it, POST
the hostile payload, and assert the upstream received *exactly* what we sent. If
anyone ever adds a JSON parse to the forward path, this test goes red. The
`passVerifier{}` is a tiny stub that accepts everything, so this test isolates the
forwarding path from signature logic.

### 8b. Verifier behaviour (table tests)

`stripe_test.go` and `github_test.go` use **table-driven tests** — a list of
cases, each run as a subtest. The Stripe table:

```go
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
```

Each case states an input and whether we expect an error (`wantErr`). This proves
the four properties that matter: a valid signature passes, a tampered body fails,
a stale timestamp fails, and a signature made with the wrong secret fails. The
test helper `stripeSign` reproduces Stripe's algorithm so we can generate valid
and invalid signatures on demand. GitHub's table is the same shape and includes
the emoji-payload case to prove the UTF-8 bytes survive.

Run everything:

```sh
go vet ./...    # static checks
go test ./...   # all tests
go build .      # it compiles to a binary
```

All three must be clean before moving on. Strong, specific tests mean you can
keep building without fear of silently breaking what already works.

---

## 9. Shopify verifier (the base64 twist)

Shopify's rules are almost identical to GitHub's, with one twist that catches
people out:

- Header `X-Shopify-Hmac-SHA256` holds the signature directly (no `name=` prefix).
- The signed message is the raw body.
- Algorithm: HMAC-SHA256 — but the output is **base64**-encoded, not hex.
- No timestamp, so no replay window.

`shopify.go`:

```go
func (v ShopifyVerifier) Verify(rawBody []byte, h http.Header, _ time.Time) error {
	header := h.Get("X-Shopify-Hmac-SHA256")
	if header == "" {
		return errors.New("missing X-Shopify-Hmac-SHA256 header")
	}
	got, err := base64.StdEncoding.DecodeString(header)
	if err != nil {
		return errors.New("invalid signature encoding")
	}

	mac := hmac.New(sha256.New, v.Secret)
	mac.Write(rawBody)
	expected := mac.Sum(nil)

	if !hmac.Equal(got, expected) {
		return errors.New("signature mismatch")
	}
	return nil
}
```

The only difference from GitHub is `base64.StdEncoding.DecodeString` instead of
`hex.DecodeString`. The HMAC itself is identical — same algorithm, same secret,
same raw body. This is exactly why the `Verifier` interface earns its place:
three providers, three tiny differences (timestamped-concat vs prefixed-hex vs
base64), one unchanging gateway. Adding Shopify was a new file plus one line in
the factory `switch`; nothing else moved.

> **Why decode the signature rather than encode our digest?** We could instead
> base64-encode `expected` and compare strings. Decoding the incoming signature
> to raw bytes and comparing with `hmac.Equal` keeps the comparison constant-time
> and avoids any encoding-case mismatch (upper/lower hex, base64 padding quirks).
> Always compare the decoded bytes.

Register it in the factory (`verifier.go`):

```go
case "shopify":
	return ShopifyVerifier{Secret: []byte(secret)}, nil
```

---

## 10. The Gateway signature: the internal trust boundary

So far the gateway verifies the provider and forwards the bytes. But ask: how
does the *upstream* know the request came from the gateway? If the upstream sits
on a network an attacker can reach, the attacker could POST straight to it,
skipping the gateway entirely, and the upstream would have no way to tell.

A naive fix is to have the gateway add a header like `X-Webhook-Verified: true`
and have the upstream trust it. **This is worthless** — anyone can set that header
on a forged request. It is the exact anti-pattern that makes the whole gateway
pointless.

The real fix: the gateway **re-signs** every verified request with a *single*
internal secret shared only between gateway and upstream. The upstream verifies
that one signature. An attacker without the internal secret cannot forge it.

This is the heart of HookGuard's value: the upstream stops implementing N bespoke
provider verifications and implements **one** check. We even bind the verified
provider name into the signature, so the upstream learns *which* provider was
verified and an attacker cannot relabel a payload (say, pass a forged Shopify
order off as a Stripe charge) without breaking the signature.

The signature lives in its own package, `internal/gatewaysig`, because it has two
users — the gateway (which signs) and the sample upstream (which verifies). Shared
code with two real callers belongs in one place, tested once.

`internal/gatewaysig/gatewaysig.go`:

```go
const (
	Header         = "X-HookGuard-Signature"
	ProviderHeader = "X-HookGuard-Provider"
)

// Sign returns the hex HMAC-SHA256 over "<provider>.<body>" keyed by secret.
func Sign(secret []byte, provider string, body []byte) string {
	return hex.EncodeToString(mac(secret, provider, body))
}

// Verify reports whether sigHex matches Sign(secret, provider, body), comparing
// in constant time.
func Verify(secret []byte, provider string, body []byte, sigHex string) error {
	got, err := hex.DecodeString(sigHex)
	if err != nil {
		return errors.New("invalid gateway signature encoding")
	}
	if !hmac.Equal(got, mac(secret, provider, body)) {
		return errors.New("gateway signature mismatch")
	}
	return nil
}

func mac(secret []byte, provider string, body []byte) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(provider))
	m.Write([]byte("."))
	m.Write(body)
	return m.Sum(nil)
}
```

`Sign` and `Verify` share the `mac` helper so the signing and checking logic can
never drift apart — a classic bug source when the two halves live in different
places. The message is `"<provider>.<body>"` (the same shape as Stripe's
`"<t>.<body>"`), which is what binds the provider name to the payload.

---

## 11. Signing on the way out, and the sample upstream

The gateway attaches the signature in `forward` (`main.go`):

```go
out.Header.Set("Content-Type", "application/json")
// Attach the Gateway signature: one internal HMAC the upstream verifies
// instead of re-running the provider's verification.
out.Header.Set(gatewaysig.ProviderHeader, r.Provider)
out.Header.Set(gatewaysig.Header, gatewaysig.Sign(internalSecret, r.Provider, body))
```

`internalSecret` is read once at startup from `INTERNAL_SECRET` and passed down;
the gateway refuses to start without it. The signature covers the **same** `body`
bytes we forward, so the upstream's check is over identical bytes.

The sample upstream (`cmd/upstream/main.go`) shows the entire contract from the
app's side — it is deliberately tiny, because that is the whole point:

```go
http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	provider := r.Header.Get(gatewaysig.ProviderHeader)
	if err := gatewaysig.Verify(secret, provider, body, r.Header.Get(gatewaysig.Header)); err != nil {
		http.Error(w, "gateway signature invalid", http.StatusUnauthorized)
		return
	}
	// ... handle the verified webhook ...
})
```

A real upstream reimplements this one check in whatever language it is written
in — that is the entire integration burden, replacing six provider-specific
verifiers. Note this is a *separate binary* (`cmd/upstream/`); it represents the
customer's app, not part of the gateway.

> **The signature is necessary but not sufficient.** A leaked internal secret, or
> an attacker who can reach the upstream *and* knows the secret, defeats it. So in
> deployment (Day 6) we *also* isolate the upstream on an internal Docker network
> with no public port — defence in depth. The signature defends against a flat or
> misconfigured network; the network isolation defends against a stolen-secret
> replay. Neither alone is enough; together they are solid.

---

## 12. Testing the trust boundary

Two tests cover this. First, the signature package round-trips and rejects every
kind of tampering (`internal/gatewaysig/gatewaysig_test.go`): a valid signature
passes; a *different provider name*, a tampered body, a wrong secret, and a
malformed encoding all fail. The different-provider case is what proves the
provenance binding works.

Second, an end-to-end test (`main_test.go`, `TestGatewaySignatureEndToEnd`)
stands up a fake upstream that verifies the gateway signature, sends a genuinely
Stripe-signed request through a real gateway handler, and asserts the upstream
accepts it. Then it simulates an attacker POSTing **directly** to the upstream
with a forged gateway signature and asserts the upstream returns `401`. That is
the trust boundary demonstrated in code: in through the gateway works, around the
gateway does not.

---

## 13. Where we are, and what comes next

What exists after Day 4:

- A gateway that buffers the raw body, verifies the provider by path, attaches a
  Gateway signature, and forwards verified bytes unchanged.
- Three provider verifiers (Stripe, GitHub, Shopify) behind one `Verifier`
  interface — covering timestamped-concat, prefixed-hex, and base64 signature
  shapes.
- A Gateway signature that collapses N provider verifications into one check at
  the upstream, with the verified provider bound into the signature.
- A sample upstream demonstrating the app-side contract.
- Tests proving the raw-body invariant, each verifier's behaviour, the signature
  round-trip, and the end-to-end trust boundary.

Still to come:

- **Deployment** — a Docker setup where the upstream is isolated on an internal
  network and never exposed, so the only way in is through the gateway.

---

## 14. Differential harness (the correctness proof)

How do we *prove* our verifiers are correct, not just self-consistent? We compare
each one against an **independent oracle** — a separate implementation of the same
rule — across a matrix of payloads, and assert the two always reach the same
verdict. If our verifier and the oracle agree on every case (accept the same
things, reject the same things), we have strong evidence our implementation is
right.

**The oracle per provider:**

- **Stripe** → `stripe-go`, Stripe's own official Go library.
- **GitHub** → `go-github`, Google's official GitHub library (`ValidateSignature`).
- **Shopify** → no official Go library exists, so the oracle is a separate,
  independent re-implementation of the documented algorithm. This is weaker than
  a vendor library (we wrote both sides), and the report says so honestly.

**These dependencies are test-only.** They are imported only from `diff_test.go`,
never from any file that ships in the binary. So `go.mod` lists them, but the
gateway binary itself still imports nothing outside the standard library. You can
prove this:

```sh
go list -deps . | grep -E 'stripe|go-github'   # prints nothing
```

That command lists every package the gateway binary depends on; the oracle
libraries are absent. The "zero-dependency binary" claim holds.

**The matrix.** For each provider we test: a valid signature over plain JSON, a
valid signature over an emoji/UTF-8 body, a valid signature over awkward
whitespace and a trailing-zero float, a tampered body, and a wrong-secret
signature. Stripe additionally tests a stale timestamp. Each case states the
expected verdict; `logDiff` fails the test if our verifier and the oracle
disagree, or if either's verdict differs from what we expected:

```go
func logDiff(t *testing.T, provider, name string, ours, oracle, want bool) {
	t.Helper()
	switch {
	case ours != oracle:
		t.Errorf("[%s] %s: ours=%v oracle=%v — verdicts must match", provider, name, ours, oracle)
	case ours != want:
		t.Errorf("[%s] %s: verdict=%v want=%v", provider, name, ours, want)
	}
	t.Logf("%-8s %-22s ours=%-5v oracle=%-5v ...", provider, name, ours, oracle)
}
```

**A real finding the harness surfaced.** On first run, every *valid* Stripe case
disagreed: our verifier accepted, `stripe-go` rejected. Diagnosing the actual
error (rather than guessing) revealed it was not a signature failure at all —
`stripe-go`'s `ConstructEvent` verifies the signature and *then* checks that the
event's `api_version` matches the SDK's expected version, erroring if not. Our
test payloads had no `api_version`, so the SDK rejected them *after* the signature
had already verified.

This is an insight worth keeping: the official SDK **couples** two concerns —
signature verification and event deserialization/version compatibility. HookGuard
deliberately does only the first: pure signature verification, no assumptions
about the body's shape. To make the comparison fair (we are diffing *signature*
verdicts, not SDK version policy) we switch the oracle to
`ConstructEventWithOptions(..., {IgnoreAPIVersionMismatch: true})`, which isolates
the signature/timestamp check. After that, all cases agree.

**Result:** 14 cases across the three providers, every one in agreement
(ours == oracle == expected). Run it yourself:

```sh
go test -v -run Differential .
```

This is the project's headline evidence: HookGuard's verifiers reach the same
verdict as the providers' own libraries, including on the adversarial cases
(tampering, wrong secret, UTF-8, stale timestamps) where naive implementations
silently break.

---

## 15. Where we are, and what comes next

After Day 5, everything except deployment is built and proven:

- Three provider verifiers behind one `Verifier` interface, each unit-tested and
  each cross-checked against an independent oracle.
- A gateway that buffers the raw body, verifies, signs, and forwards.
- A Gateway signature collapsing N verifications into one upstream check.
- A differential harness proving the verifiers match the official libraries.

The last step is **deployment**: a Docker setup that runs the gateway and an
isolated upstream, with the upstream bound to an internal network and never
exposed publicly — so the signature check and the network boundary reinforce each
other.

---

## 16. Deployment: Docker, and the network half of the trust boundary

The Gateway signature stops an attacker who reaches the upstream but lacks the
internal secret. The deployment adds the second half: make the upstream
**unreachable** in the first place.

**The image.** A multi-stage `Dockerfile` builds a static binary in a full Go
image, then copies just that binary onto a tiny `distroless/static:nonroot`
base — no shell, no package manager, a non-root user, ~2MB plus our ~9MB binary:

```dockerfile
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/hookguard .

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=build /out/hookguard /hookguard
COPY config.json /config.json
EXPOSE 9000
USER nonroot:nonroot
ENTRYPOINT ["/hookguard"]
```

`CGO_ENABLED=0` forces a fully static binary (no libc dependency) so it runs on
the minimal base. `-ldflags="-s -w"` strips debug info to shrink it. The sample
upstream has an equivalent `Dockerfile.upstream`.

**The isolation.** This is the important part of `docker-compose.yml`:

```yaml
  upstream:
    build: { context: ., dockerfile: Dockerfile.upstream }
    environment:
      INTERNAL_SECRET: ${INTERNAL_SECRET:?set INTERNAL_SECRET}
    networks: [internal]          # NO ports: — never published

  gateway:
    build: { context: ., dockerfile: Dockerfile }
    ports: ["9000:9000"]          # the only exposed surface
    environment: { INTERNAL_SECRET: ..., STRIPE_SECRET: ..., ... }
    volumes: ["./config.docker.json:/config.json:ro"]
    networks: [internal]
```

The upstream service has **no `ports:` mapping**. Docker therefore never publishes
it to the host, so nothing outside the Docker network can connect to port 8080.
Only the gateway, attached to the same `internal` network, can reach the upstream
— and it does so by the service name `upstream`, which is why the Compose config
uses `config.docker.json` (URLs point at `http://upstream:8080/...`) instead of
the `localhost` URLs in `config.json` used for local runs.

`${INTERNAL_SECRET:?set INTERNAL_SECRET}` makes Compose refuse to start if the
secret is unset — the same fail-fast discipline the binary uses, pushed up to the
orchestration layer.

**Two halves, together.** An attacker now faces both: they cannot reach the
upstream (no published port), and even if they breach the internal network they
cannot forge a request the upstream will accept (no internal secret). Either alone
has a failure mode — a misconfigured network, or a leaked secret — so we use both.

**Verifying it.** With the real built binaries run as local processes, three
checks confirm the cryptographic boundary end to end: a correctly Stripe-signed
request is verified, forwarded, and accepted by the upstream (`200 ok`); a
tampered body is rejected (`401`); and an attacker POSTing **directly** to the
upstream with a forged Gateway signature is rejected (`401`). The network-level
"upstream is unreachable from the host" property is enforced by the Compose file
above and is confirmed the moment you run `docker compose up` — the upstream has
no host-facing port to connect to.

---

## 17. Done

HookGuard is complete: a zero-dependency Go gateway that verifies Stripe, GitHub,
and Shopify webhook signatures behind one `Verifier` interface, collapses them
into a single Gateway-signature check for the upstream, proves its verifiers
correct against the providers' own libraries, and deploys as two tiny isolated
containers. Every rule in this guide is backed by a test you can run with
`go test ./...`.
