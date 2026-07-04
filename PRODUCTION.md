# Production Deployment Checklist

HookGuard is a signature-verifying gateway. It is the **only** surface exposed to
the internet; it verifies each provider's webhook signature, attaches an internal
Gateway signature, and forwards the unaltered body to a protected upstream that is
never published to the network.

This checklist gets a real deployment turnkey. Read it top to bottom once.

## 1. Put TLS in front — required

The gateway serves **plain HTTP on `:9000`** (`main.go`). Webhook providers POST to
a public **HTTPS** URL, so you must terminate TLS ahead of it:

- Caddy / nginx / a cloud load balancer terminates HTTPS and reverse-proxies to
  `gateway:9000`.
- Public URL becomes `https://hooks.your-domain.com/hook/<provider>`.
- Do **not** expose `:9000` directly to the internet without TLS — signatures are
  still verified, but the traffic (and your upstream response) would be in clear.

## 2. Secrets — set every one, or the gateway won't boot

`docker-compose.yml` uses `${VAR:?}`, so a missing secret fails fast at start.
Copy `.env.example` → `.env` and fill in **real** values:

| Env var | Used by | Source |
| --- | --- | --- |
| `INTERNAL_SECRET` | Gateway↔upstream signature (shared) | Generate a strong random string |
| `STRIPE_SECRET` | `/hook/stripe` | Stripe Dashboard → webhook signing secret (`whsec_…`) |
| `GITHUB_SECRET` | `/hook/github` | The secret you set when creating the GitHub webhook |
| `SHOPIFY_SECRET` | `/hook/shopify` | Shopify app → webhook signing secret |

PayPal uses **no** shared secret (asymmetric). See §4.

## 3. Routes — point each provider's dashboard at the matching path

Routes are declared in `config.json` (local) / `config.docker.json` (container,
mounted read-only). Each provider's dashboard webhook URL must match its route:

| Provider | Public webhook URL | Upstream (internal) |
| --- | --- | --- |
| Stripe | `https://…/hook/stripe` | `http://upstream:8080/stripe` |
| GitHub | `https://…/hook/github` | `http://upstream:8080/github` |
| Shopify | `https://…/hook/shopify` | `http://upstream:8080/shopify` |
| PayPal | `https://…/hook/paypal` | `http://upstream:8080/paypal` |

Edit the `upstream` URLs to point at your real application.

## 4. PayPal — extra step + a smoke test

PayPal carries no shared secret. Instead:

- Set `webhook_id` in the PayPal route to your **webhook subscription ID** from the
  PayPal Developer dashboard (replace `WH-CHANGE-ME`). It is config, not a secret.
- PayPal's signature is asymmetric (RSA-SHA256); the gateway fetches PayPal's public
  certificate at request time, pins the cert host to `*.paypal.com` over HTTPS, and
  validates the chain to a trusted root before trusting it.

**Smoke-test PayPal before trusting real traffic.** The HMAC providers are proven
byte-identical to their official libraries by the differential harness; PayPal has
no official Go library, so its live cert-fetch path is **not** exercised in CI. Send
one event from the **PayPal sandbox** webhook simulator through the deployed gateway
and confirm it returns `200`. This needs a (free) PayPal developer account.

## 5. Deploy

```sh
cp .env.example .env      # then edit .env with real secrets
docker compose up --build -d
```

- Only `gateway:9000` is published; `upstream` has no `ports:` mapping and is
  reachable only over the internal Docker network — the network half of the trust
  boundary.

## 6. Verify the deployment

```sh
go test ./...             # all green; includes the differential harness
bash demo.sh              # boots gateway+upstream, drives the full threat model
```

Expected from `demo.sh`: valid webhook → `200`; tampered body / wrong secret /
stale timestamp / forged gateway signature → `401`.

## 7. Operational notes

- **Forwarding is synchronous** — the gateway waits on your upstream and returns its
  status to the provider. If the upstream is slow, the provider may time out and
  retry. Keep the upstream fast, or add the deferred fast-ack pattern (issue #9).
- **Rotate secrets** by updating `.env` and restarting; there is no zero-downtime
  reload yet (issue #4).
- **Stateless** — no queue or retry buffer. An upstream 5xx is returned to the
  provider, which handles its own retry (issue #5 tracks a durable queue).

## 8. Console — the web dashboard (optional)

The Console (`/web`) is a separate Go binary and module: a marketing landing page,
session-authenticated auth, endpoint management, and a live log/stats view fed by
the gateway's verdict events. It is entirely optional — the gateway and upstream
work identically with the `console` service removed from `docker-compose.yml`.

### Bootstrap: creating the first (admin) account

Signup is **closed by default** (`CONSOLE_ALLOW_SIGNUP=false` in the shipped
compose file) — there is no logic that automatically closes signup after the
first user, so this is a manual one-time flip, not a "just works" toggle:

1. Temporarily set `CONSOLE_ALLOW_SIGNUP=true` in `docker-compose.yml` (or your
   `.env`, if you wire it through) and restart the `console` service.
2. Visit `http://<console-host>:7000/signup` and create the first account — it
   automatically becomes `admin`.
3. Set `CONSOLE_ALLOW_SIGNUP` back to `false` and restart again. From here,
   the admin creates any further users from **Settings → Users**.

Alternatively, skip signup entirely: `docker compose exec console /console
reset-password <email>` prints a one-time reset URL for an email that doesn't
exist yet — check `web/cmd/console/resetpassword.go` if you want to script an
account into existence without ever opening signup.

### Wiring the gateway's events to the Console

The shipped `docker-compose.yml` already sets `EVENTS_URL:
http://console:7000/api/v1/ingest` on the `gateway` service, so verdict events
flow to the Console out of the box. This env var is optional and additive —
unset it (or remove the `console` service) and the gateway's behavior is
byte-for-byte what it was before this env var existed (`events.go`).

### Exposure

The Console is a webhook **admin panel** — treat it with the same care as any
other admin surface, not as a public page (the landing page at `/` is public;
everything under `/dashboard` requires a session). Put it behind the same
TLS-terminating reverse proxy as the gateway (§1); consider restricting
`console:7000` to a VPN/allowlist rather than the open internet, on top of the
closed-signup default above.

### Data

The Console's data (users, sessions, endpoints, events) lives in one SQLite
file under the `console-data` volume (`CONSOLE_DATA_DIR=/data`). Back it up by
copying that one file. There is no retention/pruning job yet (issue tracked as
future work) — the `events` table grows unbounded until one is added.
