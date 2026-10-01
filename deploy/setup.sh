#!/bin/bash
# Configures a Debian 13 droplet for doiomad (PLAN.md §18). Idempotent: run it
# again after changing anything under deploy/. Run as root from the unpacked
# deploy/ directory; deploy/provision.sh does this for you.
#
#   ADMIN_USER   the admin login created by cloud-init (required)
#   DEPLOY_PUBKEY public key for the deploy user (required on first run)
#   REPLICATE=1   run Litestream (the primary only). Any other server, such as a
#                 restore-rehearsal spare, restores from R2 but must never
#                 write to the same replica.
set -euo pipefail
cd "$(dirname "$0")"

LITESTREAM_VERSION=0.5.17
: "${ADMIN_USER:?set ADMIN_USER}"
[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }
export DEBIAN_FRONTEND=noninteractive

step() { echo "==> $*"; }

step "packages"
apt-get update -q
apt-get install -yq --no-install-recommends ca-certificates curl gnupg sqlite3 unattended-upgrades apt-listchanges

step "cloudflared (Cloudflare's apt repository)"
install -d -m 0755 /usr/share/keyrings
curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg -o /usr/share/keyrings/cloudflare-main.gpg
echo "deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared any main" >/etc/apt/sources.list.d/cloudflared.list
apt-get update -q
apt-get install -yq cloudflared

step "litestream $LITESTREAM_VERSION"
if [ "$(litestream version 2>/dev/null || true)" != "$LITESTREAM_VERSION" ]; then
	deb=litestream-$LITESTREAM_VERSION-linux-x86_64.deb
	url=https://github.com/benbjohnson/litestream/releases/download/v$LITESTREAM_VERSION
	tmp=$(mktemp -d)
	curl -fsSL -o "$tmp/$deb" "$url/$deb"
	curl -fsSL -o "$tmp/checksums.txt" "$url/checksums.txt"
	(cd "$tmp" && grep " $deb\$" checksums.txt | sha256sum -c -)
	apt-get install -yq -o Dpkg::Options::=--force-confold "$tmp/$deb"
	rm -rf "$tmp"
fi

step "swap (1 GB)"
if ! swapon --show=NAME --noheadings | grep -qx /swapfile; then
	[ -f /swapfile ] || { fallocate -l 1G /swapfile; chmod 600 /swapfile; mkswap /swapfile; }
	swapon /swapfile
	grep -q '^/swapfile ' /etc/fstab || echo '/swapfile none swap sw 0 0' >>/etc/fstab
fi
echo 'vm.swappiness=10' >/etc/sysctl.d/60-swappiness.conf
sysctl -q -p /etc/sysctl.d/60-swappiness.conf

step "users and directories"
id doiomad >/dev/null 2>&1 || useradd --system --home-dir /var/lib/doiomad --shell /usr/sbin/nologin doiomad
id deploy >/dev/null 2>&1 || useradd --create-home --shell /bin/sh deploy
install -d -o root -g root -m 0755 /opt/doiomad /opt/doiomad/releases
install -d -o doiomad -g doiomad -m 0750 /var/lib/doiomad /var/lib/doiomad/backups
install -d -o root -g doiomad -m 0750 /etc/doiomad

if [ -n "${DEPLOY_PUBKEY:-}" ]; then
	install -d -o deploy -g deploy -m 0700 ~deploy/.ssh
	echo "restrict,command=\"/usr/local/bin/doiomad-deploy-ssh\" $DEPLOY_PUBKEY" >~deploy/.ssh/authorized_keys
	chown deploy:deploy ~deploy/.ssh/authorized_keys
	chmod 0600 ~deploy/.ssh/authorized_keys
fi
[ -s ~deploy/.ssh/authorized_keys ] || echo "warning: deploy user has no key yet (set DEPLOY_PUBKEY)" >&2

step "files"
(cd root && find . -type f) | while read -r f; do
	f=${f#./}
	mode=0644
	case $f in usr/local/*bin/*) mode=0755 ;; etc/sudoers.d/*) mode=0440 ;; esac
	install -D -o root -g root -m "$mode" "root/$f" "/$f"
done
sed -i "s/ADMIN_USER/$ADMIN_USER/" /etc/ssh/sshd_config.d/50-doiomad.conf
visudo -cq -f /etc/sudoers.d/doiomad-deploy
sshd -t
systemctl reload ssh
systemctl restart systemd-journald
systemctl daemon-reload

step "unattended upgrades"
echo 'unattended-upgrades unattended-upgrades/enable_auto_updates boolean true' | debconf-set-selections
dpkg-reconfigure -f noninteractive unattended-upgrades

step "database restore (only when there is no database yet)"
if [ -f /etc/doiomad/litestream.env ] && [ ! -f /var/lib/doiomad/doesitomarchy.db ]; then
	set -a; . /etc/doiomad/litestream.env; set +a
	sudo -u doiomad --preserve-env=LITESTREAM_BUCKET,LITESTREAM_ENDPOINT,LITESTREAM_ACCESS_KEY_ID,LITESTREAM_SECRET_ACCESS_KEY \
		litestream restore -config /etc/litestream.yml -if-replica-exists /var/lib/doiomad/doesitomarchy.db
fi

step "services"
systemctl enable doiomad.service
[ -e /opt/doiomad/current ] && systemctl restart doiomad.service || echo "  doiomad: no release yet; the first deploy starts it"
if [ "${REPLICATE:-}" != 1 ]; then
	systemctl disable --now -q litestream.service 2>/dev/null || true
	echo "  litestream: off (not the primary; restore only)"
elif [ -f /etc/doiomad/litestream.env ]; then
	systemctl enable litestream.service
	systemctl restart litestream.service
else
	echo "  litestream: waiting for /etc/doiomad/litestream.env"
fi
if [ -f /etc/doiomad/tunnel.env ]; then
	systemctl enable cloudflared.service
	systemctl restart cloudflared.service
else
	echo "  cloudflared: waiting for /etc/doiomad/tunnel.env"
fi
step "done"
