# Opskest deployment guide

This guide covers every way to run Opskest: Docker Compose (recommended), a plain Linux host under systemd, a Windows service wrapper, and macOS or other platforms. It also covers the reverse proxy and TLS in front of it, configuration, backups, security, scaling and troubleshooting. For features, roles and the user interface, see [Docs.md](../Docs.md).

Steps that have not been tested are marked "untested" or "not verified".

## Contents

1. [Overview, decision table and requirements](#1-overview-decision-table-and-requirements)
2. [Docker Compose (recommended)](#2-docker-compose-recommended)
3. [Plain Linux (bare metal or VM) with systemd](#3-plain-linux-bare-metal-or-vm-with-systemd)
4. [Reverse proxy and TLS](#4-reverse-proxy-and-tls)
5. [Windows, macOS and other platforms](#5-windows-macos-and-other-platforms)
6. [Configuration reference](#6-configuration-reference)
7. [Data, backups and restore](#7-data-backups-and-restore)
8. [Security checklist for production](#8-security-checklist-for-production)
9. [Scaling and performance notes](#9-scaling-and-performance-notes)
10. [Verification after deploy and troubleshooting](#10-verification-after-deploy-and-troubleshooting)

---

## Quick install

Docker, the latest release (most installs):

```bash
git clone https://github.com/opskest/opskest.git && cd opskest
mkdir -p data backups && sudo chown -R 65532:65532 data backups
docker compose up -d
```

Open <http://localhost:8085> and create the first administrator. Upgrade later with `docker compose pull && docker compose up -d`.

To change the port, copy `.env.example` to `.env`, set `OPSKEST_PORT` (for example `OPSKEST_PORT=9000`), and run `docker compose up -d` again. The sign-in address follows it. For a real domain, set `APP_URL` in the same file (section 2).

Build the latest release from source instead (it still checks for signed updates):

```bash
git clone https://github.com/opskest/opskest.git && cd opskest
mkdir -p data backups && sudo chown -R 65532:65532 data backups
scripts/compose-build --latest
```

Upgrade later with `scripts/compose-build --latest`. The sections below cover everything else: a plain binary under systemd, Windows, a reverse proxy with TLS, backups and troubleshooting.

---

## 1. Overview, decision table and requirements

Opskest is one Go program with the web interface embedded and a SQLite database. There is no database server, message queue or web server to install. It serves plain HTTP on one address and has no TLS or ACME client of its own, so a public install always puts a reverse proxy in front of it.

Compose runs the official image from `ghcr.io/opskest/opskest`. Binaries for Linux, Windows and macOS are attached to every release at <https://github.com/opskest/opskest/releases>, or you can build either yourself from a release tag (sections 2.8 and 3.1).

### Which method

| Method | Best for | Notes |
| --- | --- | --- |
| Docker Compose (section 2) | Most installs: a single VM or server with Docker. | Distroless non-root image, healthcheck, restart policy and the ping sysctl are already set. You update with `docker compose pull && docker compose up -d`. The in-app updater only notifies. |
| Plain Linux with systemd (section 3) | Hosts without Docker, or where you want the signed in-app updater. | You manage the user, directories, sysctl and unit file. The in-app updater can replace the binary. |
| Windows (section 5) | Windows servers. | The binary builds for Windows but no service wrapper is shipped. Ping monitors are untested there (see section 5). |
| macOS and other (section 5) | Trying it out. | Binary only, with no supervisor files shipped. |
| Behind an AWS ALB | Compose or plain Linux, with ACM certificates. | See section 4.5. |

### Requirements

| Item | Guidance |
| --- | --- |
| CPU and RAM | The design target is 2,000 monitors at a 30 s interval with scheduler lag p99 under 1 s, RSS under 150 MB and under 0.5 vCPU. In a local load test with 2 CPUs and 1 GB (see [Docs.md](../Docs.md#15-performance-limits-and-contributing)), 2,000 monitors used 61.4 MB of RSS and 0.100 cores. Those are point-in-time numbers from a development machine, not guarantees. The Compose file caps the container at 1 CPU and 512 MB. For a typical install, 1 vCPU and 512 MB of RAM is a reasonable start. No figure has been measured for large numbers of status page viewers. |
| Disk | The database lives in `DATA_DIR`. Each scheduled or manual backup is a full copy of the database (`VACUUM INTO`), and 7 are kept by default, so plan for roughly (number kept + 1) times the database size plus headroom. Raw check history is kept 7 days by default (1 to 90 days), hourly rollups 90 days and daily rollups 730 days. Check Settings > Storage after the first week to see the real size. Use local disk, not a network share. |
| Operating system | Linux for production (the image and the systemd unit assume it). The release script builds linux amd64 and arm64, windows amd64 and darwin arm64. |
| Build tools | For section 2: Docker with Compose v2. The `env_file` entry in `docker-compose.yml` uses the `required: false` form, which needs Compose 2.24 or newer. For a native build: Go 1.27 (the version in `go.mod`), Node 24 with npm, and bash. |
| Inbound ports | Only the reverse proxy needs 80 and 443. Opskest itself listens on 8080 by default for the plain binary, and the Compose stack publishes `127.0.0.1:8085` only. Do not expose either to the internet. |
| Outbound access | Checks to your targets (TCP, HTTP, DNS, ICMP echo for ping monitors) and the channels you configure (Slack, SMTP and so on). TCP port 53 to `1.1.1.1`, `8.8.8.8` and `208.67.222.222` for the internet-loss guard: if none answers, failing monitors are recorded as unknown, not down. `api.globalping.io`, only if you set monitors to worldwide checks (Settings > Check regions). `data.iana.org` and registry RDAP servers, only when domain-expiry checks are on. GitHub (or your own feed URL) for update checks in official release builds. |
| Time | Keep the host clock accurate (NTP). Daily rollups, the backup hour and maintenance windows use the display time zone (`DISPLAY_TZ`). |
| DNS and TLS | A hostname that points at the proxy, and a certificate for it. |

---

## 2. Docker Compose (recommended)

Compose pulls the official multi-architecture image (`ghcr.io/opskest/opskest`, amd64 and arm64), which is built from the `Dockerfile` in three stages: Node 24 builds the web app, Go builds the binary with `CGO_ENABLED=0`, and the result is copied into `gcr.io/distroless/static-debian12:nonroot`. The container runs as uid 65532, has no shell, listens on 8080 inside, and is published on the host as `${OPSKEST_HOST:-127.0.0.1}:${OPSKEST_PORT:-8085}`. The official image knows its version and checks for signed updates; a source build (section 2.8) does not.

### 2.1 First start

```bash
git clone https://github.com/opskest/opskest.git opskest && cd opskest
mkdir -p data backups
sudo chown -R 65532:65532 data backups
cp .env.example .env
```

Edit `.env` (every line in `.env.example` is commented out by default). For anything other than a local trial, set at least:

```ini
APP_URL=https://status.example.com
```

Then start it:

```bash
docker compose up -d
docker compose ps
docker compose logs -f opskest
```

Without a `.env`, Compose defaults `APP_URL` to `http://localhost:8085`, which is right for a local trial: open <http://localhost:8085>.

To change the port on the host, copy `.env.example` to `.env` and edit the `OPSKEST_PORT` line (remove the leading `#`), for example `OPSKEST_PORT=9000`, then run `docker compose up -d`. If you set only `OPSKEST_PORT`, the default `APP_URL` follows it. Leave the container's own port (8080) alone.

Settings that matter for Compose:

| Setting | Where | Meaning |
| --- | --- | --- |
| `APP_URL` | `.env` | The exact address people open, with scheme, host and port. `https://` turns on Secure cookies. Setup and sign-in are refused with HTTP 403 from any other origin. Compose uses `http://localhost:<OPSKEST_PORT>` (`http://localhost:8085`) if you set nothing. |
| `OPSKEST_PORT` | `.env` | Host port the app is published on. Default `8085`. If you change it, update every `proxy_pass` in your proxy configuration, and set `APP_URL` if people do not open it as `localhost`. |
| `OPSKEST_HOST` | `.env` | Host address the port is published on. Default `127.0.0.1`. Keep it on loopback behind a reverse proxy. Use `0.0.0.0` only behind an ALB or a firewall that restricts the port. |
| `OPSKEST_DATA_DIR` | `.env` | Host folder mounted as `/data`. Default `./data`. |
| `BACKUP_HOST_DIR` | `.env` | Host folder mounted as `/backups`. Default `./backups`. |
| `OPSKEST_VERSION` | `.env` | Image tag to run (`ghcr.io/opskest/opskest:<OPSKEST_VERSION>`, default `latest`). Pin it, for example `1.0.2`, to stay on a version. |
| `SECRET_ENCRYPTION_KEY`, `DISPLAY_TZ`, `SERVER_REGION`, `METRICS_TOKEN`, `CHECK_CONCURRENCY`, `LOG_LEVEL` | `.env` | Optional. See section 6. |

Do not set `DATA_DIR`, `BACKUP_DIR`, `PORT` or `LISTEN_ADDR` in `.env` for Compose. The whole `.env` is passed into the container as environment, and those values would override the paths and port that the image sets (`DATA_DIR=/data`, `BACKUP_DIR=/backups`, `LISTEN_ADDR=:8080`).

### 2.2 Ownership of data and backups

`./data` and `./backups` are bind mounts, not named volumes. The process runs as uid 65532 and there is no shell to fix permissions from inside the container, so both folders must be writable by that uid on the host before the first start:

```bash
sudo chown -R 65532:65532 data backups
```

If they are not writable, Opskest stops at boot, before it opens the database, with a one-line message that names the exact command (`cannot write <dir>: on the host run  sudo chown -R 65532:65532 <dir>`). If you use `OPSKEST_DATA_DIR` or `BACKUP_HOST_DIR`, chown those folders instead.

### 2.3 The ping sysctl

`docker-compose.yml` sets `net.ipv4.ping_group_range: "0 2147483647"` on the container, so ping monitors can use unprivileged ICMP without granting `NET_RAW`. Keep that line. Without it, every ping monitor fails with a permission error that has nothing to do with the monitor's own settings.

### 2.4 Healthcheck, limits and logs

- The healthcheck runs `/opskest healthcheck`, a subcommand that requests `http://127.0.0.1:<port>/healthz` and exits 0 on HTTP 200 (the distroless image has no curl or wget). The Compose file runs it every 30 s with a 5 s timeout, 3 retries and a 10 s start period.
- Resource limits in the Compose file are 1 CPU and 512 MB.
- Logs are JSON on stdout, kept by the `json-file` driver at 3 files of 10 MB.
- The restart policy is `unless-stopped`, which also brings Opskest back after the clean exit that follows a database restore.

```bash
docker compose ps                                  # shows (healthy) once /healthz answers
docker compose exec opskest /opskest healthcheck; echo "exit code: $?"
docker compose exec opskest /opskest version
docker compose logs --tail 100 opskest
docker compose logs -f opskest
```

### 2.5 First-run setup

Open `APP_URL` exactly as configured. The first screen creates the first administrator. It works once (an atomic operation), and there are no seeded credentials or login bypass. Create the administrator before you share the address or open the proxy to the public, then add teammates under Settings > Team access.

### 2.6 Upgrading

The in-app updater does not replace a container: on Docker, it can only notify. Settings > About & updates shows when a newer version exists. To upgrade, back up, then pull and restart:

```bash
# 1. In the web interface: Settings > Backups > back up now (or wait for the scheduled one).
# 2. Pull the new image and restart:
cd opskest
docker compose pull
docker compose up -d
docker compose ps
```

Migrations run automatically on start and only move forward. To go back, pin the previous tag in `.env` and start again:

```bash
OPSKEST_VERSION=1.0.2    # in .env
docker compose up -d
```

If an older version refuses a database that a newer one has migrated, restore the backup you took before the upgrade (section 7).

### 2.7 Stopping and removing

Backups and restore are covered in section 7. Both folders are on the host, so they survive `docker compose down` and `docker compose down -v`.

```bash
docker compose down                 # stop and remove the container; data and backups stay on the host
docker compose restart opskest
docker compose stop opskest         # stop without removing
```

### 2.8 Building from source

To build the code yourself, run the build script. `--latest` switches to the newest release first:

```bash
scripts/compose-build --latest
```

On a clean release tag this is a release build. It reports the tag's version and has the project's public key (`deploy/release.pub`) and the release feed address built in, so Settings, About & updates checks for new versions and verifies their signatures, exactly like the official image. It still only notifies: to upgrade, run `scripts/compose-build --latest` again, which is also the command the update screen shows.

Any other checkout (a branch, a commit without a release, or a tag with local changes) builds as `dev`, which has no key or feed and never checks for updates. That is deliberate: modified code must not present itself as a signed release. To go back to the official image, run `docker compose up -d` without the build file.

---

## 3. Plain Linux (bare metal or VM) with systemd

Use this when you do not want Docker, or when you want the signed in-app updater to replace the binary.

### 3.1 Get a binary

There are two ways to get one.

Download a release. When a version has been published, the release page at <https://github.com/opskest/opskest/releases> has `opskest-linux-amd64` and `opskest-linux-arm64` (plus the Windows and macOS builds), a `manifest.json` and its signature. Releases are built and signed by GitHub Actions and carry a build provenance attestation, which you can check with the GitHub CLI:

```bash
gh attestation verify opskest-linux-amd64 --repo opskest/opskest
```

Official release binaries already contain the project's update feed address and public key (see section 3.7).

Build it yourself. You need Go 1.27, Node 24 and bash. `scripts/build-release` builds the web interface and then produces four binaries in `dist/`:

```bash
git clone https://github.com/opskest/opskest.git opskest-src && cd opskest-src
scripts/build-release --latest    # switches to the newest release, then builds
ls -l dist
# dist/opskest-linux-amd64  dist/opskest-linux-arm64  dist/opskest-windows-amd64.exe  dist/opskest-darwin-arm64
```

A clean checkout of a release tag builds as that release: it reports the tag's version and has the project's public key (`deploy/release.pub`) and feed address built in, so it checks for signed updates and can install them like an official binary. `--latest` switches to the newest release tag first. Any other checkout (a branch, or changed files) builds as `dev`, which never updates itself. To build a branch as a different version, pass it yourself, for example `scripts/build-release 1.1.0-rc1`; that build has no key or feed unless you set them (section 3.7).

To build one binary by hand, build the web interface first, because Vite writes to `internal/webui/dist`, which Go embeds:

```bash
cd web && npm ci && npm run build && cd ..
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=1.0.2" -o opskest ./cmd/opskest
./opskest version
```

Without local Go and Node, the Docker wrappers do the same: `scripts/build-web` builds the web app, and `scripts/go build ...` runs the Go toolchain in a container (this produces files owned by root, so `chown` them afterwards).

### 3.2 User, directories and files

These steps follow the header of `deploy/opskest.service`. Run them from the repository root, with the binary at `./opskest` (for a downloaded or built release, copy it there first, for example `cp dist/opskest-linux-amd64 opskest`):

```bash
sudo useradd --system --home /opt/opskest --shell /usr/sbin/nologin opskest
sudo install -d -o opskest -g opskest /opt/opskest
sudo install -o opskest -g opskest -m 0755 opskest /opt/opskest/opskest
```

Keep the binary in a folder that the `opskest` user owns (here `/opt/opskest`). The updater swaps the file by writing next to it, and it refuses to update a binary under package-managed paths such as `/usr/bin`, `/usr/sbin`, `/bin`, `/sbin`, `/usr/lib` or `/snap`. Data and backups are created under `/opt/opskest/data` and `/opt/opskest/backups`, because the unit's working directory is `/opt/opskest` and the defaults are relative (`./data`, `./backups`).

### 3.3 Configuration

Opskest reads `.env` from its working directory (or the file named by `OPSKEST_ENV_FILE`). A real environment variable that is set and not empty wins over the file. The file contains secrets, so restrict it:

```bash
sudo tee /opt/opskest/.env >/dev/null <<'EOF'
APP_URL=https://status.example.com
LISTEN_ADDR=127.0.0.1:8085
OPSKEST_UPDATE_RESTART=exit
EOF
sudo chown opskest:opskest /opt/opskest/.env
sudo chmod 600 /opt/opskest/.env
```

What these three settings do:

- `APP_URL` is the exact public origin (section 6).
- `LISTEN_ADDR=127.0.0.1:8085` keeps Opskest on loopback, behind the proxy, and matches the shipped `deploy/nginx.host.conf`, which proxies to `127.0.0.1:8085`. Without it, the default is `:8080` on all interfaces.
- `OPSKEST_UPDATE_RESTART=exit` is for the in-app updater under a supervisor (section 3.7). Leave it out if you will not use the updater.

For a local trial without a proxy, leave `.env` empty or absent. Opskest then listens on `:8080` and `APP_URL` defaults to `http://localhost:8080`. Open it as `localhost`, not `127.0.0.1`, unless `APP_URL` says otherwise (the loopback spellings count as one only when `APP_URL` is itself a loopback address with the same port).

### 3.4 Unprivileged ping

Ping monitors use unprivileged ICMP (UDP-style) sockets. On Linux that needs `net.ipv4.ping_group_range` to include the group of the process. The shipped unit does not set it (it only carries a comment), so set it on the host and make it persistent:

```bash
echo 'net.ipv4.ping_group_range = 0 2147483647' | sudo tee /etc/sysctl.d/99-opskest-ping.conf
sudo sysctl --system
sysctl net.ipv4.ping_group_range
```

### 3.5 The systemd unit

Install and start it:

```bash
sudo cp deploy/opskest.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now opskest
systemctl status opskest
journalctl -u opskest -f
```

What the shipped unit does:

| Line | Effect |
| --- | --- |
| `After=network-online.target`, `Wants=network-online.target` | Start after the network is configured. |
| `User=opskest`, `Group=opskest` | Run as the dedicated unprivileged account. Its group id must fall inside `ping_group_range`. |
| `WorkingDirectory=/opt/opskest` | Relative paths and `.env` resolve here, so data is `/opt/opskest/data` and backups are `/opt/opskest/backups`. |
| `ExecStart=/opt/opskest/opskest serve` | Starts the server. |
| `Restart=always`, `RestartSec=3` | Restart 3 seconds after any exit, including a crash and the clean exit (status 0) that follows a database restore from Settings or an update in `exit` mode. A deliberate `systemctl stop` still stops it. |
| `NoNewPrivileges=true` | The process and its children cannot gain privileges (setuid binaries, file capabilities). |
| `ProtectSystem=strict` | The whole file system hierarchy is read-only for the service, except `/dev`, `/proc`, `/sys` and the paths listed in `ReadWritePaths`. |
| `ProtectHome=true` | `/home`, `/root` and `/run/user` are inaccessible. |
| `PrivateTmp=true` | The service gets its own private `/tmp` and `/var/tmp`. |
| `ReadWritePaths=/opt/opskest` | The only writable tree: the database, backups, the encryption key, and the binary's own folder (needed for in-app updates). |
| `WantedBy=multi-user.target` | Start at boot once enabled. |

Things to remember:

- If you move `DATA_DIR` or `BACKUP_DIR` outside `/opt/opskest`, add those paths to `ReadWritePaths` (use `sudo systemctl edit opskest` for a drop-in) and create them with the right owner. A `ReadWritePaths` entry that does not exist stops the unit from starting.
- To change `ExecStart` or add `Environment=` lines, use a drop-in rather than editing the shipped file, then run `sudo systemctl daemon-reload && sudo systemctl restart opskest`.
- After editing `.env`, restart with `sudo systemctl restart opskest`.

Useful commands:

```bash
sudo systemctl restart opskest
sudo systemctl stop opskest
sudo systemctl status opskest
journalctl -u opskest --since "1 hour ago"
journalctl -u opskest -p warning -f
/opt/opskest/opskest version
```

### 3.6 Firewall

Allow only SSH and the proxy ports. Do not open 8080 or 8085, since Opskest should be reachable only through the proxy. For example, with ufw:

```bash
sudo ufw allow OpenSSH
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
sudo ufw status
```

### 3.7 Upgrading

There are two ways.

By hand, which always works:

```bash
# 1. Settings > Backups > back up now. Then build or copy the new binary, and:
sudo systemctl stop opskest
sudo cp /opt/opskest/opskest /opt/opskest/opskest.old
sudo install -o opskest -g opskest -m 0755 opskest /opt/opskest/opskest
sudo systemctl start opskest
journalctl -u opskest -n 50
```

Migrations run automatically and only move forward. To go back, restore the old binary and, if it refuses the newer database, restore the pre-upgrade backup (section 7).

With the signed in-app update (plain-binary installs only). In Settings > About & updates, an admin sets a release feed (an HTTPS URL of `manifest.json`; plain `http://` is accepted only for `localhost`, `127.0.0.1` and `::1`) and a public key. Opskest then checks the feed. On Update now (or automatically in `auto` mode) it takes a database backup, downloads the new binary, and verifies the manifest's ed25519 signature (`<feed URL>.sig`), size and SHA-256. It then checks that the file is an executable for the OS, runs `<new> version` and compares the reported version, swaps the file (keeping the old one as `<exe>.prev`) and restarts.

Official release builds have the project's feed address and public key built in, so they check GitHub for new releases without any setup. A feed or key saved in Settings wins. A clean checkout of a release tag built with `scripts/build-release` (no version argument) gets both from the repository. Any other build you make has neither unless you set `OPSKEST_UPDATE_FEED_URL` and `OPSKEST_UPDATE_PUBLIC_KEY` when you run `scripts/release` or `scripts/build-release`. Without a feed URL, nothing is contacted. To publish your own feed, see [Docs.md](../Docs.md#publishing-a-release).

The code requires all of these before it allows an install:

- The install method is a plain binary (not Docker, not a package-managed path).
- The build has a real version number.
- A public key is available, either saved in Settings or built in.
- The process can create files in the binary's folder.

How the restart happens depends on `OPSKEST_UPDATE_RESTART`:

| Value | What happens after the swap | Use it when |
| --- | --- | --- |
| `self` (default; any unrecognised value also means `self`) | Opskest shuts down gracefully, closes the database, then starts the new binary itself. On Unix it `exec`s the new file in place (same PID). On Windows it starts a detached copy and exits. If the new binary cannot be started, it swaps the `.prev` file back and starts that instead. | You run the binary by hand, from cron, or in any setup with no supervisor. |
| `exit` | Opskest swaps the files, shuts down gracefully and exits with status 0. The supervisor starts the new file. | systemd with `Restart=always` (as in the shipped unit), or a Windows service wrapper such as NSSM or WinSW. Spawning a second copy under a supervisor would fight the supervisor's copy for the port. |

Under systemd, use `exit`. One difference to know about: the automatic fallback to the old binary exists only in `self` mode. In `exit` mode, if the new binary is broken, systemd keeps restarting it every 3 seconds, so roll back by hand:

```bash
sudo systemctl stop opskest
sudo mv /opt/opskest/opskest.prev /opt/opskest/opskest
sudo systemctl start opskest
```

Settings > About & updates also has a one-click Roll back that swaps the current binary and `.prev`. Because migrations only move forward, if the rolled-back version refuses the database, restore the backup named in the update.

### 3.8 Uninstall

Take a final backup and copy out the encryption key first (section 7). Then:

```bash
sudo systemctl disable --now opskest
sudo rm /etc/systemd/system/opskest.service
sudo systemctl daemon-reload
sudo rm -f /etc/sysctl.d/99-opskest-ping.conf       # only if nothing else needs it
sudo rm -rf /opt/opskest                           # deletes the database, backups and encryption key
sudo userdel opskest
```

Remove the proxy site and certificate separately (section 4).

---

## 4. Reverse proxy and TLS

Opskest serves plain HTTP only. The proxy on the host owns ports 80 and 443 for every tool on the box, and it owns the certificate lifecycle. The Opskest stack never binds 80 or 443 and never runs its own ACME client.

Any proxy must do the following:

1. `APP_URL` must equal the public origin (scheme, host, port). Opskest does not read `Host` or `X-Forwarded-Proto` from the proxy. The scheme of `APP_URL` alone decides whether cookies are Secure. Setup and sign-in return 403 ("cross-site request rejected") when the browser's `Origin` differs from `APP_URL`, or when `Sec-Fetch-Site` is `cross-site`.
2. Do not buffer the two live streams: `/api/stream` (the signed-in dashboard) and `/api/public/pages/<slug>/stream` (public and private status pages). They are long-lived Server-Sent Events responses. Opskest sends `X-Accel-Buffering: no`, which nginx honours, but configure it explicitly as well, and give these two paths a long read timeout.
3. Set the client address, and make the proxy the only way in. The public status page rate limit (and the badge, feed and subscribe limits) is keyed by the `X-Real-IP` header, else the first value of `X-Forwarded-For`, else the socket address. A client that reaches Opskest directly can choose that value. Sign-in throttling uses the socket address only, which behind a proxy is the proxy's address, so all users share one bucket of 60 attempts per 10 minutes for the source address.
4. Compression: Opskest compresses responses itself. It gzips JSON, HTML, CSS, JavaScript, XML and SVG for clients that send `Accept-Encoding: gzip`, and never touches the streams. You do not need proxy compression. If you enable it anyway, exclude the two stream paths so it cannot buffer them.
5. Allow large request bodies where needed. Restore uploads can be up to 1 GiB and monitor imports up to 5 MiB.
6. Enable HTTP/2 to browsers (see section 9).

### 4.1 nginx on the host

`deploy/nginx.host.conf` is a ready server block for HTTP port 80 that proxies to `127.0.0.1:8085`. It sets `X-Real-IP` from `$remote_addr` (overwriting anything the client sent) and contains:

- an ACME challenge location (`/.well-known/acme-challenge/`, root `/var/www/html`);
- a regex location for the two stream paths, with `proxy_buffering off`, `proxy_cache off`, `proxy_http_version 1.1`, an empty `Connection` header and `proxy_read_timeout 1h`;
- a location for `POST /api/backups/upload` with `client_max_body_size 1025m` and 5-minute timeouts;
- a general location with 30 s timeouts and `client_max_body_size 2m`.

Install it (replace the hostname; these are the Debian and Ubuntu package names):

```bash
sudo apt install nginx certbot python3-certbot-nginx
sudo cp deploy/nginx.host.conf /etc/nginx/sites-available/opskest
sudo sed -i 's/opskest\.example\.com/status.example.com/' /etc/nginx/sites-available/opskest
sudo ln -s /etc/nginx/sites-available/opskest /etc/nginx/sites-enabled/opskest
sudo nginx -t
sudo systemctl reload nginx
```

If Opskest listens somewhere other than `127.0.0.1:8085`, change every `proxy_pass` in the file. If you import monitor files larger than 2 MiB (the application accepts up to 5 MiB), raise `client_max_body_size` in the general location.

Complete first-admin setup privately before you open public access, for example through an SSH tunnel to the loopback port.

### 4.2 Certificates with certbot (on the host)

The certificate belongs to the host proxy, never to the Opskest stack. With the nginx plugin:

```bash
sudo certbot --nginx -d status.example.com
sudo certbot renew --dry-run
systemctl list-timers | grep -i certbot
```

Certbot edits the server block to add the HTTPS listener and a redirect. Check the result with `sudo nginx -t`, load the site over HTTPS, and confirm renewal (the dry run above, and the timer or cron entry your distribution installs). Then set `APP_URL=https://status.example.com` in Opskest's `.env` and restart Opskest, so that cookies become Secure and alert links use HTTPS.

Without the nginx plugin you can use the webroot method, which the shipped ACME location supports:

```bash
sudo certbot certonly --webroot -w /var/www/html -d status.example.com
```

Then add the `listen 443 ssl` block and the certificate paths to the nginx configuration yourself.

HTTP/2: certbot may not enable it. Check the HTTPS listener and, if needed, change `listen 443 ssl;` to `listen 443 ssl http2;`. On nginx 1.25.1 or newer, add `http2 on;` inside the server block instead. Reload with `sudo nginx -t && sudo systemctl reload nginx`.

To keep Prometheus metrics off the public internet, allow only your scraper. This is not part of the shipped file; add it before the general `location /` block:

```nginx
location = /metrics {
    allow 203.0.113.10;   # your Prometheus server
    deny all;
    proxy_pass http://127.0.0.1:8085;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
}
```

### 4.3 Apache or `.htaccess` (untested)

`deploy/apache.htaccess` is a reverse-proxy example for a VirtualHost or a cPanel account's `.htaccess`. It needs `mod_proxy`, `mod_proxy_http` and `mod_rewrite`, redirects HTTP to HTTPS, and forwards everything with `RewriteRule ^(.*)$ http://127.0.0.1:8080/$1 [P,L]`. The file itself says it is not tested, and it has not been tested for this guide either. In particular:

- Replace `8080` with the port Opskest listens on, and set `APP_URL` to the `https` address.
- Verify that the two streams reach the browser unbuffered (section 10). Whether they do depends on your Apache configuration.
- Check any `LimitRequestBody` or hosting limit against restore uploads (up to 1 GiB) and imports (up to 5 MiB).
- The file does not set `X-Real-IP`. Apache's `mod_proxy` appends the client address to any `X-Forwarded-For` the client sent, and Opskest uses the first value, which the client controls. The effect is limited to the public-page rate limit (sign-in throttling uses the socket address). One possible fix is to set `X-Real-IP` from the connection address, for example `RewriteRule ^ - [E=OPSKEST_CLIENT_IP:%{REMOTE_ADDR}]` followed by `RequestHeader set X-Real-IP "%{OPSKEST_CLIENT_IP}e"`. This is untested.
- Shared hosting rarely allows long-running processes, extra ports or the needed modules. A VPS with root access (WHM on cPanel) is the realistic case: run the binary under systemd (section 3) and proxy to it.

### 4.4 Caddy (example, untested)

Caddy on the host can obtain certificates itself, which is fine because it is the host's proxy and not part of the Opskest stack. This Caddyfile uses documented Caddy directives but has not been run against Opskest:

```text
status.example.com {
    reverse_proxy 127.0.0.1:8085 {
        flush_interval -1
        header_up X-Real-IP {remote_host}
    }
}
```

`flush_interval -1` disables response buffering, which keeps the two streams live. `header_up X-Real-IP {remote_host}` matters because Caddy does not strip a client-supplied `X-Real-IP`, and Opskest prefers that header. Caddy serves HTTP/2 to browsers automatically. Verify streaming with the checks in section 10.

### 4.5 AWS Application Load Balancer

For an ALB with ACM certificates, set `OPSKEST_HOST=0.0.0.0` (Compose) or `LISTEN_ADDR=0.0.0.0:8085` (plain binary), and restrict that port in the security group to the ALB only. Then configure:

- Target group health check: HTTP `GET /healthz`, expecting 200 (the body is `ok`).
- Idle timeout of at least 60 seconds. Both streams, `/api/stream` and the public status page stream (`/api/public/pages/<slug>/stream`), send a `: ping` comment about every 15 seconds, so they stay open under that timeout even when nothing changes.
- `APP_URL=https://<your hostname>`. TLS ends at the ALB.
- The ALB appends the client address to `X-Forwarded-For` and does not set `X-Real-IP`. Opskest uses the first `X-Forwarded-For` value, which a client can supply. The effect is limited to the public-page rate limit. Behind an ALB, sign-in throttling sees the load balancer's address.

### 4.6 Cloudflare and other CDNs

- Opskest sets `Content-Type: text/event-stream`, `Cache-Control: no-cache` and `X-Accel-Buffering: no` on the streams. Whether a given CDN passes them through unbuffered has not been tested. Test with section 10, and turn off any response buffering, caching or compression feature for `/api/stream` and `/api/public/pages/*/stream`.
- Never cache `/api/` responses that depend on a session. Opskest marks private responses `private, no-store`, and public feeds, summaries and badges are cacheable for 30 to 60 seconds.
- Client address: behind a CDN, nginx's `$remote_addr` is the CDN's edge address. Every visitor would then share one rate-limit bucket (20 requests per 10 seconds, burst 40) and see HTTP 429. Make nginx recover the real address with the standard `realip` module, using the CDN's published address ranges (take them from the CDN's documentation):

```nginx
set_real_ip_from <CDN address range>;   # repeat per range
real_ip_header CF-Connecting-IP;        # Cloudflare's header; other CDNs use other names
```

- Restrict the origin to the CDN's addresses at the firewall, so nobody can bypass the CDN and forge headers.

---

## 5. Windows, macOS and other platforms

### 5.1 Windows (partly untested)

`scripts/build-release` produces `dist/opskest-windows-amd64.exe`, so the binary builds for Windows. The Windows-specific code is the time zone lookup (registry, through `golang.org/x/sys`) and the update install and restart (rename aside, then start a detached copy).

Run it in the foreground to try it:

```powershell
$env:APP_URL = "http://localhost:8080"
$env:DATA_DIR = "C:\opskest\data"
$env:BACKUP_DIR = "C:\opskest\backups"
.\opskest.exe serve
```

No service wrapper is shipped. The binary does not implement the Windows service control interface, so `sc.exe create` cannot run it directly; the service would fail to start. Use a wrapper such as NSSM or WinSW, and set `OPSKEST_UPDATE_RESTART=exit` so that the wrapper (not Opskest) starts the new file after an update. The NSSM commands below follow NSSM's documented usage and have not been run:

```powershell
nssm install Opskest "C:\opskest\opskest.exe" serve
nssm set Opskest AppDirectory "C:\opskest"
nssm set Opskest AppEnvironmentExtra APP_URL=https://status.example.com LISTEN_ADDR=127.0.0.1:8085 OPSKEST_UPDATE_RESTART=exit DATA_DIR=C:\opskest\data BACKUP_DIR=C:\opskest\backups
nssm set Opskest AppExit Default Restart
nssm start Opskest
```

Alternatively, put the same settings in a `.env` file in the working directory. The wrapper must also restart the process after a clean exit, because a database restore makes Opskest exit with status 0.

Caveats:

- Ping monitors: on Windows the ping check selects the ping library's privileged mode. Whether it works without elevated rights has not been verified, so treat Windows ping as untested.
- Shutdown: the process handles Ctrl+C and termination signals for graceful shutdown. Make sure your wrapper stops it that way.
- TLS: use a reverse proxy such as IIS ARR, nginx for Windows or Caddy. None is provided here.

### 5.2 macOS and other platforms

`scripts/build-release` builds `dist/opskest-darwin-arm64` (Apple silicon). Run it with `./opskest serve`, and put your settings in a `.env` file beside it or in the environment. No launchd file or other supervisor is shipped. If you use one, set `OPSKEST_UPDATE_RESTART=exit` and make it restart the process after a clean exit. Other operating systems and architectures can be built with `GOOS` and `GOARCH` as in section 3.1, but the scripts build and publish only the four release targets, and none of the others have been tested.

---

## 6. Configuration reference

Settings come from environment variables or a `.env` file. The lookup order is:

1. A real environment variable that is set to a non-empty value.
2. The value from the `.env` file in the working directory (or from the file named by `OPSKEST_ENV_FILE`).
3. The default.

A missing file is not an error. The file format is `KEY=VALUE` lines, `#` comments, an optional `export ` prefix and optional matching quotes around a value. A trailing ` # comment` is stripped, and values are trimmed of surrounding spaces. Invalid values stop startup with a one-line message that names the variable.

### Process settings

| Variable | Default | Meaning |
| --- | --- | --- |
| `APP_URL` | `http://localhost:<port>` (port from the listen address) | The address people open: an absolute `http(s)` URL; one trailing slash is removed. `https` makes cookies Secure. Used for the sign-in origin check (403 on mismatch), alert links and push URLs. If unset, a warning is logged. |
| `LISTEN_ADDR` | none (then `PORT`, then `:8080`) | Address to listen on, for example `127.0.0.1:8085`. Wins over `PORT`. The healthcheck subcommand always probes `127.0.0.1` on this address's port. |
| `PORT` | none | Port (1 to 65535) used as `:<port>` when `LISTEN_ADDR` is unset. |
| `DATA_DIR` | `data` (relative to the working directory) | Folder for `opskest.db` and `encryption.key`. Created if missing. Must be writable. |
| `BACKUP_DIR` | `backups` | Folder for backups. Created if missing. Must be writable. |
| `SECRET_ENCRYPTION_KEY` | empty | 64 hex characters (`openssl rand -hex 32`) that encrypt channel, monitor and two-factor secrets. If empty, the key is read from `DATA_DIR/encryption.key`, which is created (mode 0600) on first start. When this variable is set, the key file is neither read nor created. |
| `DISPLAY_TZ` | empty | IANA zone (for example `Europe/Berlin`) for charts, daily rollups, the backup hour and new maintenance windows. If empty: `TZ`, then the server's zone, then UTC. `Local` is rejected. The zone database is embedded in the binary. |
| `TZ` | empty | Used only when `DISPLAY_TZ` is empty. Must be an IANA name. |
| `SERVER_REGION` | empty (the interface shows "This server") | Label for where checks run from. |
| `METRICS_TOKEN` | empty | Bearer token for `GET /metrics`. Empty disables the endpoint (404). A wrong or missing header gets 401. |
| `CHECK_CONCURRENCY` | `256` | Concurrent checks, 1 to 2048. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` (or `warning`) or `error`, case-insensitive. |
| `OPSKEST_ENV_FILE` | `.env` | Read this file instead of `./.env`. It must be a real environment variable, because it is read before the file. |
| `OPSKEST_UPDATE_RESTART` | `self` | `self` or `exit`; any other value means `self`. See section 3.7. Can be set in `.env`. |
| `OPSKEST_IN_DOCKER` | unset | Set to `1` to force the "running in Docker" detection (no self-update). Detection is otherwise automatic (`/.dockerenv`, `/run/.containerenv`, or a docker, kubepods, containerd or libpod hint in the cgroup). |

### Compose-only variables (`docker-compose.yml`)

| Variable | Default | Meaning |
| --- | --- | --- |
| `OPSKEST_PORT` | `8085` | Host port published for the container's port 8080. The default `APP_URL` follows it. |
| `OPSKEST_HOST` | `127.0.0.1` | Host address the port is published on. |
| `OPSKEST_DATA_DIR` | `./data` | Host folder mounted as `/data`. |
| `BACKUP_HOST_DIR` | `./backups` | Host folder mounted as `/backups`. |
| `OPSKEST_VERSION` | `latest` | Image tag to run (`ghcr.io/opskest/opskest:<OPSKEST_VERSION>`). |

The image itself sets `DATA_DIR=/data`, `BACKUP_DIR=/backups` and `LISTEN_ADDR=:8080`. Compose also supplies `APP_URL` (default `http://localhost:8085`) in `environment`, which takes precedence over a different `APP_URL` in an `env_file`. Keep `APP_URL` in `.env` so that both agree.

### Command line

`opskest [serve | healthcheck | version]`. With no argument it runs `serve`. `healthcheck` exits 0 when `http://127.0.0.1:<port>/healthz` answers 200 within 3 seconds, and 1 otherwise. `version` prints the build version. Any other argument exits with status 2.

### Settings stored in the database

The backup hour and number kept, raw retention, storm grouping, check regions, update feed and mode, and similar options are set in Settings (admin only) and are included in database backups.

---

## 7. Data, backups and restore

### 7.1 What is on disk

| Path | Contents |
| --- | --- |
| `DATA_DIR/opskest.db` | The SQLite database, plus `opskest.db-wal` and `opskest.db-shm` while running. |
| `DATA_DIR/encryption.key` | The generated encryption key (64 hex characters, mode 0600). Absent if you set `SECRET_ENCRYPTION_KEY`. |
| `DATA_DIR/restore-pending.db` | Transient: a restore staged for the next start. |
| `BACKUP_DIR/opskest-YYYYMMDD-HHMM.db` | Backups (consistent copies made with `VACUUM INTO`). The timestamp is the time the backup ran, in the server process's local time zone (UTC in the container image). |
| `BACKUP_DIR/pre-restore-YYYYMMDD-HHMM.db` | A safety copy of the previous database, made at the moment of a restore. Settings > Backups lists these and can restore or delete them, but the backup rotation never removes them, so delete old ones yourself. |

All writes go through one SQLite writer. Do not copy a running `opskest.db` by hand: its write-ahead log can hold committed data that a plain file copy misses. Use the backup feature.

### 7.2 Backup schedule

Settings > Backups sets the hour (0 to 23 in the display zone, default 2) and the number of snapshots kept (1 to 60, default 7). You can also back up now, download, delete or upload. The scheduler checks hourly from process start, so the minute within the hour varies, and it makes at most one scheduled backup per calendar day for each run of the process. Backups contain the database only, not the encryption key. The in-app updater also takes a backup before it installs.

Copy backups off the host (another disk, object storage, another machine) on a schedule you control. Backups in `./backups` on the same machine do not protect you from losing the machine.

### 7.3 The encryption key

Channel configurations, monitor passwords and tokens, secret headers, visitor Slack webhooks and two-factor secrets are encrypted with one key. If `SECRET_ENCRYPTION_KEY` is unset, Opskest creates `DATA_DIR/encryption.key` once and reuses it. A corrupt key file is an error and is never replaced, because a new key would make every stored secret unreadable. If you lose the key, a database copy cannot be decrypted. Opskest has no key rotation procedure, so treat the key as permanent.

- Back the key up when you first create it: copy `DATA_DIR/encryption.key` to a password manager or secrets store.
- Keep it separate from the database backups where you can, because a backup file plus the key exposes every stored secret. Make sure it stays available, so that you can still restore.
- To move to a new host, restore the database and place the same key in the new `DATA_DIR/encryption.key` (mode 0600, owned by the service user) before the first start, or set `SECRET_ENCRYPTION_KEY` to its value. If you move from the key file to the environment variable, the variable must hold the same 64 hex characters.

```bash
# plain Linux
sudo cat /opt/opskest/data/encryption.key
# Compose
cat data/encryption.key
```

### 7.4 Restore

From the web interface (admin): open Settings > Backups and choose a backup, or upload a file of up to 1 GiB. Opskest validates it (SQLite integrity check, required tables, and that every recorded migration is known to this version, so a database from a newer version is refused), stages it as `restore-pending.db` and exits. On the next start it copies the current database to `BACKUP_DIR/pre-restore-YYYYMMDD-HHMM.db`, removes the old `-wal` and `-shm` files and moves the staged file into place. Something must start the process again: Compose (`restart: unless-stopped`) and the shipped systemd unit (`Restart=always`) do, but a plain binary or a cron job must be started by hand. If validation fails at boot, the staged file is deleted and the current database is kept.

Offline, with the service stopped: use the same boot-time mechanism by placing the backup as the pending file.

```bash
# Compose
docker compose stop opskest
sudo cp backups/opskest-20261031-0200.db data/restore-pending.db
sudo chown 65532:65532 data/restore-pending.db
docker compose start opskest
docker compose logs --tail 20 opskest      # look for "boot restore: swapped in restored database"
```

```bash
# plain Linux
sudo systemctl stop opskest
sudo install -o opskest -g opskest -m 0644 /path/to/opskest-20261031-0200.db /opt/opskest/data/restore-pending.db
sudo systemctl start opskest
journalctl -u opskest -n 20                # look for "boot restore: swapped in restored database"
```

The pending file is consumed (renamed or deleted), so keep your original backup. If you are restoring onto a different host, restore the matching `encryption.key` first.

Practise restores in an isolated install with alert traffic blocked. Restored channels and subscribers can message real people.

### 7.5 Migrations

Schema migrations run automatically at start and only move forward. Rolling back to an older version works only if that version understands the database. Otherwise, restore the pre-upgrade backup.

---

## 8. Security checklist for production

1. HTTPS and `APP_URL`. Terminate TLS at the proxy and set `APP_URL` to the exact `https://` origin. This makes cookies Secure (the session cookie is also HttpOnly, and both cookies are SameSite=Lax). A Secure cookie is not sent over plain HTTP, so sign-in appears not to stick on an `http://` address when `APP_URL` is `https://`.
2. Origin must equal `APP_URL`. Setup and sign-in return 403 ("cross-site request rejected") when the browser origin differs in scheme, host or port. Loopback spellings (localhost, 127.0.0.1, `[::1]`) count as one only when `APP_URL` is itself loopback.
3. Bind to loopback. Compose binds `127.0.0.1:8085`. For the plain binary, set `LISTEN_ADDR=127.0.0.1:<port>`. Docker-published ports are not blocked by host firewalls such as ufw (Docker adds its own rules), so the loopback bind is the control that matters. Widen it only behind an ALB and with a restrictive security group.
4. Create the first administrator before you expose the address. First-run setup works once, with no seeded credentials, so anyone who reaches an uninitialised install first can claim it. Do setup through an SSH tunnel or on loopback, then open the proxy.
5. The proxy must be the only way in, and it must set `X-Real-IP` (the nginx file does). Otherwise clients can pick their own rate-limit key.
6. Firewall. Allow 22, 80 and 443 only (section 3.6). Do not open 8080 or 8085.
7. Accounts. Use strong, unique passwords (12 to 128 characters), give people the lowest role they need (viewer, user or admin), and turn on authenticator-app two-factor authentication, keeping the recovery codes outside Opskest. You can require two-factor authentication for everyone in Settings > Team access. Sign-in is throttled to 15 requests per user name and 60 per source address per 10 minutes.
8. Metrics. Set `METRICS_TOKEN` to a long random value (`openssl rand -hex 32`) only if you scrape `/metrics`, and limit who can reach it at the proxy (section 4.2). Leave it empty otherwise, and the endpoint answers 404.
9. Secrets and files. Keep `.env` at mode 0600. Back up `encryption.key` separately (section 7.3). Opskest never logs secrets, payloads or channel configurations.
10. Updates. Update regularly (section 2.6 or 3.7). For the in-app updater, verify the release public key out of band and publish `manifest.json` last. Treat the update feed host as trusted infrastructure. Official release builds check the project's GitHub releases by default; turn off Check for updates in Settings > About & updates if you do not want that.
11. Privacy. Monitors created in the web form check from this server only. A monitor set to worldwide or a custom country list (or created through the API without `regionMode`) sends its hostname to Globalping; see Settings > Check regions. Alerts include the target, reason and the start of the response body, so send them only to channels you trust.
12. Back up off the host, and rehearse a restore.
13. Status pages. Public pages never expose targets, reasons, runbooks, tags or team notes, and private pages answer 404 to signed-out visitors. If you serve a page on your own domain, forward only `/status/`, `/api/public/`, `/assets/`, `favicon` and `robots.txt` from that domain to Opskest, with the streams unbuffered. Sign-in still works only from `APP_URL`.

---

## 9. Scaling and performance notes

Opskest is a single node with SQLite and no clustering. All database writes go through one writer (a 20,000-item queue, committing every 1 s or 500 items).

### Live status page viewers

Every status page viewer holds one long-lived Server-Sent Events connection (`/api/public/pages/<slug>/stream`) while the page is open and visible.

- A browser tab that has been hidden for 30 seconds closes its stream and reconnects when it becomes visible again, so background tabs do not hold connections indefinitely.
- Browsers allow at most 6 HTTP/1.1 connections per site, and each open stream uses one of them. With several tabs of the same site open (the dashboard and status pages), streams can starve other requests on HTTP/1.1. Enable HTTP/2 on the proxy (section 4.2) so that all requests and streams from one browser share a single multiplexed connection. Opskest itself speaks HTTP/1.1 only, and the proxy talks HTTP/1.1 to it, using one upstream connection per open stream.
- Each viewer costs the proxy two connections (browser side and upstream side), so the proxy's connection limits bound the number of simultaneous viewers (for nginx, `worker_connections` and the open-file limit). The Opskest process sets no connection cap of its own.
- Both kinds of stream send a `: ping` keep-alive about every 15 seconds, so idle connections are not closed by proxies or load balancers with timeouts of a minute or more.
- Opskest builds each public page snapshot once per engine change and shares the same bytes with every viewer of that page. A viewer is sent data only when the page's snapshot has changed; pings and changes to unrelated monitors send it nothing. The daily history for a page is cached for one minute per page, monitor set and window, and live status is read from memory on each request.
- Every connected client receives each global change notification through a small buffer (4 messages). A client that does not drain it is dropped, and its browser reconnects with backoff and gets a fresh snapshot.
- The public rate limit is per client address: 20 requests per 10 seconds sustained, with a burst of 40, kept in memory. It applies to the page, stream, history, incident, latency, feed, summary and badge endpoints of public pages (private pages are exempt). Subscribe requests have a stricter limit of 5 per 10 minutes. Because the key is the client address from `X-Real-IP`, visitors behind one NAT share a bucket, and a proxy that does not pass the real address makes everyone share the proxy's bucket (section 4.6).
- JSON for a status page can run to a few hundred KB uncompressed. Opskest gzips it for clients that accept it.

### Monitoring load

`CHECK_CONCURRENCY` (default 256, maximum 2048) caps simultaneous checks. If checks are queueing or the lag metric `opskest_scheduler_lag_p99_seconds` rises, look at that setting first, then at the CPU limit.

### Disk

Retention settings control growth: raw checks (1 to 90 days, default 7), notification log 30 days, audit log 365 days, hourly rollups 90 days and daily rollups 730 days. Settings > Storage shows what is held, clears older history and compacts the file.

---

## 10. Verification after deploy and troubleshooting

### 10.1 Verification

1. Health: Opskest answers `ok` on `/healthz`, locally and through the proxy.

```bash
curl -fsS http://127.0.0.1:8085/healthz; echo
curl -fsS https://status.example.com/healthz; echo
docker compose exec opskest /opskest healthcheck; echo "exit code: $?"     # Compose only
```

2. Sign in at `APP_URL` exactly (create the first administrator if this is a new install). The browser address must match `APP_URL`.
3. Live updates through the proxy. The signed-in feed `/api/stream` needs a session, so check it in the browser: open the developer tools, go to the Network tab, and confirm that the `stream` request stays open and the dashboard updates within a second or two of a change. For a status page, create a public page with one monitor, then run:

```bash
curl -N -sS https://status.example.com/api/public/pages/<slug>/stream
```

You should see `event: snapshot` and a `data:` line immediately. Change a service's state (for example, set a manual monitor down) and confirm that a new `snapshot` arrives within about a second. If nothing arrives until you stop the command, or it arrives in bursts, the proxy is buffering.

4. Add a test monitor (for example an HTTP monitor for a known-good URL and one for a closed port) and confirm that up and down are detected.
5. Add a channel in Settings > Alert delivery and press Test. Then take a monitor down and bring it back, and confirm that both the Down and Recovered messages reach a real destination.
6. Take a backup (Settings > Backups), confirm that a file appears in the backup folder, and rehearse a restore somewhere isolated.
7. Confirm that a private page answers 404 when you are signed out, and that a public page shows no targets or reasons.

### 10.2 Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| Startup stops with `cannot write <dir>: on the host run  sudo chown -R 65532:65532 <dir>` | The data or backup folder is not writable by the process. Compose: `sudo chown -R 65532:65532 data backups`. Plain Linux: `sudo chown -R opskest:opskest /opt/opskest`; if you moved the folders, also add them to `ReadWritePaths`. |
| Setup or sign-in returns 403 "cross-site request rejected" | The browser address differs from `APP_URL` in scheme, host or port (for example `127.0.0.1` against `localhost`, `:8085` against `:8080`, `http` against `https`), or the request is cross-site. Open `APP_URL` exactly, or correct `APP_URL` and restart. Behind a proxy, `APP_URL` must be the public `https://` origin. |
| Sign-in succeeds but the session is lost | `APP_URL` is `https://` (Secure cookies) but the browser is using plain HTTP. Use `https`, or use an `http://` `APP_URL` for local HTTP only. |
| 429 "too many attempts" at sign-in | 15 attempts per user name or 60 per source address in 10 minutes. Behind a proxy, all users share the proxy's address. Wait it out. |
| 429 on a public status page | The per-client public rate limit (20 per 10 s, burst 40). A proxy or CDN that does not pass the real client address (`X-Real-IP`) makes every visitor share one bucket (section 4.6). |
| Ping monitors fail with a permission error | Unprivileged ICMP needs `net.ipv4.ping_group_range` to include the process group. Compose sets it. On plain Linux, see section 3.4 and verify with `sysctl net.ipv4.ping_group_range`. Ping on Windows is untested (section 5.1). |
| Failing monitors show unknown, not down | The internet-loss guard thinks the server is offline because TCP 53 to `1.1.1.1`, `8.8.8.8` and `208.67.222.222` is blocked. Allow that outbound traffic. |
| Live updates stall or arrive in bursts | The proxy is buffering `/api/stream` or `/api/public/pages/<slug>/stream`. Turn off buffering for exactly those two paths and raise the read timeout (nginx: `proxy_buffering off`, `proxy_read_timeout 1h`). Check CDN settings too. |
| Status page streams keep reconnecting | Something between the browser and Opskest is closing idle or long connections sooner than the 15 second keep-alive ping can prevent. Check proxy, load balancer and CDN idle and read timeouts (section 4.5). |
| 502 or 504 from the proxy | 502: Opskest is not running, or the proxy points at the wrong address. Check `curl -fsS http://127.0.0.1:8085/healthz`, `systemctl status opskest` or `docker compose ps`, and the `proxy_pass` port. 504: a stream or upload exceeded a proxy timeout. Raise `proxy_read_timeout` for streams, and `proxy_send_timeout` and `client_max_body_size` for restore uploads. 413: `client_max_body_size` is too small. |
| `listen :8080: bind: address already in use` (or the same for another address) | Another process holds the port. Find it with `sudo ss -ltnp \| grep -E ':(8080\|8085)'`. Plain binary: change `LISTEN_ADDR` or `PORT`. Compose: set `OPSKEST_PORT`, for example `8086`, and update the proxy. |
| `APP_URL: must be an absolute http(s) URL` | `APP_URL` lacks a scheme or host. |
| `SECRET_ENCRYPTION_KEY: must decode to 32 bytes`, or an `encryption key:` error naming `encryption.key` | The key is not 64 hex characters, or the key file is corrupt. Opskest never replaces a corrupt file. Restore the correct key from your backup. |
| Opskest exited after a restore or an update | A restore (and an update in `exit` mode) makes Opskest exit with status 0 by design. The supervisor must restart it: Compose and the shipped unit do. Otherwise, start it by hand. |
| `docker compose pull` finds no image, or the version says `dev` | You are running a source build (`docker-compose.build.yml`). Use the default `docker-compose.yml`, which pulls the official image. |
| The update screen says it cannot install | The install is Docker or package-managed, the build is a development build (`dev`), no public key is set, or the binary's folder is not writable by the service user (`ReadWritePaths` must include it). |
| `/metrics` returns 404 or 401 | 404: `METRICS_TOKEN` is unset. 401: the `Authorization: Bearer <token>` header is missing or wrong. |
| Healthcheck fails although the app works | `/opskest healthcheck` always probes `127.0.0.1` on the port from `LISTEN_ADDR` or `PORT`. If `LISTEN_ADDR` is a specific non-loopback address, nothing answers there. |
