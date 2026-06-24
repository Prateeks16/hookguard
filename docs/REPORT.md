# HookGuard — Project Report

> Living document. Structure is the standard major-project layout and is easy to
> remap onto a university template if one is mandated. The **Development Log**
> (§10) is updated step by step as issues are resolved.

## 1. Abstract

HookGuard is a self-hosted gateway that verifies inbound webhook signatures at the
network edge and forwards only authenticated traffic to a protected application.
Webhooks are unauthenticated HTTP by default, and every provider signs them
differently, so correct verification is fragmented, error-prone, and frequently
skipped — leaving critical endpoints open to payload spoofing and replay. HookGuard
verifies each provider's signature correctly once, then re-signs verified traffic
with a single internal signature so the downstream application performs one check
instead of N. It is a single zero-dependency Go binary, deploys as two isolated
containers, and ships with a differential test harness proving its verifiers reach
the same verdict as the providers' own official libraries.

## 2. Introduction and Problem

Modern systems communicate by webhooks: a provider POSTs an event (a payment, a
code push, an order) to a URL on the consumer's server. Because the endpoint is
public and HTTP is unauthenticated, the consumer cannot distinguish a genuine
event from a payload forged by an attacker. Providers solve this with
cryptographic signatures over the request body, sent in a header; the consumer
recomputes and compares.

The difficulty is fragmentation. Each provider uses a different header, algorithm,
encoding, and rule set (timestamps, prefixes, base64 vs hex). A team integrating
several providers cannot write one verifier — they maintain a different one per
provider, and a subtle mistake (most commonly parsing the body and re-serializing
it, which changes the bytes the signature was computed over) silently disables the
protection.

## 3. Threat Model

- **Payload spoofing.** An attacker POSTs a forged event to an unauthenticated
  endpoint, triggering business logic (unlock a subscription, ship goods, deploy
  code).
- **Replay.** An attacker resends a genuine, correctly-signed request to repeat a
  non-idempotent action. Mitigated by a timestamp + freshness window where the
  provider includes one (Stripe).
- **Timing attack.** A non-constant-time signature comparison leaks, via response
  timing, how many leading bytes matched, enabling byte-by-byte forgery. Mitigated
  by constant-time comparison everywhere.
- **Raw-body corruption.** Middleware or proxies that parse/re-serialize the body
  change its bytes and break verification. Mitigated by never parsing the body.
- **Internal forgery.** An attacker who reaches the protected application directly
  bypasses the gateway. Mitigated by the Gateway signature plus network isolation.

## 4. Design and Architecture

**Verifier seam.** A single interface, `Verify(rawBody []byte, h http.Header, now
time.Time) error`, with one adapter per provider (Stripe, GitHub, Shopify,
PayPal) in its own file. Adding a provider is a new file plus one registry line;
the gateway never changes — proven when PayPal's fundamentally different
asymmetric scheme required no change to the gateway or the interface, only a new
file and one factory branch. This is the project's deep module and the
differential harness's test surface.

**PayPal's asymmetric shape and its SSRF guard.** PayPal has no shared secret:
it signs with a private key and is verified against PayPal's own certificate,
fetched at runtime from a `paypal-cert-url` header. Because that header is
attacker-controlled input, the fetch is gated by a host allowlist
(`*.paypal.com`, https only) checked *before* any network call, and the fetched
certificate must additionally chain to a trusted root — a host match alone is
not sufficient. The certificate is cached per cert-URL to avoid refetching on
every webhook. PayPal's webhook subscription ID (`webhook_id`) is ordinary
config, not a secret, so its route carries no `secret_env`.

**Gateway signature.** On a verified request the gateway re-signs `"<provider>.
<body>"` with one `INTERNAL_SECRET` and forwards it. The protected application
verifies that one signature — collapsing N provider verifications into one — and
the bound provider name means an attacker cannot relabel a payload without breaking
the signature. This deliberately replaces the forgeable `X-Verified: true` boolean
header anti-pattern.

**Trust boundary, two reinforcing halves.** The Gateway signature (an attacker
without the internal secret cannot forge an acceptable request) and network
isolation (the application is never published; only the gateway can reach it over
an internal network). Either alone has a failure mode; together they are solid.

**Configuration.** JSON (not YAML — YAML would be a dependency, breaking the
zero-dependency goal) maps each path to a provider, an upstream URL, an optional
replay window, and the *name* of the environment variable holding the secret.
Secrets live in the environment, never in the file.

**Zero dependencies.** The shipped binary imports only the Go standard library
(`crypto/hmac`, `crypto/sha256`, `encoding/hex`, `encoding/base64`, `net/http`).
This is the core differentiator versus heavier gateways.

## 5. Implementation

Written in Go. Layout: a flat `package main` for the gateway; `cmd/upstream` for a
sample protected application; `internal/gatewaysig` (the Gateway signature, shared
by both). The request path buffers the raw body once, selects the provider verifier
by path, rejects an invalid signature with `401`, attaches the Gateway signature,
and forwards the unaltered bytes. Provider construction goes through a pure factory
that takes an already-resolved secret, so its branching is unit-testable (see §6).

## 6. Testing and Results

- **Raw-body invariant.** A passthrough test sends a payload built to break naive
  parsing (odd whitespace, unsorted keys, a trailing-zero float, an emoji) and
  asserts the upstream receives byte-identical bytes.
- **Per-verifier unit tests.** Each provider has a table test: valid, tampered,
  wrong-secret, missing-header, encoding edge cases; Stripe additionally covers
  stale and fresh timestamps.
- **Factory tests.** The pure `buildVerifier` is tested across all four branches
  (empty secret, bad replay window, unknown provider, correct dispatch).
- **Gateway-signature + trust boundary.** A round-trip test plus an end-to-end test
  showing a genuinely-signed request is accepted and an attacker POSTing directly
  to the upstream with a forged Gateway signature is rejected.
- **Differential harness (headline result).** For each provider, a matrix of
  payloads is run through our verifier and an independent oracle, asserting the
  verdicts agree and match expectation. Oracles: `stripe-go` and `go-github` (the
  providers' official libraries); Shopify has no official Go library, so its oracle
  is an independent re-implementation of the documented algorithm (stated honestly).
  **Result: 14 cases across three providers, all in agreement.** The harness also
  surfaced that `stripe-go`'s `ConstructEvent` couples signature verification with
  event-version deserialization; HookGuard does pure signature verification, and the
  comparison isolates the signature verdict via `IgnoreAPIVersionMismatch`.

The oracle libraries are **test-only**: `go list -deps . | grep -E 'stripe|go-github'`
is empty, confirming the shipped binary remains zero-dependency.

**PayPal (asymmetric, no official Go library).** The differential-harness
approach does not apply — there is no official `stripe-go`/`go-github`
equivalent to diff against. Instead: a roundtrip test signs with a locally
generated RSA keypair exactly as PayPal documents and asserts verification
passes, then asserts it rejects a tampered body, the wrong key, the wrong
`webhookId`, and a malformed signature; a dedicated test asserts the
`paypal-cert-url` host-pin rejects every non-PayPal host (including
suffix-confusion attempts like `paypal.com.evil.com`) before any fetch; and a
dedicated test asserts a self-signed certificate (standing in for an
attacker-supplied one) fails chain validation. **Not** covered by an automated
test: the full pipeline against a real certificate fetched from a real PayPal
host and a genuinely PayPal-signed event — that requires a PayPal sandbox
account and webhook simulator capture, and is recorded here as the deliberate
next manual step rather than simulated in CI.

**Validation strength (stated honestly).** Stripe and GitHub are cross-checked
against the providers' own official libraries, so their correctness is
*independently* established. Shopify implements the documented algorithm but is
verified only against an independent re-implementation of that same documented
scheme — strong, but not a check against Shopify's own code. PayPal's signature
math is verified by roundtrip against a locally generated key (sound, since the
algorithm is just RSA-SHA256 over a documented message), but the cert-fetch and
chain-validation pipeline is untested end-to-end against a real PayPal
certificate — the weakest-validated piece in the project, and honestly the
reason a manual sandbox-capture step is named rather than skipped. All other
tests use synthetic payloads signed with each provider's algorithm; the suite
has not yet been run against real captured webhooks or vendor-published test
vectors generally, which would be the next step to raise assurance further.

**Live demonstration.** `demo.sh` starts the gateway and a sample upstream and
fires the full threat-model matrix — a valid webhook (`200 ok`), then tampered,
wrong-secret, stale-timestamp, and a direct-to-upstream forgery (all `401`) —
followed by the differential harness. Reproducible with `bash demo.sh`.

## 7. Competitive Positioning (honest)

The technical problem is real and the market is mature: Hookdeck (managed, 120+
providers, durable queuing), Convoy (open-source Go, database-backed), Svix Bridge,
Kong's HMAC plugin, Caddy's `caddy-hmac` (zero-code HMAC verify-and-proxy), and the
cross-industry "Standard Webhooks" specification. HookGuard is **not** the first
verifying proxy. Its defensible angle is a published correctness harness (which the
incumbents do not ship), a genuinely zero-dependency single binary, and total data
sovereignty (self-hosted, no payload leaves the customer's network). For an academic
project, the value is engineering depth and the correctness proof, not market
novelty.

## 8. Limitations and Future Work

Tracked as GitHub issues. Out of the current 4-provider scope: Twilio (HMAC over
the exact URL plus lexically-sorted params — breaks behind a path-rewriting
proxy), the Standard Webhooks spec. A manual sandbox-capture fixture test for
PayPal (a real webhook from a PayPal sandbox account and webhook simulator) is
also deferred — see §6. Beyond verification: mTLS between gateway and upstream,
dynamic secret rotation, a durable queue with retry/backpressure (HookGuard is
intentionally stateless), observability, per-route rate limiting, and a fast-ack
pattern. Two architectural refactors are scheduled by condition: a
self-registering provider registry (now that provider count has grown to four,
worth revisiting) and a composable replay-window check (when a second
timestamped provider lands — PayPal carries a transmission time but no replay
window was implemented for it yet, deferred deliberately).

## 9. Conclusion

HookGuard demonstrates that the fragmented, error-prone task of webhook signature
verification can be consolidated into a single small, dependency-free gateway with a
provable correctness guarantee. By verifying once at the edge and collapsing the
downstream contract to one signature check, it removes the per-provider cognitive
load that causes real-world security gaps, while staying lightweight enough to
deploy as two isolated containers.

## 10. Development Log

Step-by-step record, updated as issues are resolved.

- **Build (Days 1–6).** Scaffold + raw-body passthrough; Verifier interface +
  Stripe; GitHub; Shopify + Gateway signature; differential harness; Docker
  deployment with an isolated upstream. All tests green; gateway binary zero-dep.
- **Issue #10 — testable Verifier factory.** Split secret *resolution* (now at the
  call site in `main`) from verifier *construction* (`buildVerifier`, now a pure
  function of provider/secret/replay-window). Added `verifier_test.go` covering all
  four factory branches. Closes the project's one previously-untested piece of
  logic. *Resolved.*
- **Demo.** Added `demo.sh` — a reproducible live walkthrough of the threat model
  plus the differential harness.
- **Issue #1 — PayPal verifier (asymmetric RSA).** Added a fourth provider behind
  the unchanged `Verifier` interface: offline RSA-SHA256 over
  `transmissionId|transmissionTime|webhookId|crc32(body)`, verified against a
  certificate fetched from `paypal-cert-url`. Required widening
  `buildVerifier` from per-provider scalars to `(Route, secret, verifierDeps)`
  since PayPal needs an `*http.Client` and no secret, and adding `webhook_id` to
  `Route` (config, not a secret — no `secret_env` for PayPal routes). The
  cert-url host is pinned to `*.paypal.com` over https *before* any fetch (the
  fetch target is attacker-controlled input — the single most important check
  in this provider) and the fetched certificate must additionally chain to a
  trusted root; results are cached per cert-URL. No official PayPal Go library
  exists, so correctness rests on a roundtrip test against a locally generated
  keypair plus dedicated SSRF and chain-rejection tests, rather than a
  differential harness; a real sandbox-capture fixture is named as a deferred
  manual step (§6, §8). Replay-window freshness checking on the transmission
  time was considered (PayPal does carry one) and deliberately deferred, same
  as the registry refactor. *Resolved.*

## References

Derived from the project's source evaluation (works cited therein): provider
documentation for Stripe, GitHub, Shopify, Twilio, PayPal; Hookdeck, Convoy, Svix,
Caddy `caddy-hmac`; the Standard Webhooks specification. Full citation list in the
source evaluation document.
