#!/usr/bin/env bash
# HookGuard demo: starts the gateway + a sample upstream, then sends webhooks
# that exercise the whole threat model so you can watch accept/reject happen.
# Requires Go and openssl. Run from the repo root: bash demo.sh
set -u
cd "$(dirname "$0")"

EXE=""
case "${OS:-}" in Windows_NT) EXE=".exe" ;; esac

export INTERNAL_SECRET=demo-internal
export STRIPE_SECRET=whsec_demo
export GITHUB_SECRET=gh-demo
export SHOPIFY_SECRET=shop-demo

echo "building gateway + sample upstream..."
go build -o ".gw$EXE" . && go build -o ".up$EXE" ./cmd/upstream || { echo "build failed"; exit 1; }

"./.up$EXE" >/tmp/hg_up.log 2>&1 & UP=$!
"./.gw$EXE" >/tmp/hg_gw.log 2>&1 & GW=$!
cleanup() { kill "$UP" "$GW" 2>/dev/null; rm -f ".gw$EXE" ".up$EXE"; }
trap cleanup EXIT

# wait for listeners (no sleep): retry until the connection is accepted
curl -s --retry 30 --retry-connrefused --max-time 2 -o /dev/null localhost:8080/ || true

req() { # label, path, header-name, header-val, body
	local code body
	code=$(curl -s -o /tmp/hg_body -w '%{http_code}' --max-time 3 \
		-X POST "localhost:9000$2" -H "$3: $4" --data "$5")
	body=$(tr -d '\n' </tmp/hg_body)
	printf '  %-38s -> HTTP %s  %s\n' "$1" "$code" "$body"
}

BODY='{"id":"evt_demo","amount":4242}'
TS=$(date +%s)
SIG=$(printf '%s' "$TS.$BODY" | openssl dgst -sha256 -hmac "$STRIPE_SECRET" | awk '{print $NF}')
OLD=$((TS - 600))
OLDSIG=$(printf '%s' "$OLD.$BODY" | openssl dgst -sha256 -hmac "$STRIPE_SECRET" | awk '{print $NF}')

echo
echo "=== through the gateway (:9000) — only valid traffic should pass ==="
req "1. valid Stripe webhook"     /hook/stripe "Stripe-Signature" "t=$TS,v1=$SIG"     "$BODY"
req "2. tampered body"            /hook/stripe "Stripe-Signature" "t=$TS,v1=$SIG"     '{"id":"evt_demo","amount":99999}'
req "3. wrong-secret signature"   /hook/stripe "Stripe-Signature" "t=$TS,v1=deadbeef" "$BODY"
req "4. stale timestamp (replay)" /hook/stripe "Stripe-Signature" "t=$OLD,v1=$OLDSIG" "$BODY"

echo
echo "=== attacker bypasses the gateway, hits the upstream (:8080) directly ==="
acode=$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 \
	-X POST localhost:8080/stripe \
	-H "X-HookGuard-Provider: stripe" -H "X-HookGuard-Signature: deadbeef" --data "$BODY")
printf '  %-38s -> HTTP %s\n' "5. forged gateway signature" "$acode"

echo
echo "=== correctness proof: differential harness vs official libraries ==="
if go test -run Differential . >/dev/null 2>&1; then
	echo "  PASS — verifiers agree with stripe-go and go-github on every case"
else
	echo "  FAIL"
fi
echo
echo "(expected: 1 -> 200 ok, 2/3/4/5 -> 401, harness PASS)"
