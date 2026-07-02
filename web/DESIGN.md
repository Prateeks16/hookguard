# HookGuard Web — Design Document

> Design-only deliverable. Nothing in this document is implemented yet. It covers the
> marketing landing page, the mascot identity, a real auth system, the dashboard
> (console), system architecture, data model, and a phased implementation plan.
> Scope boundary: everything here lives under `/web` and must never add a dependency
> to the gateway binary's dependency graph.

---

## 1. Executive summary

HookGuard gets a web presence and a product surface: a single additional Go binary —
the **Console** — that serves a green-branded marketing landing page at `/` and a
real, session-authenticated dashboard at `/dashboard`. The Console manages Routes
(the gateway's `config.json`, but with a UI), receives a live feed of
verification verdicts from the gateway, and renders stats and a streaming log of
accepted/rejected webhooks with reasons. The visual identity centers on **Siggy**,
a cartoon shield-shaped gatekeeper who checks every webhook at the gate — friendly
to legitimate traffic, immovable toward forgeries.

The Console is a **separate Go module** at `/web` with exactly two third-party Go
dependencies (`modernc.org/sqlite`, `golang.org/x/crypto`); the shipped gateway
binary stays **zero-dependency, stdlib only** — that claim is checked in CI, not
just asserted. Frontend is server-rendered Go `html/template` + a vendored htmx
file + hand-written CSS design tokens: no Node toolchain anywhere in the repo. The
gateway's only change is an optional, env-gated, stdlib-only event emitter
(~40 lines) that POSTs each verify verdict to the Console, signed with the existing
Gateway signature.

The whole thing deploys as a third container in the existing `docker-compose.yml`,
storing its data in a single SQLite file — matching HookGuard's identity: small,
self-hosted, sovereign, no moving parts you didn't ask for.

---

## 2. Brand & visual identity

### 2.1 Concept

Hookdeck is blue and infrastructure-flavored. HookGuard is **green** —
"gatekeeper, safe, go." The brand feeling is a *calm checkpoint*: everything that
should pass, passes instantly; everything else is turned away politely and loudly
logged. Visual language: gates, barriers, stamps, badges, checkmarks; terminal
snippets as proof, not decoration.

### 2.2 Color palette (exact values)

**Brand greens** (primary scale — used for actions, accents, the mascot):

| Token | Hex | Use |
|---|---|---|
| `--green-50`  | `#F0FDF4` | light-mode tinted backgrounds |
| `--green-100` | `#DCFCE7` | badges, subtle fills |
| `--green-200` | `#BBF7D0` | hover fills (light) |
| `--green-300` | `#86EFAC` | charts, secondary accents |
| `--green-400` | `#4ADE80` | **focus rings**, links on dark, mascot highlight |
| `--green-500` | `#22C55E` | **mascot body**, "accepted" verdict, charts |
| `--green-600` | `#16A34A` | **primary action** (buttons) on light |
| `--green-700` | `#15803D` | primary hover |
| `--green-800` | `#166534` | pressed states |
| `--green-900` | `#14532D` | mascot outline, deep fills |
| `--green-950` | `#052E16` | hero gradients (dark) |

**Accent** (used sparingly — hero glow, one highlight per screen):

| Token | Hex | Use |
|---|---|---|
| `--accent-lime` | `#BEF264` | hero keyword highlight, "live" pulse dot |

**Neutrals** — green-tinted, not gray-blue (this is what makes the palette feel
owned rather than default-Tailwind):

| Token | Hex (dark mode) | Role |
|---|---|---|
| `--ink-950` | `#0A120D` | page background |
| `--ink-900` | `#0F1911` | card background |
| `--ink-800` | `#16241A` | raised surface / hover |
| `--ink-700` | `#24382B` | borders, dividers |
| `--ink-400` | `#7E947F` | disabled text |
| `--ink-300` | `#9BB1A4` | muted / secondary text |
| `--ink-100` | `#E6F0E9` | primary text on dark |

| Token | Hex (light mode) | Role |
|---|---|---|
| `--paper` | `#FAFDF9` | page background |
| `--card` | `#FFFFFF` | card background |
| `--border` | `#E2EAE3` | borders |
| `--text` | `#10231A` | primary text |
| `--text-muted` | `#5B6E60` | secondary text |

**Semantic** (verdicts are the product — these must be unmistakable):

| Token | Light | Dark | Use |
|---|---|---|---|
| `--ok` | `#16A34A` | `#22C55E` | accepted, success toasts |
| `--reject` | `#DC2626` | `#F87171` | rejected verdict, destructive actions |
| `--warn` | `#D97706` | `#FBBF24` | stale-timestamp class of reasons, degraded |
| `--info` | `#0284C7` | `#38BDF8` | informational banners |

Rule: **brand green ≠ automatic success.** In log views, "accepted" uses `--ok`
with a ✓ glyph and "rejected" uses `--reject` with a ✕ glyph *plus* the words,
so verdicts survive color-blindness and monochrome screenshots.

### 2.3 Typography

Self-hosted `woff2` files (no Google Fonts CDN — a security product should not
leak its users' dashboards to third-party origins). All three are OFL-licensed.

| Role | Face | Weights | Notes |
|---|---|---|---|
| Display / headings | **Space Grotesk** | 500, 700 | techy but warm; matches the mascot's rounded-geometric feel |
| UI / body | **Inter** | 400, 500, 600 | with `font-feature-settings: "cv05","tnum"` for tabular stats |
| Code / logs / payload hashes | **JetBrains Mono** | 400, 700 | log stream, terminal blocks, signature headers |

Type scale (rem, 1rem = 16px): `0.75 / 0.875 / 1 / 1.125 / 1.375 / 1.75 / 2.25 / 3 / 3.75`.
Hero H1 at `3.75rem/1.05` desktop, `2.25rem` mobile.

### 2.4 Spacing, shape, motion, tone

- **Spacing**: 4px base grid; section rhythm on landing page = 96px desktop / 56px mobile.
- **Radii**: inputs/buttons `8px`, cards `12px`, hero panels & mascot bubbles `16px`.
- **Borders over shadows** on dark; a single soft shadow (`0 1px 2px rgb(16 35 26 / .06), 0 8px 24px rgb(16 35 26 / .08)`) on light.
- **Focus**: 2px ring `--green-400`, 2px offset, always visible — keyboard-first, it's a security tool.
- **Motion**: 150ms ease-out for UI; the only "showy" animation allowed is the hero barrier lifting once on load and the live-log pulse dot. Respect `prefers-reduced-motion`.
- **Dark mode is the default** (developer tool); light mode via `prefers-color-scheme` plus a manual toggle persisted in `localStorage`. All tokens defined for both in one `tokens.css`.
- **Voice**: calm, precise, lightly wry *at the gate* — never jokey about failures.
  Microcopy examples:
  - rejected log detail: "Turned away at the gate: signature mismatch."
  - empty log state: "No traffic at the gate yet. Siggy is… very rested."
  - 404: "This route isn't in my config."
  - config export toast: "config.json ready. Restart the gateway to apply."

---

## 3. Mascot — **Siggy**, the gate shield

### 3.1 Concept

**Name:** Siggy (from *signature*; alternates considered and rejected: "Gus"
too folksy, "Warden" too grim, "Hooky" collides with truancy).

**Species/shape:** a living **shield** — the silhouette *is* the logo. Rounded
shield body in `--green-500` with a `--green-900` outline, short legs with black
boots, expressive oval eyes, a peaked **security cap** with a badge, and a
**clipboard** that carries the current verdict. Often posed next to a striped
**boom barrier** (the "gate").

**Personality:** friendly-but-vigilant. Siggy *wants* to let your webhook in —
it just needs to see some ID. Never smug when rejecting; rejection poses read as
"rules are rules," with the reason stamped on the clipboard. Siggy is competent,
a little proud of the zero-dependency badge on its cap, and visibly delighted by
a valid signature.

**Silhouette test:** cap + shield + boots must read at 24×24px (favicon). At
small sizes, drop the clipboard and barrier; keep cap, eyes, outline.

### 3.2 Expression / pose set (8 core poses, each one SVG file)

| ID | Pose | Face | Prop | Used at |
|---|---|---|---|---|
| `siggy-post.svg` | standing at barrier, one palm raised in a friendly "halt" | alert smile, raised brow | boom barrier | **landing hero** |
| `siggy-approve.svg` | stamping a big ✓ | delighted, eyes closed | stamp + clipboard "✓ VERIFIED" | success toasts, "how it works" step 3 |
| `siggy-reject.svg` | arms crossed | neutral-firm (not angry) | clipboard "✕ 401" | login error, rejected-event detail |
| `siggy-inspect.svg` | leaning in with magnifying glass over a letter/envelope | one eye enlarged through lens | magnifier, envelope with wax seal | "how it works" step 2, docs |
| `siggy-sleep.svg` | slumped on a stool, cap over eyes, "zzz" | asleep | stool | empty states (no events, no endpoints) |
| `siggy-lost.svg` | scratching head with an upside-down map | puzzled | map | 404 page |
| `siggy-run.svg` | jogging with clipboard, motion lines | focused | clipboard | loading skeletons / spinner replacement (≥ 400ms waits only) |
| `siggy-badge.svg` | head-and-cap only, front-facing | wink | — | favicon, avatar fallback, footer sign-off |

### 3.3 Usage rules

1. **One Siggy per viewport.** It's a mascot, not a pattern.
2. Siggy never appears inside the raw log rows or data tables — data surfaces stay serious; Siggy owns *edges*: heroes, empty states, errors, toasts.
3. Siggy never mocks the user or the sender ("nice try" is banned copy).
4. Fixed palette: body `#22C55E`, belly highlight `#4ADE80` at 55% opacity, outline `#14532D` at 5px stroke (scales with viewBox), cap `#14532D`, badge `#BEF264`, boots `#0A120D`. No re-coloring per theme; Siggy is identical in light and dark.
5. Minimum sizes: full pose 96px tall; `siggy-badge` down to 16px.

### 3.4 Concept sketch (real inline SVG — `siggy-post`, the hero pose)

This is the working concept vector, also saved at `web/design/mascot-siggy.svg`.
It is a sketch to hand to a refinement pass, not final art — but it establishes
proportions (head-cap ⅓, body ½, legs ⅙), the outline weight, and the palette.

```svg
<svg viewBox="0 0 300 300" xmlns="http://www.w3.org/2000/svg" role="img"
     aria-label="Siggy, the HookGuard gatekeeper, standing at a boom barrier with one palm raised">
  <!-- ===== boom barrier (behind Siggy) ===== -->
  <g>
    <rect x="18" y="188" width="14" height="92" rx="5" fill="#14532D"/>
    <g transform="rotate(-22 25 196)">
      <rect x="22" y="188" width="170" height="17" rx="8.5" fill="#E6F0E9"
            stroke="#14532D" stroke-width="4"/>
      <path d="M52 190h24l-11 13H41Z" fill="#22C55E"/>
      <path d="M100 190h24l-11 13H89Z" fill="#22C55E"/>
      <path d="M148 190h24l-11 13h-24Z" fill="#22C55E"/>
    </g>
    <circle cx="25" cy="196" r="7" fill="#BEF264" stroke="#14532D" stroke-width="3"/>
  </g>

  <!-- ===== legs & boots ===== -->
  <rect x="138" y="242" width="15" height="28" rx="7.5" fill="#14532D"/>
  <rect x="168" y="242" width="15" height="28" rx="7.5" fill="#14532D"/>
  <ellipse cx="143" cy="274" rx="18" ry="9" fill="#0A120D"/>
  <ellipse cx="177" cy="274" rx="18" ry="9" fill="#0A120D"/>

  <!-- ===== raised left arm (friendly halt) ===== -->
  <path d="M104 168 Q84 150 78 122" fill="none" stroke="#14532D"
        stroke-width="13" stroke-linecap="round"/>
  <g transform="rotate(-15 78 112)">
    <ellipse cx="78" cy="110" rx="15" ry="17" fill="#22C55E"
             stroke="#14532D" stroke-width="5"/>
    <path d="M68 100v14 M78 96v18 M88 100v14" stroke="#14532D"
          stroke-width="4" stroke-linecap="round"/>
  </g>

  <!-- ===== shield body ===== -->
  <path d="M158 62c28 13 50 17 64 18v76c0 46-28 76-64 88-36-12-64-42-64-88V80
           c14-1 36-5 64-18Z"
        fill="#22C55E" stroke="#14532D" stroke-width="6" stroke-linejoin="round"/>
  <!-- belly highlight -->
  <path d="M158 80c19 8 35 11 46 12v62c0 35-19 58-46 68V80Z"
        fill="#4ADE80" opacity="0.55"/>

  <!-- ===== face ===== -->
  <ellipse cx="138" cy="130" rx="11" ry="13" fill="#FFFFFF"/>
  <ellipse cx="176" cy="130" rx="11" ry="13" fill="#FFFFFF"/>
  <circle cx="140" cy="133" r="5" fill="#0A120D"/>
  <circle cx="174" cy="133" r="5" fill="#0A120D"/>
  <!-- vigilant brows: inner ends slightly raised -->
  <path d="M126 112q12 -7 24 -2" fill="none" stroke="#14532D"
        stroke-width="5" stroke-linecap="round"/>
  <path d="M188 112q-12 -7 -24 -2" fill="none" stroke="#14532D"
        stroke-width="5" stroke-linecap="round"/>
  <path d="M146 156q12 10 24 0" fill="none" stroke="#14532D"
        stroke-width="5" stroke-linecap="round"/>

  <!-- ===== security cap ===== -->
  <path d="M112 66q46 -34 92 0l-4 12q-42 -18 -84 0Z"
        fill="#14532D" stroke="#14532D" stroke-width="4" stroke-linejoin="round"/>
  <path d="M108 78q50 -14 100 0q4 8 -4 10q-46 -12 -92 0q-8 -2 -4 -10Z"
        fill="#14532D"/>
  <!-- visor -->
  <path d="M124 88q34 10 68 0q6 8 -2 12q-32 -8 -64 0q-8 -4 -2 -12Z"
        fill="#052E16"/>
  <!-- cap badge: the zero-dep mark -->
  <circle cx="158" cy="62" r="10" fill="#BEF264" stroke="#052E16" stroke-width="3"/>
  <path d="M153 62l4 4 7 -8" fill="none" stroke="#052E16"
        stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/>

  <!-- ===== right arm holding clipboard ===== -->
  <path d="M214 160 Q234 168 240 184" fill="none" stroke="#14532D"
        stroke-width="13" stroke-linecap="round"/>
  <g transform="rotate(8 246 210)">
    <rect x="224" y="182" width="52" height="66" rx="7" fill="#FFFFFF"
          stroke="#14532D" stroke-width="5"/>
    <rect x="240" y="174" width="20" height="12" rx="4" fill="#14532D"/>
    <path d="M233 204h34 M233 216h34" stroke="#9BB1A4" stroke-width="4"
          stroke-linecap="round"/>
    <path d="M236 232l7 7 14 -16" fill="none" stroke="#16A34A"
          stroke-width="6" stroke-linecap="round" stroke-linejoin="round"/>
  </g>
</svg>
```

---

## 4. Landing page — IA & copy direction

Single page at `/`, server-rendered, sections in order. Anchor nav
(`Features · Providers · Security · Compare · Pricing · Docs`) in a sticky
header with the `siggy-badge` mark + wordmark "HookGuard" (Space Grotesk 700),
and a `Sign in` link + `Get started` button (→ `/signup`).

**Honesty rule for the whole page:** this is a college-built OSS project. No
fabricated customer logos, no invented testimonials. The social-proof slot is
filled with *verifiable engineering claims* — which happens to be exactly
HookGuard's differentiator anyway.

### 4.1 Hero

- **H1:** `Every webhook checked at the gate.`
- **Sub:** `HookGuard is a self-hosted gateway that verifies Stripe, GitHub, Shopify and PayPal webhook signatures at your network edge — and forwards only authenticated traffic to your app. Your app verifies one signature. HookGuard handles the other four.`
- **Primary CTA:** `Deploy in 5 minutes` (→ Quick-start docs / `docker compose up`)
- **Secondary CTA:** `Read the correctness proof` (→ differential-harness section of docs)
- **Visual:** left = copy; right = `siggy-post` at the barrier, with three envelope "webhooks" queued: two wax-sealed (green ✓ tags: `stripe`, `github`) passing under the lifted barrier, one torn-seal envelope held back with a small red `401` tag. Barrier lifts once on load (240ms, `prefers-reduced-motion` aware).
- **Beneath the fold-line, a live terminal strip** (JetBrains Mono, typed-out once):
  `$ curl -X POST hooks.example.com/hook/stripe … → 200 ok` then
  `$ curl (tampered body) … → 401 unauthorized`

### 4.2 Proof bar (the social-proof slot, kept honest)

A single row of stat chips, each linking to the receipt in the repo/docs:

`0 external dependencies` · `1 static binary` · `4 providers, 4 signature shapes` · `14/14 differential cases agree with official libraries` · `100% self-hosted — payloads never leave your network`

Caption: `Every number on this bar is checked in CI or reproducible with one command.`

### 4.3 How it works (3 steps)

Headline: `One gate. One signature. Zero guesswork.`

1. **Point providers at the gate.** `Stripe, GitHub, Shopify and PayPal POST to https://hooks.you.com/hook/<provider>.` (mini diagram: envelopes → gate)
2. **Siggy checks the signature.** `Constant-time HMAC for the symmetric providers; RSA-SHA256 against PayPal's chain-validated certificate — over the raw bytes, never a re-serialized body.` (`siggy-inspect`)
3. **Your app trusts one thing.** `Verified traffic is re-signed with a single Gateway signature and forwarded byte-identical. Everything else gets a 401 and a log line.` (`siggy-approve`)

Below: the README's ASCII flow redrawn as a clean SVG diagram (providers → HookGuard :9000 → internal network → Your app), with the caption
`Your app is never exposed. Only the gate is.`

### 4.4 Provider grid

Headline: `Four providers, four different locks.`
Sub: `Every provider signs differently — different headers, algorithms, encodings, replay rules. HookGuard speaks each dialect so you don't have to.`

Four cards (provider name, header, shape, one distinguishing detail — factual,
from the README table):

- **Stripe** — `Stripe-Signature` · HMAC-SHA256 hex · timestamped `t=,v1=` with a replay window.
- **GitHub** — `X-Hub-Signature-256` · HMAC-SHA256 hex · `sha256=` prefix.
- **Shopify** — `X-Shopify-Hmac-SHA256` · HMAC-SHA256 base64.
- **PayPal** — `paypal-transmission-sig` · **asymmetric** RSA-SHA256 · cert fetched at runtime, host-pinned to `*.paypal.com`, chain-validated. Badge: `no shared secret`.

Footer link: `Need Twilio or Standard Webhooks? They're on the roadmap →` (links to issues — matches future-work doc, promises nothing).

### 4.5 Security / zero-dependency story

Headline: `A security tool you can audit in an afternoon.`

Three columns:

1. **Zero dependencies, provably.** Terminal visual:
   `$ go list -deps . | grep -E 'stripe|go-github'` → `(nothing)`.
   Copy: `The shipped gateway imports only the Go standard library. Nothing to typosquat, nothing to vendor-audit, no supply chain but Go itself.`
2. **A published correctness harness.** `Each verifier's verdict is differentially tested against the provider's own official library, across valid, tampered and adversarial payloads. Incumbents don't ship this.`
3. **A real trust boundary.** `Cryptographic half: the Gateway signature binds the verified provider name — not a forgeable "X-Verified: true" header. Network half: your app has no published port.`

Mascot: none in this section — it's the "serious" section; the cap badge glyph may appear as a bullet mark.

### 4.6 Comparison vs the incumbents

Headline: `Small on purpose.`
Sub: `Hookdeck, Convoy and Svix are excellent — and big. HookGuard does one job at the edge, with nothing attached.`

| | **HookGuard** | Hookdeck | Convoy | Svix |
|---|---|---|---|---|
| Model | self-hosted OSS | managed SaaS | OSS + cloud | OSS + cloud |
| Edge signature verification | core product | ✓ | ✓ | ✓ |
| Zero-dependency binary | **✓ (checked in CI)** | n/a | ✗ (Postgres + Redis) | ✗ |
| Published differential correctness harness | **✓** | ✗ | ✗ | ✗ |
| Durable queue / retry | ✗ — stateless by design | ✓ | ✓ | ✓ |
| Providers verified | 4 | 100+ | many | many |
| Payloads leave your network | **never** | yes | no (self-host) | depends |

Footnote (verbatim tone from REPORT §7): `We are not the first verifying proxy, and we're honest about the ✗ column: if you need durable delivery and a hundred providers, use Hookdeck. If you need a small, auditable gate you fully control, that's us.`

### 4.7 Pricing

Headline: `Free. It's yours.`

- **Card 1 — Self-hosted (Open Source):** `$0 forever` — all 4 providers, differential harness, Docker deploy, the Console dashboard, community support. CTA: `Deploy now`.
- **Card 2 — HookGuard Cloud:** `Managed gate, zero ops` — marked **Waitlist**, email capture only. *(Open question §11 — ship this card or cut it.)*

Mascot: `siggy-approve` stamping the $0 card.

### 4.8 Final CTA band

Full-width `--green-950` gradient band.
**H2:** `Put a gatekeeper on it.`
Sub: `docker compose up and every webhook gets checked at the door.`
CTA: `Get started` + a copyable one-liner code block (`cp .env.example .env && docker compose up --build`).
Mascot: `siggy-post` small, at the right edge, palm raised at the reader.

### 4.9 Footer

Columns: Product (Features, Providers, Compare, Pricing) · Docs (Quick start, Correctness proof, Production checklist, Report) · Project (GitHub, Issues, License) · Legal (Privacy — trivial: "we host nothing of yours"). Sign-off line: `siggy-badge` + `Checked at the gate.` Theme toggle lives here and in the header.

---

## 5. Auth / login system design

Posture: this is a **security product's own console** — the auth must be boring,
conservative, and defensible in a viva. No novel schemes.

### 5.1 Model decisions

- **Server-side cookie sessions, not JWT.** Justification: (1) instant revocation — logout and admin-revoke actually kill the session, no token lives on after compromise; (2) no signing-key management, no algorithm-confusion class of bugs; (3) this is a same-origin server-rendered app — JWT's stateless-API advantage buys nothing here; (4) session rows give an auditable "active sessions" screen for free. JWTs would be re-introduced only for a future machine API (§11).
- **Session token:** 32 bytes from `crypto/rand`, base64url in the cookie; **only `SHA-256(token)` is stored** in the DB (a DB leak doesn't yield usable cookies). Cookie: `hg_session`, `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`. Idle timeout **7 days** (sliding, `last_seen_at`), absolute lifetime **30 days**. Token is **rotated on login** (session fixation) and all other sessions for the user are listable/revocable in Settings.
- **Password hashing: Argon2id** via `golang.org/x/crypto/argon2` — params `time=3, memory=64MiB, threads=2, keyLen=32`, per-user 16-byte salt, encoded in PHC string format so params can be upgraded later. (bcrypt is the acceptable fallback if the 64MiB footprint bothers the target host — decide at M1; default Argon2id.)
- **CSRF:** synchronizer token — 32-byte random per session, stored server-side, embedded as a hidden input in every form and sent as `X-CSRF-Token` on htmx requests; verified with `subtle.ConstantTimeCompare` on **every** non-GET route. `SameSite=Lax` is the backstop, not the mechanism.
- **Rate limiting** (in-memory token buckets, keyed by IP and by email):
  login `10 / 15min / (IP+email)`, signup `5 / hour / IP`, with a fixed
  `429` + `Retry-After`. In-memory is fine: single instance, self-hosted.
- **Enumeration & timing:** login failure message is always `Invalid email or password.`; unknown email still runs Argon2id against a fixed dummy hash so timing doesn't distinguish the cases. Signup with an existing email returns the same generic "check your details" path.
- **Password policy:** minimum 12 characters, maximum 128, no composition rules (NIST-style); a length meter, not a "complexity" meter.

### 5.2 Registration policy (self-hosted single-team)

- **First signup bootstraps the instance**: the first user created becomes `admin`.
- After that, **signup is closed by default** (`403` with "Ask your admin for an invite"). Env `CONSOLE_ALLOW_SIGNUP=true` reopens it; admins can also create users from Settings. This is the correct posture for a self-hosted security console — an internet-exposed open signup on a webhook admin panel is a hole.
- **No email verification / no SMTP dependency.** Password reset is an operator action: `./console reset-password <email>` prints a one-time reset URL to stdout. (Email flows are future work, §11.)

### 5.3 Flows

- **Signup** `GET /signup` → form (email, password ×2) → `POST /signup` → validate, hash, create user (+admin if first), create session, `303 → /dashboard`. If signups closed: static 403 page (`siggy-reject`).
- **Login** `GET /login` → `POST /login` → rate-limit check → verify hash → rotate/create session → `303 → /dashboard` (or `?next=` if it's a safe local path).
- **Logout** `POST /logout` (CSRF-protected) → delete session row, clear cookie, `303 → /`.
- **Auth middleware:** every `/dashboard/*` and mutating `/api/*` route loads the session, bumps `last_seen_at` (throttled to once per minute), injects `user` into template context; missing/expired → `303 /login?next=…`.
- **Auth event log:** login success/failure, logout, session revoke, password change → `auth_events` table, surfaced read-only in Settings.

### 5.4 Headers & transport

All responses: `Content-Security-Policy: default-src 'self'` (no inline scripts —
htmx config via attributes; the one theme-toggle script is an external file),
`X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`,
`X-Frame-Options: DENY`. TLS is terminated in front (same rule as the gateway,
PRODUCTION.md §1); cookies are `Secure`, and the compose file notes the reverse-proxy
requirement.

---

## 6. Dashboard (Console) design

### 6.1 Information architecture & layout

Left sidebar (collapsible to icons at <1024px, bottom tab bar at <640px):

```
┌───────────────────────────────────────────────────────┐
│ [siggy-badge] HookGuard          ●live   user@… ▾     │  top bar
├──────────┬────────────────────────────────────────────┤
│ Overview │                                            │
│ Endpoints│                main content                │
│ Live Logs│                                            │
│ Providers│                                            │
│ Settings │                                            │
├──────────┴────────────────────────────────────────────┤
│ gateway: connected · last event 3s ago · v0.4         │  status strip
└───────────────────────────────────────────────────────┘
```

The **status strip** is the trust indicator: `connected` means the Console has
received a gateway event or heartbeat within 60s; otherwise `no signal from
gateway` in `--warn` with a link to the ingest-setup doc.

### 6.2 Screens

**Overview** (`/dashboard`)
- Four stat cards (24h, tabular numerals): `Accepted`, `Rejected`, `Accept rate`, `p50 verify+forward latency`.
- Area chart: events/hour over 24h/7d toggle, stacked accepted (green) / rejected (red). Rendered as server-generated inline SVG — no charting library.
- "Recent rejections" table (last 10): time, provider, path, **reason** (`signature mismatch`, `stale timestamp`, `missing header`, `cert host rejected`…), remote IP. Reasons are first-class — this is the product.
- Empty state (no events ever): `siggy-sleep` + "No traffic at the gate yet" + a copyable `curl` snippet from the README quick-start to fire a test webhook.

**Endpoints** (`/dashboard/endpoints`) — CRUD over Routes.
- Table: path, provider (logo chip), upstream URL, replay window, secret env var name / webhook ID, active toggle, 24h sparkline, ⋮ menu (edit, disable, delete).
- **Create/edit** (`/new`, `/{id}/edit`): provider select drives the form shape — HMAC providers show `secret_env` (the **name** of the env var; a bold callout repeats the project rule: *secrets live in the environment, never in this database*); PayPal shows `webhook_id` (labeled "subscription ID — config, not a secret") and hides `secret_env`; Stripe defaults `replay_window` to `5m`. Validation mirrors `buildVerifier`'s per-provider rules so the UI can't produce a config the gateway would refuse to boot.
- **Apply flow (v1, deliberately explicit):** header button `Export config.json` → downloads/regenerates the gateway config from the DB; a diff view shows current-file vs DB before export; toast reminds "Restart the gateway to apply." A parity test guarantees exported JSON round-trips against the existing `config.json` schema. (Hot-reload is future work, §11.)
- Delete = type-the-path confirm modal (`--reject` button).

**Live Logs** (`/dashboard/logs`)
- Streaming table via **SSE** (`/dashboard/logs/stream`): time, provider chip, path, verdict pill (✓ accepted / ✕ rejected + words), reason, upstream status, latency ms, body size, remote IP.
- Filters (querystring-backed, shareable URLs): provider, verdict, reason class, time range, path. Pause/resume button; new-rows counter while paused.
- Row click → detail drawer: full event record, the signature-relevant headers as received (e.g. `Stripe-Signature: t=…, v1=…`), and `body_sha256`. **Body content is not stored** (§8 rationale) — the drawer says so and links to the "capture" open question.
- Empty state: `siggy-sleep`; error state (SSE dropped): `--warn` banner "Live stream reconnecting…" with htmx/EventSource auto-retry.

**Providers** (`/dashboard/providers`)
- Four cards = setup guides: where in that provider's dashboard the secret lives (mirrors PRODUCTION.md §2–4), which headers HookGuard checks, that provider's reject-reason taxonomy, and provider-filtered 24h stats. PayPal card carries the sandbox smoke-test checklist verbatim from PRODUCTION.md §4.

**Settings** (`/dashboard/settings`)
- Profile: email, change password (requires current password; rotates session).
- Sessions: active sessions table (created, last seen, IP, UA) with per-row and "revoke all others" actions.
- Users (admin only): list, create user, deactivate; signup-open toggle mirrors the env default.
- Security log: read-only `auth_events`.
- Instance: retention window for events (default 30 days), data-file location, version, link to PRODUCTION checklist. Internal-secret **rotation guidance** text (the secret itself is env-only, never shown or stored).

### 6.3 Interaction model

htmx drives all in-page mutation (form posts → partial re-render of the affected
fragment); SSE drives the live log; everything works without JS except the live
stream (the logs page degrades to a "refresh" meta-link). URL is the state:
filters, pagination and tabs are querystring-backed so every view is bookmarkable.

---

## 7. System architecture

### 7.1 Shape: two binaries, one repo, arrow points one way

```
                    ┌────────────────────────────── internet ──┐
 providers ──POST──►│ gateway :9000  (zero-dep, unchanged core) │
                    └───────┬───────────────────────┬──────────┘
                            │ forwards verified     │ POSTs verdict events
                            │ (Gateway signature)   │ (Gateway-signature signed,
                            ▼                       ▼  async, best-effort, env-gated)
                    ┌──────────────┐        ┌─────────────────────────┐
                    │ upstream:8080│        │ console :7000  (/web)   │◄── browser
                    │ (protected)  │        │ landing + auth + dash   │    (users)
                    └──────────────┘        │ SQLite: /data/console.db│
                                            └─────────────────────────┘
```

- **`console`** is a new Go binary from the new module at `/web`. It serves the
  landing page (public), auth, the dashboard, and one machine endpoint:
  `POST /api/v1/ingest` for gateway events.
- **Dependency direction is one-way:** `web → gateway module`, never the reverse.
  The gateway does not know the Console exists except through one optional env var.

### 7.2 Zero-dep isolation (the non-negotiable)

- `/web/go.mod` declares **`module hookguard/web`** — a **nested Go module**. The
  Go toolchain excludes nested-module directories from the parent build, so
  *nothing under `/web` can appear in the gateway's dependency graph, structurally*.
- Web module's third-party deps, exactly two, pinned:
  `modernc.org/sqlite` (pure-Go SQLite, no CGo — keeps the static-binary,
  easy-cross-compile story) and `golang.org/x/crypto` (Argon2id). htmx is a
  **vendored static file** (`ui/static/js/htmx.min.js`, ~14KB gz) — no npm, no
  Node, no lockfile ecosystem enters the repo.
- Web module reuses gateway code via
  `require hookguard v0.0.0` + `replace hookguard => ../` in `/web/go.mod`.
  It imports **`hookguard/internal/gatewaysig`** (legal: the importer's path
  `hookguard/web/...` is inside the `hookguard/` internal-visibility tree) to
  verify ingest signatures with the *same code* the gateway signs with — no
  drift, no copy.
- **CI guard (extends the existing check):**
  `go list -deps . | grep -vE '^(hookguard|internal/|std list…)'` at repo root
  must stay empty, and a new assertion that root `go.mod` gained no `require`
  lines beyond the two test-only oracles. The claim stays *checked*, not asserted.
- A top-level `go.work` (git-ignored or committed — implementer's choice, note in
  M0) makes both modules build in one editor workspace.

### 7.3 The one gateway change: verdict events (env-gated, stdlib-only)

The gateway currently logs nothing per-request and is "done"; this is the
minimal, reversible touchpoint (product-owner approval required — §11 Q1):

- New optional env `EVENTS_URL`. **Unset (default): the gateway's behavior is
  byte-for-byte today's behavior.** Set: after each verify decision, the handler
  enqueues an event onto a buffered channel (size 256, **drop-oldest on overflow**
  — telemetry must never block or backpressure the hot path) consumed by one
  goroutine that POSTs JSON to `EVENTS_URL`:

  ```json
  {
    "ts": "2026-07-02T12:34:56.789Z",
    "path": "/hook/stripe",
    "provider": "stripe",
    "verdict": "rejected",
    "reason": "signature mismatch",
    "upstream_status": 0,
    "latency_ms": 2,
    "body_bytes": 214,
    "body_sha256": "9f86d0…",
    "remote_ip": "203.0.113.7"
  }
  ```

- The POST carries the **Gateway signature** headers (`gatewaysig.Sign(INTERNAL_SECRET,
  "console-ingest", eventJSON)`) — reusing the existing primitive; the Console
  verifies with the shared package. No new secret is introduced.
- Failure mode: fire-and-forget with a 2s client timeout; delivery failures are
  counted and logged once per minute, never surfaced to the webhook sender.
  **The gateway's verdict path is completely independent of the Console being up.**
- Reason strings become part of the event contract; verifiers already return
  distinct errors — the emitter maps them to a small stable taxonomy
  (`missing header`, `signature mismatch`, `stale timestamp`, `cert host rejected`,
  `cert chain invalid`, `unsupported algorithm`, `bad encoding`).
- Estimated size: ~40 lines in a new `events.go` at root, stdlib only
  (`net/http`, `encoding/json`, `time`). Covered by a unit test with an
  `httptest` collector, plus a test that `EVENTS_URL` unset emits nothing.

### 7.4 HTTP surface of the Console

**Public HTML** — `GET /` (landing), `GET /login`, `GET /signup`, `GET /healthz` (200 + version).
**Auth actions** — `POST /login`, `POST /signup`, `POST /logout`.
**Dashboard HTML** (session-gated) —
`GET /dashboard` · `GET /dashboard/endpoints` · `GET /dashboard/endpoints/new` ·
`POST /dashboard/endpoints` · `GET /dashboard/endpoints/{id}/edit` ·
`PUT /dashboard/endpoints/{id}` · `DELETE /dashboard/endpoints/{id}` ·
`GET /dashboard/endpoints/export` (→ `config.json` download) ·
`GET /dashboard/logs` · `GET /dashboard/logs/stream` (SSE) ·
`GET /dashboard/providers` · `GET /dashboard/settings` ·
`POST /dashboard/settings/password` · `POST /dashboard/settings/sessions/{id}/revoke` ·
`POST /dashboard/settings/users` (admin).
**Machine JSON** —
`POST /api/v1/ingest` (Gateway-signature auth, not session auth) ·
`GET /api/v1/stats/summary?window=24h` (session auth; feeds the overview cards via htmx polling every 30s).
Routing: Go 1.22+ stdlib `ServeMux` method patterns (`"GET /dashboard/endpoints/{id}/edit"`) — no router dependency.

### 7.5 Frontend stack decision (with the tradeoff called explicitly)

**Chosen: Go `html/template` + htmx (vendored) + hand-written CSS tokens + SSE.**

- *Why not Next.js/React+Tailwind/shadcn:* it would produce a slicker component
  library faster, but it imports an entire second toolchain (Node, npm, bundler,
  lockfiles, transitive JS supply chain) into a project whose **thesis is a
  provably dependency-free security binary**. A landing page that says "zero
  dependencies, nothing to typosquat" sitting on 1,200 npm packages is a
  credibility self-own — and in a viva, the examiner gets to ask about it.
- *Why not Astro islands:* better than a SPA for a landing page, but still Node,
  and the dashboard (the harder surface) gains nothing from it.
- *What we give up:* prebuilt component polish and client-side interactivity
  ceiling. Acceptable: the dashboard is tables, forms, one chart (server-rendered
  SVG) and a stream (SSE — a *better* fit than client polling anyway). What we
  gain: one language, one binary, `go build` is the entire frontend build, CSP
  `default-src 'self'` with zero exceptions, and the whole web tier stays
  reviewable by the same person who audits the gateway.

### 7.6 Deployment — extend the existing compose

Add one service to `docker-compose.yml` (gateway/upstream untouched except the
optional `EVENTS_URL` env):

```yaml
  console:
    build: { context: ./web, dockerfile: Dockerfile }
    ports: ["7000:7000"]
    environment:
      INTERNAL_SECRET: ${INTERNAL_SECRET:?set INTERNAL_SECRET}   # verifies ingest
      CONSOLE_DATA_DIR: /data
      CONSOLE_ALLOW_SIGNUP: "false"
    volumes: [console-data:/data]
    networks: [internal]

  gateway:
    environment:
      EVENTS_URL: http://console:7000/api/v1/ingest   # optional; omit to disable
volumes:
  console-data:
```

Same TLS rule as the gateway (PRODUCTION.md §1): a reverse proxy terminates HTTPS
for `hooks.your-domain.com` (→ gateway:9000) and `console.your-domain.com`
(→ console:7000). The Console is `Secure`-cookie only, so it effectively requires
that proxy in production. `web/Dockerfile` mirrors the gateway's: multi-stage,
`CGO_ENABLED=0` (possible because `modernc.org/sqlite` is pure Go), distroless/scratch
final stage, single static binary + embedded templates/static via `go:embed`.

---

## 8. Data model

### 8.1 Database choice: **SQLite** (via `modernc.org/sqlite`), not Postgres

- The Console is single-instance by design (self-hosted, one team). One writer,
  low write volume: even 100k webhooks/day ≈ ~1 event/sec — decades below
  SQLite-in-WAL-mode territory.
- Postgres would be a second stateful service to install, secure, back up and
  explain — precisely the operational surface HookGuard's identity rejects.
  SQLite keeps "deploy = one more container + one volume."
- WAL mode + `busy_timeout=5000`; one `*sql.DB` with `SetMaxOpenConns(1)` for
  writes semantics kept simple; ingest batches inserts per 100ms tick.
- Backup story = copy one file (documented in Settings → Instance). Retention
  pruning (below) keeps it bounded.
- Escape hatch: the store is behind a small interface; if a future multi-node
  Console ever exists, Postgres is a store implementation, not a redesign.

### 8.2 Schema (migration `0001_init.sql`, embedded, applied at boot)

```sql
CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  email         TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password_hash TEXT NOT NULL,              -- PHC string: $argon2id$v=19$m=65536,t=3,p=2$...
  role          TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('admin','member')),
  active        INTEGER NOT NULL DEFAULT 1,
  created_at    INTEGER NOT NULL            -- unix ms, everywhere
);

CREATE TABLE sessions (
  id           INTEGER PRIMARY KEY,
  token_hash   BLOB NOT NULL UNIQUE,        -- sha256(cookie token)
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  csrf_token   TEXT NOT NULL,
  created_at   INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL,            -- absolute cap
  ip           TEXT,
  user_agent   TEXT
);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE endpoints (                     -- a Route, DB-backed
  id            INTEGER PRIMARY KEY,
  path          TEXT NOT NULL UNIQUE,        -- "/hook/stripe"
  provider      TEXT NOT NULL CHECK (provider IN ('stripe','github','shopify','paypal')),
  upstream_url  TEXT NOT NULL,
  replay_window TEXT NOT NULL DEFAULT '',    -- Go duration string ("5m") or ''
  secret_env    TEXT NOT NULL DEFAULT '',    -- NAME of env var; never the secret
  webhook_id    TEXT NOT NULL DEFAULT '',    -- PayPal only; config, not a secret
  active        INTEGER NOT NULL DEFAULT 1,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL,
  CHECK ((provider = 'paypal' AND webhook_id <> '' AND secret_env  = '')
      OR (provider <> 'paypal' AND secret_env <> '' AND webhook_id = ''))
);

CREATE TABLE events (                        -- one row per gateway verdict
  id              INTEGER PRIMARY KEY,
  received_at     INTEGER NOT NULL,          -- gateway ts, unix ms
  path            TEXT NOT NULL,
  provider        TEXT NOT NULL,
  verdict         TEXT NOT NULL CHECK (verdict IN ('accepted','rejected')),
  reason          TEXT NOT NULL DEFAULT '',  -- '' when accepted
  upstream_status INTEGER NOT NULL DEFAULT 0,
  latency_ms      INTEGER NOT NULL DEFAULT 0,
  body_bytes      INTEGER NOT NULL DEFAULT 0,
  body_sha256     TEXT NOT NULL DEFAULT '',
  remote_ip       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX events_time     ON events(received_at DESC);
CREATE INDEX events_verdict  ON events(verdict, received_at DESC);
CREATE INDEX events_provider ON events(provider, received_at DESC);

CREATE TABLE event_rollups (                 -- hourly, upserted at ingest
  hour     INTEGER NOT NULL,                 -- unix hour bucket
  provider TEXT    NOT NULL,
  verdict  TEXT    NOT NULL,
  n        INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (hour, provider, verdict)
);

CREATE TABLE auth_events (
  id         INTEGER PRIMARY KEY,
  at         INTEGER NOT NULL,
  user_id    INTEGER,                        -- nullable: failed logins
  email      TEXT NOT NULL DEFAULT '',
  kind       TEXT NOT NULL,                  -- login_ok|login_fail|logout|pw_change|session_revoke|user_create
  ip         TEXT NOT NULL DEFAULT ''
);

CREATE TABLE settings (                      -- instance key/value (retention_days=30, …)
  key TEXT PRIMARY KEY, value TEXT NOT NULL
);
```

**Design points**

- **No webhook bodies stored — deliberate.** Payloads carry payments/PII; a
  security product should not quietly build a payload warehouse next to the gate.
  We store `body_sha256` + size (enough to correlate a dispute) and the verdict
  reason. Per-endpoint "capture last N rejected payloads for debugging" is an
  explicit open question (§11 Q4), off by default if built.
- **No secrets in the DB, ever** — `secret_env` stores the env var *name*, exactly
  the gateway's config rule. The endpoints CHECK constraint encodes the
  provider-shape rule (`paypal ⇒ webhook_id, others ⇒ secret_env`) so the DB
  cannot hold a Route the gateway would refuse.
- **Rollups at ingest** make Overview cards and charts O(hours), not O(events);
  a nightly job deletes `events` older than `retention_days` (default 30) and
  keeps rollups for 13 months.
- `config.json` export = `SELECT … FROM endpoints WHERE active=1 ORDER BY path`,
  serialized to the existing schema, with a golden-file parity test against the
  repo's current `config.json`.

---

## 9. Directory structure under `/web`

```
web/
├── DESIGN.md                     # this document
├── go.mod                        # module hookguard/web  (require hookguard => ../)
├── go.sum
├── Dockerfile                    # multi-stage, CGO_ENABLED=0, static binary
├── cmd/
│   └── console/
│       └── main.go               # flags/env, store open, migrate, serve; `reset-password` subcommand
├── internal/
│   ├── server/                   # ServeMux wiring, middleware (session, CSRF, headers, ratelimit)
│   │   ├── router.go
│   │   ├── middleware.go
│   │   ├── handlers_public.go    # landing, login/signup pages
│   │   ├── handlers_auth.go      # POST login/signup/logout
│   │   ├── handlers_endpoints.go
│   │   ├── handlers_logs.go      # list + SSE stream
│   │   ├── handlers_settings.go
│   │   └── handlers_api.go       # /api/v1/ingest, /api/v1/stats
│   ├── auth/                     # argon2id, sessions, csrf, ratelimit (+_test.go each)
│   ├── store/                    # sqlite open/migrate; users.go sessions.go endpoints.go events.go
│   │   └── migrations/0001_init.sql   (go:embed)
│   ├── ingest/                   # event decode, gatewaysig verify, batcher, rollups
│   └── gwconfig/                 # endpoints → config.json export (+ golden parity test)
├── ui/
│   ├── templates/
│   │   ├── layouts/  base.html dashboard.html
│   │   ├── pages/    landing.html login.html signup.html overview.html
│   │   │             endpoints.html endpoint_form.html logs.html
│   │   │             providers.html settings.html 404.html 403.html
│   │   └── partials/ statcards.html eventrow.html endpoint_row.html flash.html
│   └── static/                   # go:embed, hashed filenames at build
│       ├── css/  tokens.css  app.css  landing.css
│       ├── js/   htmx.min.js  sse.js  theme.js      # vendored + two tiny own files
│       ├── img/  mascot/ siggy-post.svg siggy-approve.svg siggy-reject.svg
│       │                 siggy-inspect.svg siggy-sleep.svg siggy-lost.svg
│       │                 siggy-run.svg siggy-badge.svg
│       │         diagrams/ flow.svg
│       └── fonts/ space-grotesk-*.woff2 inter-*.woff2 jetbrains-mono-*.woff2
└── design/
    └── mascot-siggy.svg          # concept sketch (source of §3.4)
```

Root-level changes (small, approval-gated): `events.go` + `events_test.go`
(gateway emitter, §7.3), `docker-compose.yml` (console service), optional
committed `go.work`.

---

## 10. Phased implementation plan

Each milestone is independently shippable and verifiable; do them in order.
"Gateway guard" = run `go list -deps . | grep -vE 'hookguard|^[a-z]+(/|$)'`-style
zero-dep check at repo root and full `go test ./...` — it must pass at **every**
milestone.

**M0 — Module scaffold & design tokens** *(no product behavior)*
Create `/web` module, `cmd/console` serving `GET /healthz` + a static
placeholder page with `tokens.css` applied; vendored htmx + fonts; Dockerfile;
optional `go.work`.
✅ Verify: `cd web && go build ./... && go test ./...` green; `curl :7000/healthz` → 200;
root gateway guard unchanged; `docker compose build console` succeeds.

**M1 — Store + auth**
Migrations, `store` package, `auth` package (Argon2id, sessions, CSRF, rate
limits), signup/login/logout pages and flows, first-user-is-admin,
signup-closed default, security headers middleware, `reset-password` subcommand,
`auth_events`.
✅ Verify: `httptest` suite covers — signup→login→logout roundtrip; second signup
403 when closed; wrong password generic error; 11th login attempt in 15min → 429;
cookie flags asserted; CSRF-less POST → 403; session revoked ⇒ next request →
login. Manual: full flow in a browser against the Docker container.

**M2 — Dashboard shell + settings**
Auth-gated layout (sidebar, top bar, status strip), Overview with placeholder
zeros + `siggy-sleep` empty state, Settings (password change, sessions list/revoke,
admin user management), 404/403 pages with mascots, dark/light toggle.
✅ Verify: unauthenticated `/dashboard` → redirect; password change rotates
session; revoking a session kills it (two-browser test); axe/Lighthouse a11y pass
≥ 95 on the shell.

**M3 — Endpoint CRUD + config export**
Endpoints table/list, provider-shaped create/edit forms with `buildVerifier`-mirroring
validation, active toggle, delete-with-confirm, `GET /dashboard/endpoints/export`,
seed command importing the repo's `config.json`.
✅ Verify: golden test — import repo `config.json` → export → byte-comparable
(JSON-equal) result; CHECK-constraint tests (paypal without `webhook_id` rejected
at form *and* DB layer); the gateway boots successfully against an exported file
(`go run . ` with exported config in a temp dir — scripted).

**M4 — Ingest + live logs + real stats** *(includes the gateway touchpoint — get §11 Q1 approved first)*
Gateway `events.go` (env-gated emitter + tests); Console `/api/v1/ingest` with
gatewaysig verification; batcher + rollups; Live Logs page with SSE stream,
filters, detail drawer; Overview cards/chart go real; status-strip liveness.
✅ Verify: gateway tests — `EVENTS_URL` unset ⇒ zero requests (httptest collector);
set ⇒ event schema matches; hot path latency unchanged within noise when collector
is down (the drop-oldest test). Console tests — bad signature → 401 + no row;
end-to-end: `demo.sh`-style run with `EVENTS_URL` set shows the demo's 200/401
matrix appear in the live log with correct reasons. Root gateway guard still green.

**M5 — Landing page + mascot set**
All §4 sections with final copy; the 8 Siggy SVGs finalized from the §3.4 sketch;
flow diagram; responsive (375px → 1440px); OG/meta/favicon (siggy-badge).
✅ Verify: Lighthouse ≥ 95 performance & a11y & SEO on `/`; total page weight
< 300KB (no JS frameworks makes this easy); every §4.2 proof-bar claim links to
its receipt; renders sanely with JS disabled.

**M6 — Polish & production pass**
Retention job + Settings control; SSE reconnect UX; empty/error states audit;
CSP verified with zero violations in console; compose finalized + PRODUCTION.md
gains a "Console" section; README gains a screenshot + one paragraph; `demo.sh`
optionally boots the console too.
✅ Verify: `docker compose up --build` from a clean clone → landing at :7000,
signup, seed endpoints, fire README's curl → event visible in live log within 1s;
restart container → data persists (volume); repo-root zero-dep guard + full test
suite green one final time.

---

## 11. Risks & open questions (product-owner input wanted)

1. **[Decision needed — blocks M4] Gateway touchpoint.** The verdict-event
   emitter (§7.3) is the only change to the "finished" gateway: ~40 stdlib-only
   lines, env-gated off by default, tested. Without it, the dashboard has no live
   data (alternative: log-file tailing — uglier, fragile in Docker, and *still*
   touches the gateway to emit structured lines). **Recommend: approve the emitter.**
2. **[Decision needed — M5] Pricing section.** Ship the "HookGuard Cloud —
   waitlist" card (aspirational but honest), or a single free card? A college
   project inventing a paid tier can read as either ambition or cosplay.
   **Recommend: keep the waitlist card, label it clearly "planned."**
3. **[Decision needed — M3] Config apply flow.** v1 is export + manual gateway
   restart (explicit, honest). Hot-reload (SIGHUP or a signed admin endpoint on
   the gateway) is more product-like but expands the gateway's attack surface and
   contradicts "the gateway is done." **Recommend: v1 manual; revisit later.**
4. **[Open — post-M6] Rejected-payload capture.** Debugging a rejection sometimes
   needs the body. Off-by-default per-endpoint "capture last 20 rejected bodies,
   24h TTL" is useful but creates a PII store. Default answer: **don't build yet.**
5. **Fidelity of "live" stats if the emitter is rejected** (fallback risk for Q1):
   Overview/logs would ship demo-only with seeded data — materially weaker. Flagging
   so Q1 isn't waved through without seeing the cost of "no."
6. **No SMTP** means operator-run password resets (`reset-password` subcommand)
   and no email verification. Fine for self-hosted single-team; revisit only if
   Cloud ever happens.
7. **Console exposure.** The Console is a webhook *admin panel*; docs must be
   loud that it belongs behind the reverse proxy, ideally not on the public
   internet at all (VPN/allowlist). Signup-closed-by-default (§5.2) is the
   backstop.
8. **`replace ../` coupling.** The web module pins the gateway module by relative
   path — correct for a monorepo, but it means the repo must be cloned whole (no
   `go install hookguard/web/...@latest`). Acceptable; note it in the web README.
9. **Font licensing/self-hosting** — all three faces are OFL; woff2 files are
   committed (~300KB). Confirm repo-size tolerance.
10. **Mascot refinement.** §3.4 is a real but rough vector; one polish pass
    (consistent stroke joins, pose variants) is budgeted inside M5 — flag now if
    outside art is preferred instead.
