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
time.Time) error`, with one adapter per provider (Stripe, GitHub, Shopify) in its
own file. Adding a provider is a new file plus one registry line; the gateway never
changes. This is the project's deep module and the differential harness's test
surface.

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

Tracked as GitHub issues. Out of the current 3-provider scope: PayPal (asymmetric
RSA), Twilio (URL + sorted params), the Standard Webhooks spec. Beyond verification:
mTLS between gateway and upstream, dynamic secret rotation, a durable queue with
retry/backpressure (HookGuard is intentionally stateless), observability, per-route
rate limiting, and a fast-ack pattern. Two architectural refactors are scheduled by
condition: a self-registering provider registry (when provider count grows) and a
composable replay-window check (when a second timestamped provider lands).

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

## References

Derived from the project's source evaluation (works cited therein): provider
documentation for Stripe, GitHub, Shopify, Twilio, PayPal; Hookdeck, Convoy, Svix,
Caddy `caddy-hmac`; the Standard Webhooks specification. Full citation list in the
source evaluation document.
