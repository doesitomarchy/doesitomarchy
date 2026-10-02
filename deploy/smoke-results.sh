#!/bin/bash
# Release smoke test for diagnostic reports (PLAN.md §21.8). Runs on the droplet against
# a THROWAWAY COPY of the production database: a second doiomad serves the
# copy on a spare local port, the synthetic fixture is imported, accepted and
# retracted there, and the pages are checked at each step. Production never
# holds test data. Run from the repo root:
#
#   deploy/smoke-results.sh [NAME]      default droplet: doiomad-1
set -euo pipefail
cd "$(dirname "$0")/.."
NAME=${1:-doiomad-1}
CTX=${DO_CONTEXT:-doesitomarchy}
ADMIN_USER=${ADMIN_USER:-$USER}
ADMIN_KEY=${ADMIN_KEY:-$HOME/.ssh/ThinkPad_om_key.pub}
FIXTURE=internal/results/fixtures/mbp152-synthetic.yaml
ip=$(doctl --context "$CTX" compute droplet list --tag-name doiomad --format Name,PublicIPv4 --no-header | awk -v n="$NAME" '$1==n {print $2}')
[ -n "$ip" ] || { echo "droplet $NAME not found" >&2; exit 1; }

remote=$(cat <<'EOF'
set -euo pipefail
T=$(mktemp -d /tmp/doiomad-smoke.XXXXXX)
BIN=/opt/doiomad/current/doiomad
PORT=18080
cleanup() { [ -n "${PID:-}" ] && kill "$PID" 2>/dev/null; rm -rf "$T"; }
trap cleanup EXIT
sqlite3 /var/lib/doiomad/doesitomarchy.db ".backup '$T/smoke.db'"
"$BIN" serve -db "$T/smoke.db" -addr 127.0.0.1:$PORT -watch 1s > "$T/serve.log" 2>&1 &
PID=$!
for _ in $(seq 50); do curl -fs 127.0.0.1:$PORT/healthz >/dev/null && break; sleep 0.2; done
page() { curl -fs "127.0.0.1:$PORT$1"; }
# has PATH TEXT: the page contains TEXT. The page is fetched in full first:
# piping curl into grep -q under pipefail fails whenever grep stops early.
has() { local b; b=$(page "$1") || return 1; grep -qF -- "$2" <<<"$b"; }
wait_for() { # wait_for PATH TEXT [absent]
	for _ in $(seq 40); do
		if has "$1" "$2"; then [ "${3:-}" != absent ] && return 0; else [ "${3:-}" = absent ] && return 0; fi
		sleep 0.5
	done
	echo "FAIL: $1 ${3:-has} '$2'" >&2; exit 1
}
tested_before=$(b=$(page /); grep -o 'class="n">[0-9]*</span> /' <<<"$b" | sed -n 2p)
ID=$(cat "$T/fixture.yaml" | "$BIN" reports import -db "$T/smoke.db" - | awk '/^report [0-9a-f]+ · pending/ {print $2}')
[ -n "$ID" ] || { echo "FAIL: import" >&2; exit 1; }
sleep 2
has /mac/MacBookPro15-2 'href="/report/'"$ID"'"' && { echo "FAIL: a pending report is visible" >&2; exit 1; }
echo "ok   import $ID: pending, nothing visible"
DOIOMAD_DB="$T/smoke.db" "$BIN" reports accept "$ID" >/dev/null
wait_for /mac/MacBookPro15-2 'href="/report/'"$ID"'"'
has /mac/MacBookPro15-2 'Blocked by: Graphics → External display output' || { echo "FAIL: blocker" >&2; exit 1; }
has /report/$ID "Diagnostic Report" || { echo "FAIL: report page" >&2; exit 1; }
has /report/$ID 'C02XG0FDH7JY' && { echo "FAIL: serial number on the report page" >&2; exit 1; }
echo "ok   accept: model page, blocker and report page updated without a restart"
DOIOMAD_DB="$T/smoke.db" "$BIN" reports retract "$ID" -reason "smoke test" >/dev/null
wait_for /mac/MacBookPro15-2 'Latest diagnostic report <a href="/report/'"$ID"'"' absent
tested_after=$(b=$(page /); grep -o 'class="n">[0-9]*</span> /' <<<"$b" | sed -n 2p)
[ "$tested_before" = "$tested_after" ] || { echo "FAIL: coverage did not return ($tested_before → $tested_after)" >&2; exit 1; }
echo "ok   retract: back to the previous state"
cmp -s <(sqlite3 /var/lib/doiomad/doesitomarchy.db "SELECT count(*) FROM results") <(echo 0) || echo "note: production holds real results (untouched)"
echo "PASS (the throwaway copy is removed)"
EOF
)
# Ship the fixture and the script; run as the service user so it can read the database.
{ printf '%s\n' "mkdir -p /tmp/doiomad-smoke-in && cat > /tmp/doiomad-smoke-in/fixture.yaml <<'FIXTURE'"; cat "$FIXTURE"; echo FIXTURE;
  printf '%s\n' "$remote" | sed 's#cat "\$T/fixture.yaml"#cat /tmp/doiomad-smoke-in/fixture.yaml#';
  echo 'rm -rf /tmp/doiomad-smoke-in'; } |
	ssh -o IdentitiesOnly=yes -i "$ADMIN_KEY" "$ADMIN_USER@$ip" 'sudo -u doiomad bash -s'
