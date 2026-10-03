#!/usr/bin/env bash
# Puts the fix-tracking GitHub settings (PLAN.md §26) on the server: copies
# GITHUB_TOKEN and GITHUB_WEBHOOK_SECRET from your deploy.env into
# /etc/doiomad/doiomad.env, keeping its other settings, then restarts doiomad.
#
# Usage: deploy/github.sh [ssh-host]   (default host: doiomad, from ~/.ssh/config)
set -euo pipefail
host="${1:-doiomad}"
env_file="${DEPLOY_ENV:-$HOME/.config/doesitomarchy/deploy.env}"
# shellcheck source=/dev/null
source "$env_file"
: "${GITHUB_TOKEN:?add GITHUB_TOKEN=github_pat_… to $env_file}"
: "${GITHUB_WEBHOOK_SECRET:?add GITHUB_WEBHOOK_SECRET=… to $env_file (openssl rand -hex 32)}"

printf 'GITHUB_TOKEN=%s\nGITHUB_WEBHOOK_SECRET=%s\n' "$GITHUB_TOKEN" "$GITHUB_WEBHOOK_SECRET" |
	ssh "$host" "sudo sh -c 'umask 077; f=/etc/doiomad/doiomad.env; { [ -f \$f ] && grep -v -E \"^GITHUB_(TOKEN|WEBHOOK_SECRET)=\" \$f; cat; } > \$f.tmp && mv \$f.tmp \$f && systemctl try-restart doiomad'"
echo "GitHub settings written to /etc/doiomad/doiomad.env on $host; doiomad restarted"
