# doiomad runbook

How doesitomarchy.com runs, and how to rebuild it from nothing. The design is
in PLAN.md §18.

```
browser ──► Cloudflare (cache, WAF, Access) ──► tunnel ──► cloudflared ──► doiomad :8080 (loopback)
                                                                              │
                                                       /var/lib/doiomad/doesitomarchy.db ──► Litestream ──► R2
```

| What | Where |
|---|---|
| Droplet | `doiomad-1` in project DoesItOmarchy, DigitalOcean TOR1, s-1vcpu-1gb, Debian 13, tag `doiomad` |
| Firewall | `doiomad-ssh`: inbound TCP 22 only; nothing else reaches the droplet |
| Live binary | `/opt/doiomad/current/doiomad` → `/opt/doiomad/releases/<version>/` (last 5 kept) |
| Database | `/var/lib/doiomad/doesitomarchy.db`; pre-deploy copies in `/var/lib/doiomad/backups/` |
| Secrets on the droplet | `/etc/doiomad/litestream.env` (R2), `/etc/doiomad/tunnel.env` (tunnel token) |
| Services | `doiomad`, `litestream`, `cloudflared` (systemd) |
| Backups | R2 bucket `doesitomarchy-db`, continuous, 30 days |
| Logins | your admin user (sudo); `deploy` (GitHub Actions, forced command only) |

## Everyday

```sh
ssh <admin>@<ip>
doiomad version                       # the live binary, as the service user
doiomad sync                          # re-sync the catalog (deploys do this on start)
sudo doiomad-deploy status            # live version, health, installed releases
sudo doiomad-deploy rollback          # back to the previous release
journalctl -u doiomad -f              # request log (no IPs or user agents, by design)
journalctl -u litestream -u cloudflared --since today
```

**Release.** Merge to main, then:

```sh
git tag v0.5.1 && git push origin v0.5.1
```

The `release` workflow runs every CI check, publishes a GitHub Release with the
binary and its SHA-256, and deploys it. The deploy key's forced command checks
the checksum and the binary's version, copies the database, switches
`current`, restarts, and waits for `/healthz` to answer `ok v0.5.1`. If it doesn't,
it switches back and the workflow fails. Then the workflow purges
Cloudflare's cache. Nothing on main goes live until it's tagged.

**Migrations must be additive** (new tables or columns, never drops or renames
in the same release), so a rollback's older binary still runs against the newer
schema. If a release ever needs a destructive migration, restore the
pre-deploy copy from `backups/` when rolling back.

## One-time account setup

1. **Cloudflare zone.** Add `doesitomarchy.com` (Free plan). At Spaceship, set
   the nameservers to the two Cloudflare gives you. The zone shows *Active*
   once they propagate (minutes to hours).
2. **Zero Trust.** Open Zero Trust once, choose a team name and the Free plan.
   Then go to Integrations → Identity providers → Add new identity provider → **One-time PIN**.
   It is not always on by default, and without it Access can't email you a code.
   `deploy/access-check.sh` shows the login methods.
3. **R2.** Enable R2 and create the bucket `doesitomarchy-db` (location: automatic).
   Create an R2 API token with *Object Read & Write* on that bucket only. Note
   its access key ID and secret, and the S3 endpoint
   `https://<account-id>.r2.cloudflarestorage.com`.
4. **API token for `cloudflare.sh`** (My Profile → API Tokens → Create Custom Token):
   - Account: *Cloudflare Tunnel: Edit*, *Access: Apps and Policies: Edit*
   - Zone (doesitomarchy.com only): *Zone: Read*, *DNS: Edit*, *Zone Settings: Edit*,
     *Single Redirect: Edit*, *Cache Rules: Edit*, *Zone WAF: Edit*
5. **API token for deploys** (GitHub secret `CF_CACHE_TOKEN`): Zone
   (doesitomarchy.com only): *Cache Purge: Purge*. Nothing else.
6. **DigitalOcean.** A `doctl` profile named `doesitomarchy` with a custom-scoped
   token (account read; droplet, firewall, monitoring CRUD; ssh_key and tag
   create and read; project read and assign_resource; regions, sizes, image,
   snapshot, actions, vpc read), your admin SSH key registered, and a project named
   `DoesItOmarchy` (the droplet is created in it).

Keep tokens in your shell environment, a password manager, or GitHub secrets.
Never put them in chat, commits, or command-line arguments.

## Build the server

From the repo root, with the R2 values in your environment:

```sh
export LITESTREAM_BUCKET=doesitomarchy-db
export LITESTREAM_ENDPOINT=https://<account-id>.r2.cloudflarestorage.com
export LITESTREAM_ACCESS_KEY_ID=...  LITESTREAM_SECRET_ACCESS_KEY=...
SET_GITHUB_SECRETS=1 deploy/provision.sh          # droplet, firewall, setup.sh, deploy secrets

export CF_API_TOKEN=... CF_ACCOUNT_ID=...
deploy/cloudflare.sh                              # tunnel, DNS, rules, Access, tunnel token
gh secret set CF_ZONE_ID -R doesitomarchy/doesitomarchy      # value printed by cloudflare.sh
gh secret set CF_CACHE_TOKEN -R doesitomarchy/doesitomarchy  # paste the cache-purge token
```

Then tag a release. All three scripts are safe to re-run. After changing anything
under `deploy/root/`, run `deploy/provision.sh` again to apply it.

`setup.sh` restores the database from R2 when the droplet has none, so the
same steps rebuild a lost server: provision, run cloudflare.sh (it moves the
tunnel token to the new droplet), and tag or re-deploy a release.

## Public or private

The site went public on 2026-10-01 (Phase 6). `cloudflare.sh` keeps it public
by default and removes any Access application it finds. To make it private
again, for example during an incident, run:

```sh
ACCESS=on ACCESS_EMAILS="you@example.com" deploy/cloudflare.sh
```

This puts it behind Cloudflare Access (one-time email code). Run the script
again without `ACCESS=on` to reopen it.

## Restore rehearsal

Do this once in Phase 5 and after any change to backups. Record the results below.

1. Provision a spare: `deploy/provision.sh doiomad-restore` (same R2 env vars,
   no `SET_GITHUB_SECRETS`). setup.sh restores the database from R2. Only the
   primary (`doiomad-1`) runs Litestream; a spare must never write to the
   replica, so the script leaves it off on any other name.
2. Install the live release on it:
   `gh release download <tag> -p 'doiomad-linux-amd64*'`, then copy the binary
   over and run `sudo doiomad-deploy install <tag> <sha256> < doiomad-linux-amd64`
   on the spare.
3. Check: `sudo doiomad-deploy status` shows `ok <tag>`, and
   `sqlite3 /var/lib/doiomad/doesitomarchy.db 'PRAGMA integrity_check'` prints `ok`.
   Compare `doiomad sync` output with production.
4. Destroy the spare: `doctl --context doesitomarchy compute droplet delete doiomad-restore`.

| Date | Release | Provision → restored | Restored → healthy | Notes |
|---|---|---|---|---|
| 2026-10-01 | v0.5.1 | 3 min 40 s (droplet create + setup, restore included) | 2 s (install → `ok v0.5.1`) | integrity_check ok; schema 2; counts match production. The spare's Litestream ran for 10 s and wrote to the replica (fixed: spares no longer replicate; production moved to a fresh `live/` path). |

## Rotating secrets

- **Deploy key:** delete `~/.ssh/doesitomarchy_deploy*`, then run `SET_GITHUB_SECRETS=1 deploy/provision.sh`.
- **R2 key:** create a new key, export the new values, run `deploy/provision.sh`, then revoke the old key.
- **Tunnel token:** rotate the tunnel's token in Zero Trust, then run `deploy/cloudflare.sh`.
- **Cache-purge token:** create a new one, then `gh secret set CF_CACHE_TOKEN`.
