#!/bin/bash
# Prints how Cloudflare Access is set up for doesitomarchy.com: the app, its
# policies and the account's login methods. No secrets are printed.
#   set -a; . ~/.config/doesitomarchy/deploy.env; set +a; deploy/access-check.sh
set -euo pipefail
: "${CF_API_TOKEN:?}" "${CF_ACCOUNT_ID:?}"
ACCT=https://api.cloudflare.com/client/v4/accounts/$CF_ACCOUNT_ID
get() { curl -sS "$ACCT/$1" -H "Authorization: Bearer $CF_API_TOKEN"; }

echo "== Access apps"
get access/apps | jq 'if .success then .result[] | {id, name, domain, self_hosted_domains, allowed_idps, auto_redirect_to_identity,
	policies: [.policies[]? | {id, name, decision, include, precedence}]} else {errors} end'
echo "== Reusable policies"
get access/policies | jq -c 'if .success then .result[] | {id, name, decision, include, app_count} else {errors} end'
echo "== Login methods"
get access/identity_providers | jq -c 'if .success then (.result | if length == 0 then "none" else .[] | {id, name, type} end) else {errors} end'
echo "== Zero Trust organization"
get access/organizations | jq -c 'if .success then .result | {name, auth_domain} else {errors} end'
