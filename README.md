# HookGuard

A self-hosted gateway that verifies inbound webhook signatures at the network
edge and forwards only authenticated traffic to your application. Zero external
dependencies in the shipped binary; single static binary; Stripe, GitHub, and
Shopify supported.

## Why

Every webhook provider signs its requests differently (different headers,
algorithms, encodings, replay rules). Verifying them correctly is fiddly and
error-prone, and a subtle mistake silently disables the security. HookGuard does
the verification once, in front of your app. Your app then trusts **one** thing —
the Gateway signature — instead of implementing N bespoke verifiers.

```
                          verifies provider signature
   Stripe ─┐              attaches Gateway signature
   GitHub ─┼──▶  HookGuard  ───────────────────────▶  Your app
   Shopify─┘   (port 9000)        internal network     (verifies ONE signature)
                                  app is NOT exposed
```

## How it works

1. A provider POSTs a signed webhook to `/hook/<provider>`.
2. HookGuard reads the **raw body** (never parsing it — parsing then
   re-serializing would change the bytes and break the signature) and verifies
   the provider's signature: HMAC compare in constant time, plus a replay-window
   check where the provider includes a timestamp (Stripe).
3. On success it re-signs the body with a single `INTERNAL_SECRET` (the **Gateway
   signature**, binding the verified provider name) and forwards the unchanged
   bytes upstream. On failure it returns `401` and forwards nothing.
4. Your app verifies that one Gateway signature and trusts the payload.

The trust boundary has two reinforcing halves: the **Gateway signature** (an
attacker without `INTERNAL_SECRET` cannot forge a request the app will accept) and
**network isolation** (the app is never published; only the gateway can reach it).

## Quick start (Docker)

```sh
cp .env.example .env        # fill in real secrets
docker compose up --build
```

Only the gateway is published (`:9000`). The upstream has no `ports:` mapping — it
is unreachable from the host; only the gateway, on the shared internal network,
can talk to it.

Send a webhook (example signs a Stripe payload with `openssl`):

```sh
SECRET=whsec_change-me                 # must match STRIPE_SECRET
BODY='{"id":"evt_1","amount":4242}'
TS=$(date +%s)
SIG=$(printf '%s' "$TS.$BODY" | openssl dgst -sha256 -hmac "$SECRET" | awk '{print $NF}')

curl -s -X POST localhost:9000/hook/stripe \
  -H "Stripe-Signature: t=$TS,v1=$SIG" \
  --data "$BODY"
# -> "ok"  (verified, forwarded, upstream accepted)
```

A tampered body, wrong secret, or stale timestamp returns `401` and never reaches
your app.

## Run locally (no Docker)

```sh
go build -o hookguard .
go build -o upstream ./cmd/upstream

INTERNAL_SECRET=dev ./upstream &        # listens on :8080
INTERNAL_SECRET=dev STRIPE_SECRET=whsec_change-me \
  GITHUB_SECRET=gh SHOPIFY_SECRET=shop ./hookguard   # listens on :9000
```

(`config.json` points the routes at `localhost:8080` for local runs;
`config.docker.json` points them at the `upstream` service for Compose.)

## Configuration

`config.json` maps each inbound path to a provider, an upstream URL, an optional
replay window, and the **name** of the env var holding the secret (secrets
themselves live in the environment, never in the file):

```json
{
  "path": "/hook/stripe",
  "provider": "stripe",
  "upstream": "http://upstream:8080/stripe",
  "replay_window": "5m",
  "secret_env": "STRIPE_SECRET"
}
```

## Tests

```sh
go test ./...                      # unit + integration + differential harness
go test -v -run Differential .     # the correctness proof (vs official libraries)
```

The differential harness cross-checks each verifier against the provider's own
official library (`stripe-go`, `go-github`) — these are **test-only**
dependencies; the gateway binary imports nothing outside the standard library:

```sh
go list -deps . | grep -E 'stripe|go-github'   # prints nothing
```

## Supported providers

| Provider | Header | Algorithm | Notes |
|---|---|---|---|
| Stripe  | `Stripe-Signature` | HMAC-SHA256 hex | timestamped (`t=,v1=`), replay window |
| GitHub  | `X-Hub-Signature-256` | HMAC-SHA256 hex | `sha256=` prefix, raw UTF-8 |
| Shopify | `X-Shopify-Hmac-SHA256` | HMAC-SHA256 base64 | base64-encoded output |

## Documentation

[docs/BUILD-GUIDE.md](docs/BUILD-GUIDE.md) — a from-scratch walkthrough of the
whole project with every concept and code block explained.
