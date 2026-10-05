#!/bin/bash
# Configures Cloudflare for doesitomarchy.com (PLAN.md §18.3). Safe to re-run:
# every step looks up what exists and updates it in place.
#
#   tunnel "doiomad" → http://127.0.0.1:8080, DNS for the apex and www,
#   www → apex redirect, HTML cache rule, search rate limit, zone settings,
#   Cloudflare Access (the whole site only with ACCESS=on; /admin always), and
#   the tunnel token and Access settings on the droplet.
#
# Environment:
#   CF_API_TOKEN    API token (permissions in deploy/RUNBOOK.md)
#   CF_ACCOUNT_ID   Cloudflare account ID
#   ACCESS=on       put the site behind Cloudflare Access (private). The default,
#                   since launch on 2026-10-01, is public: any Access app is removed.
#   ACCESS_EMAILS   with ACCESS=on: space-separated emails allowed through
#   ADMIN_EMAILS    space-separated emails allowed into /admin, the review queue
#                   (PLAN.md §22.5). Unset: the /admin Access app is left as it is.
#   CF_ACCESS_TEAM  the Zero Trust team name, if the API token can't read it
#   DROPLET         droplet name (default doiomad-1); ADMIN_USER, ADMIN_KEY, DO_CONTEXT as in provision.sh
set -euo pipefail

ZONE_NAME=doesitomarchy.com
TUNNEL_NAME=doiomad
ORIGIN=http://127.0.0.1:8080
: "${CF_API_TOKEN:?}" "${CF_ACCOUNT_ID:?}"
API=https://api.cloudflare.com/client/v4
ACCT=$API/accounts/$CF_ACCOUNT_ID

step() { echo "==> $*"; }

# cf METHOD PATH [JSON]: call the API and print .result, failing on errors.
cf() {
	local out
	out=$(curl -sS -X "$1" "$2" -H "Authorization: Bearer $CF_API_TOKEN" -H "Content-Type: application/json" ${3:+--data "$3"})
	if ! jq -e .success >/dev/null <<<"$out"; then
		echo "Cloudflare API: $1 $2 failed:" >&2
		jq -c '.errors' <<<"$out" >&2 || echo "$out" >&2
		return 1
	fi
	jq -c .result <<<"$out"
}

step "zone"
zone=$(cf GET "$API/zones?name=$ZONE_NAME" | jq -r '.[0].id // empty')
[ -n "$zone" ] || { echo "zone $ZONE_NAME not found in this account" >&2; exit 1; }
status=$(cf GET "$API/zones/$zone" | jq -r .status)
echo "    $zone ($status)"
[ "$status" = active ] || echo "    note: the zone is $status; it becomes active once the nameservers at Spaceship point to Cloudflare"

step "tunnel $TUNNEL_NAME"
tunnel=$(cf GET "$ACCT/cfd_tunnel?name=$TUNNEL_NAME&is_deleted=false" | jq -r '.[0].id // empty')
if [ -z "$tunnel" ]; then
	tunnel=$(cf POST "$ACCT/cfd_tunnel" "{\"name\":\"$TUNNEL_NAME\",\"config_src\":\"cloudflare\"}" | jq -r .id)
fi
echo "    $tunnel"
cf PUT "$ACCT/cfd_tunnel/$tunnel/configurations" "$(jq -nc --arg o "$ORIGIN" --arg z "$ZONE_NAME" '{config: {ingress: [
	{hostname: $z, service: $o},
	{hostname: ("www." + $z), service: $o},
	{service: "http_status:404"}]}}')" >/dev/null

step "DNS"
for name in "$ZONE_NAME" "www.$ZONE_NAME"; do
	body=$(jq -nc --arg n "$name" --arg c "$tunnel.cfargotunnel.com" '{type: "CNAME", name: $n, content: $c, proxied: true, comment: "Cloudflare Tunnel doiomad"}')
	id=$(cf GET "$API/zones/$zone/dns_records?name=$name" | jq -r '.[0].id // empty')
	if [ -n "$id" ]; then cf PUT "$API/zones/$zone/dns_records/$id" "$body" >/dev/null; else cf POST "$API/zones/$zone/dns_records" "$body" >/dev/null; fi
	echo "    $name → tunnel"
done

step "zone settings"
for kv in always_use_https:on min_tls_version:1.2 ssl:full brotli:on; do
	cf PATCH "$API/zones/$zone/settings/${kv%%:*}" "{\"value\":\"${kv#*:}\"}" >/dev/null
done

# rules PHASE JSON_RULES: replace the zone's rules for one phase (we own them all).
rules() {
	cf PUT "$API/zones/$zone/rulesets/phases/$1/entrypoint" "{\"rules\":$2}" >/dev/null
}

step "www → apex redirect"
rules http_request_dynamic_redirect "$(jq -nc --arg z "$ZONE_NAME" '[{
	description: "www to apex", action: "redirect",
	expression: ("http.host eq \"www." + $z + "\""),
	action_parameters: {from_value: {status_code: 301, preserve_query_string: true,
		target_url: {expression: ("concat(\"https://" + $z + "\", http.request.uri.path)")}}}}]')"

step "cache rules"
# HTML is cacheable for the s-maxage the origin sends (300 s); /healthz, the
# API, the MCP server (/mcp), /admin, GitHub's webhook (/hooks/) and HTMX
# requests are never cached.
# HTMX fragments share their URL with the full page and Cloudflare ignores
# Vary, so a cached full page would be swapped into the page as a "fragment"
# (and vice versa).
# Static files cache by their own headers.
nocache='http.request.uri.path eq "/healthz" or starts_with(http.request.uri.path, "/api/") or http.request.uri.path eq "/mcp" or starts_with(http.request.uri.path, "/admin") or starts_with(http.request.uri.path, "/hooks/") or any(http.request.headers["hx-request"][*] eq "true")'
rules http_request_cache_settings "$(jq -nc --arg z "$ZONE_NAME" --arg nc "$nocache" '[
	{description: "no cache: health, API, MCP, admin, webhooks, HTMX fragments", action: "set_cache_settings", action_parameters: {cache: false},
	 expression: ("http.host eq \"" + $z + "\" and (" + $nc + ")")},
	{description: "cache pages per origin headers", action: "set_cache_settings",
	 action_parameters: {cache: true, edge_ttl: {mode: "respect_origin"}, browser_ttl: {mode: "respect_origin"}},
	 expression: ("http.host eq \"" + $z + "\" and not (" + $nc + ")")}]')"

step "rate limit on search"
rules http_ratelimit "$(jq -nc '[{
	description: "search and suggestions", action: "block",
	expression: "starts_with(http.request.uri.path, \"/search\")",
	ratelimit: {characteristics: ["ip.src", "cf.colo.id"], period: 10, requests_per_period: 60, mitigation_timeout: 10}}]')"

step "Access"
apps=$(cf GET "$ACCT/access/apps")
app=$(jq -r --arg z "$ZONE_NAME" '.[] | select(.domain == $z) | .id' <<<"$apps" | head -n1)
if [ "${ACCESS:-off}" != on ]; then
	[ -z "$app" ] || cf DELETE "$ACCT/access/apps/$app" >/dev/null
	echo "    removed: the site is public"
else
	: "${ACCESS_EMAILS:?set ACCESS_EMAILS (who may see the site while it is private)}"
	body=$(jq -nc --arg z "$ZONE_NAME" --arg e "$ACCESS_EMAILS" '{
		name: "DoesItOmarchy (private)", type: "self_hosted", domain: $z,
		destinations: [{type: "public", uri: $z}, {type: "public", uri: ("www." + $z)}],
		session_duration: "720h", app_launcher_visible: false,
		policies: [{name: "maintainers", decision: "allow",
			include: [$e | split(" ") | map(select(. != "")) | .[] | {email: {email: .}}]}]}')
	if [ -n "$app" ]; then cf PUT "$ACCT/access/apps/$app" "$body" >/dev/null; else cf POST "$ACCT/access/apps" "$body" >/dev/null; fi
	echo "    only $ACCESS_EMAILS (one-time email code)"
fi

step "Access for /admin"
# The review queue: only maintainers, by one-time email code. The server also
# checks Access's signed token, so it needs the team name and this app's
# audience tag (CF_ACCESS_TEAM, CF_ACCESS_AUD in /etc/doiomad/doiomad.env).
admin_app=$(jq -r --arg d "$ZONE_NAME/admin" '.[] | select(.domain == $d) | .id' <<<"$apps" | head -n1)
admin_env=
if [ -n "${ADMIN_EMAILS:-}" ]; then
	body=$(jq -nc --arg d "$ZONE_NAME/admin" --arg e "$ADMIN_EMAILS" '{
		name: "DoesItOmarchy admin", type: "self_hosted", domain: $d,
		destinations: [{type: "public", uri: $d}],
		session_duration: "24h", app_launcher_visible: false,
		policies: [{name: "maintainers", decision: "allow",
			include: [$e | split(" ") | map(select(. != "")) | .[] | {email: {email: .}}]}]}')
	if [ -n "$admin_app" ]; then
		aud=$(cf PUT "$ACCT/access/apps/$admin_app" "$body" | jq -r .aud)
	else
		aud=$(cf POST "$ACCT/access/apps" "$body" | jq -r .aud)
	fi
	team=${CF_ACCESS_TEAM:-$(cf GET "$ACCT/access/organizations" 2>/dev/null | jq -r '.auth_domain // empty' | sed 's/\.cloudflareaccess\.com$//' || true)}
	[ -n "$team" ] || { echo "can't read the Zero Trust team name; set CF_ACCESS_TEAM (Zero Trust → Settings → Team name)" >&2; exit 1; }
	admin_env=$(printf 'CF_ACCESS_TEAM=%s\nCF_ACCESS_AUD=%s\n' "$team" "$aud")
	echo "    /admin: only $ADMIN_EMAILS (team $team)"
	echo "    add each as a maintainer on the droplet: doiomad maintainers add HANDLE EMAIL"
elif [ -n "$admin_app" ]; then
	echo "    /admin app exists; set ADMIN_EMAILS to update it"
else
	echo "    no /admin app (set ADMIN_EMAILS); /admin answers 503 until it exists"
fi

step "tunnel token → droplet"
token=$(cf GET "$ACCT/cfd_tunnel/$tunnel/token" | jq -r .)
name=${DROPLET:-doiomad-1}
ip=$(doctl --context "${DO_CONTEXT:-doesitomarchy}" compute droplet list --tag-name doiomad --format Name,PublicIPv4 --no-header | awk -v n="$name" '$1==n {print $2}')
[ -n "$ip" ] || { echo "droplet $name not found" >&2; exit 1; }
printf 'TUNNEL_TOKEN=%s\n' "$token" | ssh -o IdentitiesOnly=yes -i "${ADMIN_KEY:-$HOME/.ssh/ThinkPad_om_key.pub}" "${ADMIN_USER:-$USER}@$ip" \
	"sudo install -m 0600 /dev/stdin /etc/doiomad/tunnel.env && sudo systemctl enable --now cloudflared && sudo systemctl restart cloudflared"
echo "    cloudflared running on $name ($ip)"
if [ -n "$admin_env" ]; then
	# Merge into doiomad.env, keeping the other settings (the purge token).
	printf '%s\n' "$admin_env" | ssh -o IdentitiesOnly=yes -i "${ADMIN_KEY:-$HOME/.ssh/ThinkPad_om_key.pub}" "${ADMIN_USER:-$USER}@$ip" \
		"sudo sh -c 'umask 077; f=/etc/doiomad/doiomad.env; { [ -f \$f ] && grep -v -E \"^CF_ACCESS_(TEAM|AUD)=\" \$f; cat; } > \$f.tmp && mv \$f.tmp \$f' && sudo systemctl try-restart doiomad"
	echo "    Access settings in /etc/doiomad/doiomad.env; doiomad restarted"
fi

echo
echo "Zone ID for the GitHub secret CF_ZONE_ID: $zone"
