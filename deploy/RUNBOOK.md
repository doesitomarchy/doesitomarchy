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
| Secrets on the droplet | `/etc/doiomad/litestream.env` (R2), `/etc/doiomad/tunnel.env` (tunnel token), `/etc/doiomad/doiomad.env` (cache-purge token and Access settings) |
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

**Rolling back.** `sudo doiomad-deploy rollback` switches back to the previous
binary. An older binary won't start on a database that a newer release has
migrated ("database schema version N is newer than this binary supports"), so
after a release with a migration, roll the database back too. The deploy copies
it before switching, to `/var/lib/doiomad/backups/pre-<version>.db`:

```sh
sudo systemctl stop doiomad litestream
sudo -u doiomad cp /var/lib/doiomad/backups/pre-v0.14.0.db /var/lib/doiomad/doesitomarchy.db
sudo rm -f /var/lib/doiomad/doesitomarchy.db-wal /var/lib/doiomad/doesitomarchy.db-shm
sudo systemctl start litestream
sudo doiomad-deploy rollback
```

Reports received since the deploy are lost that way, so a fixed new release is
usually the better way back. Keep migrations additive where possible (new
tables or columns), so that fix-forward stays easy.

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
   - Account: *Cloudflare Tunnel: Edit*, *Access: Apps and Policies: Edit*,
     and optionally *Access: Organizations, Identity Providers, and Groups: Read*
     (lets `cloudflare.sh` read the team name; otherwise set `CF_ACCESS_TEAM`)
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
export ADMIN_EMAILS="you@example.com"             # who may sign in to /admin
deploy/cloudflare.sh                              # tunnel, DNS, rules, Access, tunnel token
gh secret set CF_ZONE_ID -R doesitomarchy/doesitomarchy      # value printed by cloudflare.sh
gh secret set CF_CACHE_TOKEN -R doesitomarchy/doesitomarchy  # paste the cache-purge token
```

Then tag a release. All three scripts are safe to re-run. After changing anything
under `deploy/root/`, run `deploy/provision.sh` again to apply it.

`setup.sh` restores the database from R2 when the droplet has none, so the
same steps rebuild a lost server: provision, run cloudflare.sh (it moves the
tunnel token to the new droplet), and tag or re-deploy a release.

## Moderating diagnostic reports

Diagnostic reports arrive through the API from registered test tools, or as
files you import, and start **pending**. Nothing counts until a maintainer accepts it. Each report has a
10-character code, which is its public URL (`/report/CODE`) and how the CLI
names it. On the droplet the `doiomad` wrapper
runs as the service user, which can't read your home directory, so pipe
files in:

```sh
doiomad reports import - < report.yaml     # validates, stores as pending, previews the effect
doiomad reports list -state pending
doiomad reports show 4f9a2c7e1b            # items, evidence, flags, history
doiomad reports accept 4f9a2c7e1b
doiomad reports reject 4f9a2c7e1b -reason "duplicate of 7d1e09ab32"
doiomad reports retract 4f9a2c7e1b -reason "tested with a third-party Wi-Fi card"
doiomad reports flags                      # open review flags
doiomad reports resolve 3 -note "stored, not counted; fine"
```

- **Effect:** accepts and retractions bump a change counter. The server
  rebuilds within about 5 s, then purges Cloudflare's cache 10 s after the
  last change. That needs `CF_ZONE_ID` and `CF_PURGE_TOKEN` (a purge-only
  token) in `/etc/doiomad/doiomad.env`, which `provision.sh` writes when
  they're set. Without them, pages catch up within 5 minutes.
- **Records:** every action is recorded with your user name. Nothing is ever
  deleted; a retracted report keeps its public page, marked retracted.
- **Times:** every timestamp (tested, submitted, moderated) is stored and
  shown in UTC.
- **Privacy:** personal data (serials, MAC and IP addresses, host and user
  names, e-mail addresses) is scrubbed before storage. The raw submission is
  kept, scrubbed and private.

### In the browser: /admin

`/admin` is the same review queue in a browser: pending reports, open flags,
each report in full (with its scrubbed raw report), and buttons to accept,
reject, retract, resolve flags and pick a configuration.

- **Sign-in:** Cloudflare Access protects `/admin` only, with a one-time email
  code. `cloudflare.sh` creates that Access app from `ADMIN_EMAILS` and writes
  its team name and audience tag (`CF_ACCESS_TEAM`, `CF_ACCESS_AUD`) into
  `/etc/doiomad/doiomad.env`. The server checks Access's signed token itself.
- **Maintainers:** an address must also be a maintainer, which names who did
  what: `doiomad maintainers add carl carl@example.com`. Remove with
  `doiomad maintainers remove carl`. Without the Access settings `/admin`
  answers 503; with them, anyone who isn't a maintainer gets 403.
- **Locally:** `doiomad serve -demo -admin-insecure -addr 127.0.0.1:8081` opens
  `/admin` without Access, as "local". The flag is refused on any other address.

### Ambiguous reports

When the hardware in a report fits several configurations (they differ only
in something the probe can't see), the report is stored with a
`config_ambiguous` flag and can't be accepted until you pick one: in `/admin`,
or `doiomad reports accept CODE -config ID`. `reports show` lists the choices.

### Test tools (sources)

A tool needs a source ID and key to submit through `POST /api/v1/reports`
(documented at `/api`). Ask the author for the tool's name and homepage, then:

```sh
doiomad sources add omacdiag -name OmacDiag -homepage https://example.com/omacdiag
#   prints the key once; send it to the author privately (never in a public channel)
doiomad sources list
doiomad sources rotate omacdiag      # a leaked key: new key, the old one stops at once
doiomad sources revoke omacdiag      # stop it submitting; its reports stay
doiomad sources trust omacdiag trusted
```

Only a hash of the key is stored. Each source may submit 60 reports an hour.

## The white flag (Unsupported)

A maintainer can give up on a failing criterion, on one component (every
configuration with it) or on one configuration, with a reason the site shows.
It's in `/admin/unsupported`, or:

```sh
doiomad unsupported set input.touch-id -component bridge/apple-t2 -reason "…"
doiomad unsupported list [-all]       # -all: lifted flags too
doiomad unsupported clear ID
```

## Fix tracking (retired in v0.14.0)

Fix tracking through GitHub issues (PLAN.md §26: the fix repo, its token and
webhook, `/hooks/github`, `doiomad fixes`, `/admin/fixes`) was retired in
v0.14.0; no fix issue was ever opened. `/fixes` now lists the OmaBoot? fixes
from `data/fixes.yaml`. Builds are looked up on GitHub without a token (public
repo, lower rate limit).

**Release-ops checklist for v0.14.0:**

1. On the droplet, remove `GITHUB_TOKEN`, `GITHUB_WEBHOOK_SECRET` and
   `FIX_REPO` from `/etc/doiomad/doiomad.env`, keeping the other settings:
   `sudo sh -c 'f=/etc/doiomad/doiomad.env; grep -v -E "^(GITHUB_TOKEN|GITHUB_WEBHOOK_SECRET|FIX_REPO)=" $f > $f.tmp && mv $f.tmp $f' && sudo systemctl try-restart doiomad`
2. Remove them from `~/.config/doesitomarchy/deploy.env` too.
3. Revoke the fine-grained token on GitHub (Settings → Developer settings →
   Fine-grained tokens).
4. The webhook goes away with the `wecanfixeverything` repo when it's deleted.

## The MCP server (/mcp)

AI assistants call `/mcp` from their providers' servers (claude.ai, ChatGPT) or
from users' machines. It needs no setup, but:

- Keep Bot Fight Mode off, or skip it for `/mcp`: it challenges data-centre
  IPs, so connectors would quietly stop working.
- `cloudflare.sh` keeps `/mcp` out of the cache (POSTs aren't cached anyway).
- Usage: `sudo journalctl -u doiomad | grep "mcp tool"` shows one line per tool
  call, with the tool's name only. The server allows 300 requests a minute
  per IP and answers 429 above that.
- Check it after a release with the SDK-free probe below (one request):

```sh
curl -s https://doesitomarchy.com/mcp -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" -H "MCP-Protocol-Version: 2025-06-18" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | grep -o '"name":"[a-z_]*"'
```

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
