#!/bin/bash
# Creates (or reconfigures) the doiomad droplet on DigitalOcean and runs
# setup.sh on it (PLAN.md §18, deploy/RUNBOOK.md). Safe to re-run.
#
#   deploy/provision.sh [NAME]          default name: doiomad-1
#
# Environment:
#   DO_CONTEXT     doctl profile (default: doesitomarchy)
#   DO_PROJECT     DigitalOcean project for the droplet (default: DoesItOmarchy; must exist)
#   PRIMARY        the production droplet's name (default: doiomad-1). Only it
#                  replicates to R2; any other NAME restores from R2 and stops there.
#   ADMIN_USER     admin login on the droplet (default: $USER)
#   ADMIN_KEY      admin public key (default: ~/.ssh/ThinkPad_om_key.pub)
#   DEPLOY_KEY     deploy private key, created if missing (default: ~/.ssh/doesitomarchy_deploy)
#   LITESTREAM_BUCKET, LITESTREAM_ENDPOINT, LITESTREAM_ACCESS_KEY_ID,
#   LITESTREAM_SECRET_ACCESS_KEY   R2 replica; written to the droplet when set
#   SET_GITHUB_SECRETS=1           also store DEPLOY_HOST/KEY/KNOWN_HOSTS in GitHub
set -euo pipefail
cd "$(dirname "$0")"

PRIMARY=${PRIMARY:-doiomad-1}
NAME=${1:-$PRIMARY}
REPLICATE=0
[ "$NAME" = "$PRIMARY" ] && REPLICATE=1
CTX=${DO_CONTEXT:-doesitomarchy}
ADMIN_USER=${ADMIN_USER:-$USER}
ADMIN_KEY=${ADMIN_KEY:-$HOME/.ssh/ThinkPad_om_key.pub}
DEPLOY_KEY=${DEPLOY_KEY:-$HOME/.ssh/doesitomarchy_deploy}
REPO=doesitomarchy/doesitomarchy
FIREWALL=doiomad-ssh
PROJECT=${DO_PROJECT:-DoesItOmarchy}
TAG=doiomad

doctl() { command doctl --context "$CTX" "$@"; }
step() { echo "==> $*"; }

[ -f "$ADMIN_KEY" ] || { echo "no admin key at $ADMIN_KEY" >&2; exit 1; }
fp=$(ssh-keygen -E md5 -lf "$ADMIN_KEY" | awk '{print $2}' | sed 's/^MD5://')
doctl compute ssh-key list --format FingerPrint --no-header | grep -qx "$fp" ||
	{ echo "admin key $fp is not registered with DigitalOcean" >&2; exit 1; }

project=$(doctl projects list --format ID,Name --no-header | awk -v p="$PROJECT" '{id=$1; $1=""; sub(/^ /,"")} $0==p {print id}')
[ -n "$project" ] || { echo "DigitalOcean project \"$PROJECT\" not found" >&2; exit 1; }

if [ ! -f "$DEPLOY_KEY" ]; then
	step "creating deploy key $DEPLOY_KEY"
	ssh-keygen -q -t ed25519 -N "" -C "doiomad deploy (GitHub Actions)" -f "$DEPLOY_KEY"
fi

ip=$(doctl compute droplet list --tag-name $TAG --format Name,PublicIPv4 --no-header | awk -v n="$NAME" '$1==n {print $2}')
if [ -z "$ip" ]; then
	step "creating droplet $NAME (tor1, s-1vcpu-1gb, debian-13-x64)"
	ud=$(mktemp)
	trap 'rm -f "$ud"' EXIT
	sed -e "s|ADMIN_USER|$ADMIN_USER|" -e "s|ADMIN_KEY|$(cat "$ADMIN_KEY")|" cloud-init.yaml >"$ud"
	doctl compute droplet create "$NAME" --region tor1 --size s-1vcpu-1gb --image debian-13-x64 \
		--ssh-keys "$fp" --tag-name $TAG --project-id "$project" --enable-monitoring --enable-ipv6 \
		--user-data-file "$ud" --wait >/dev/null
	ip=$(doctl compute droplet list --tag-name $TAG --format Name,PublicIPv4 --no-header | awk -v n="$NAME" '$1==n {print $2}')
fi
echo "    $NAME is at $ip"
# Also moves a droplet created before the project setting existed.
id=$(doctl compute droplet list --tag-name $TAG --format Name,ID --no-header | awk -v n="$NAME" '$1==n {print $2}')
doctl projects resources assign "$project" --resource="do:droplet:$id" >/dev/null

if ! doctl compute firewall list --format Name --no-header | grep -qx $FIREWALL; then
	step "creating firewall $FIREWALL (inbound SSH only)"
	doctl compute firewall create --name $FIREWALL --tag-names $TAG \
		--inbound-rules "protocol:tcp,ports:22,address:0.0.0.0/0,address:::/0" \
		--outbound-rules "protocol:tcp,ports:all,address:0.0.0.0/0,address:::/0 protocol:udp,ports:all,address:0.0.0.0/0,address:::/0 protocol:icmp,address:0.0.0.0/0,address:::/0" >/dev/null
fi

# Offer only the admin key: the droplet allows 3 auth attempts and an agent
# with several keys would use them up. -i takes the public key; the private
# key can stay in the agent.
ssh_opts=(-o StrictHostKeyChecking=accept-new -o ConnectTimeout=5 -o IdentitiesOnly=yes -i "$ADMIN_KEY")
step "waiting for SSH and cloud-init"
for _ in $(seq 60); do
	ssh "${ssh_opts[@]}" "$ADMIN_USER@$ip" true 2>/dev/null && break
	sleep 5
done
ssh "${ssh_opts[@]}" "$ADMIN_USER@$ip" "cloud-init status --wait >/dev/null; true"

if [ -n "${LITESTREAM_ACCESS_KEY_ID:-}" ]; then
	step "writing R2 credentials"
	printf 'LITESTREAM_BUCKET=%s\nLITESTREAM_ENDPOINT=%s\nLITESTREAM_ACCESS_KEY_ID=%s\nLITESTREAM_SECRET_ACCESS_KEY=%s\n' \
		"${LITESTREAM_BUCKET:?}" "${LITESTREAM_ENDPOINT:?}" "$LITESTREAM_ACCESS_KEY_ID" "${LITESTREAM_SECRET_ACCESS_KEY:?}" |
		ssh "${ssh_opts[@]}" "$ADMIN_USER@$ip" "sudo install -d -m 0750 /etc/doiomad && sudo install -m 0640 -g root /dev/stdin /etc/doiomad/litestream.env.tmp && sudo mv /etc/doiomad/litestream.env.tmp /etc/doiomad/litestream.env"
fi

step "running setup.sh"
tar -czf - setup.sh root | ssh "${ssh_opts[@]}" "$ADMIN_USER@$ip" \
	"rm -rf /tmp/doiomad-setup && mkdir /tmp/doiomad-setup && tar -xzf - -C /tmp/doiomad-setup &&
	 sudo ADMIN_USER='$ADMIN_USER' REPLICATE=$REPLICATE DEPLOY_PUBKEY='$(cat "$DEPLOY_KEY.pub")' /tmp/doiomad-setup/setup.sh &&
	 rm -rf /tmp/doiomad-setup"

if [ "${SET_GITHUB_SECRETS:-}" = 1 ]; then
	step "storing deploy secrets in GitHub ($REPO)"
	gh secret set DEPLOY_HOST -R $REPO --body "$ip"
	gh secret set DEPLOY_SSH_KEY -R $REPO <"$DEPLOY_KEY"
	hostkey=$(ssh "${ssh_opts[@]}" "$ADMIN_USER@$ip" cat /etc/ssh/ssh_host_ed25519_key.pub | awk -v h="$ip" '{print h, $1, $2}')
	[ -n "$hostkey" ] || { echo "could not read the droplet's host key" >&2; exit 1; }
	gh secret set DEPLOY_KNOWN_HOSTS -R $REPO --body "$hostkey"
fi
step "done: ssh $ADMIN_USER@$ip"
