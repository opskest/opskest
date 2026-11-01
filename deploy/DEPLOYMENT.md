# Opskest deployment guide

This guide covers every supported way to run Opskest: Docker Compose (recommended), a plain Linux host under systemd, a Windows service wrapper, and macOS or other platforms. It also covers the reverse proxy and TLS in front of it, configuration, backups, security, scaling and troubleshooting.

Everything here was checked against the code and the files in this repository (`Dockerfile`, `docker-compose.yml`, `.env.example`, `deploy/`, `cmd/opskest`, `internal/config`, `internal/update`, `internal/app`, `internal/api`, `internal/hub`). Statements that could not be verified from the repository are labelled "not verified" or "untested". For features, roles and the user interface, see `Docs.md`.

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

## 1. Overview, decision table and requirements

Opskest is one Go program with the web interface embedded and a SQLite database. There is no database server, message queue or web server to install. It serves plain HTTP on one address and has no TLS or ACME client of its own, so a public install always puts a reverse proxy in front of it.

There is no published image or release download at present. You build from source (a Docker image through Compose, or a binary through `scripts/build-release` or `go build`).

### Which method

| Method | Best for | Notes |
| --- | --- | --- |
| Docker Compose (section 2) | Most installs. A single VM or server with Docker. | Reproducible build, distroless non-root image, healthcheck, restart policy and the ping sysctl are already set. Updates are done by rebuilding. The in-app updater only notifies. |
| Plain Linux with systemd (section 3) | Hosts without Docker, or where you want the signed in-app updater. | You manage the user, directories, sysctl and unit file. The in-app updater can replace the binary. |
| Windows (section 5) | Windows servers. | The binary builds for Windows but no service wrapper is shipped. Ping monitors are probably not usable there (see section 5). |
| macOS and other (section 5) | Trying it out. | Binary only, no supervisor files shipped. |
| Behind an AWS ALB | Compose or plain Linux, with ACM certificates. | See section 4.5. |

### Requirements

| Item | Guidance |
| --- | --- |
| CPU and RAM | The stated target is 2,000 monitors at a 30 s interval with scheduler lag p99 under 1 s, RSS under 150 MB and under 0.5 vCPU. The latest recorded load test (`Docs.md`, section 15: 2 CPUs and 1 GB limits, Docker on Windows) measured RSS 61.4 MB and 0.100 cores for 2,000 monitors. These are point-in-time numbers on those hosts, not guarantees. The Compose file caps the container at 1 CPU and 512 MB. For a typical install, 1 vCPU and 512 MB of RAM is a reasonable start. No figure has been measured for large numbers of status page viewers. |
| Disk | The database lives in `DATA_DIR`. Each scheduled or manual backup is a full copy of the database (`VACUUM INTO`), and 7 are kept by default, so plan for roughly (number kept + 1) times the database size plus headroom. Raw check history is kept 7 days by default (1 to 90 days); hourly rollups are kept 90 days and daily rollups 730 days. Watch Settings > Storage after the first week to see the real size. Use local disk, not a network share. |
| Operating system | Linux for production (the image and the systemd unit assume it). The release script builds linux amd64 and arm64, windows amd64 and darwin arm64. |
| Build tools | Docker with a recent Compose v2 for section 2. The `env_file` entry in `docker-compose.yml` uses the `required: false` form, which needs Compose 2.24 or newer (from the Compose release notes, not verified here). For a native build: Go 1.27 (pinned in `go.mod`), Node 22 with npm, and bash. |
| Inbound ports | Only the reverse proxy needs 80 and 443. Opskest itself listens on 8080 by default for the plain binary, and the Compose stack publishes `127.0.0.1:8085` only. Do not expose either to the internet. |
| Outbound access | Checks to your targets (TCP, HTTP, DNS, ICMP echo for ping monitors). Alert channels you configure (Slack, SMTP and so on). TCP port 53 to `1.1.1.1`, `8.8.8.8` and `208.67.222.222` for the internet-loss guard: if none answers, failing monitors are recorded as unknown, not down. Optional and on by default for public HTTP, ping and DNS monitors: `api.globalping.io` (Settings > Check regions). Only when used: `data.iana.org` and registry RDAP servers (domain expiry), and your update feed URL. |
| Time | Keep the host clock accurate (NTP). Daily rollups, the backup hour and maintenance windows use the display time zone (`DISPLAY_TZ`). |
| DNS and TLS | A hostname pointing at the proxy and a certificate for it. |

---

## 2. Docker Compose (recommended)

Compose builds the image locally from the `Dockerfile` (three stages: Node 22 builds the web app, Go 1.27 builds the binary with `CGO_ENABLED=0`, and the result is copied into `gcr.io/distroless/static-debian12:nonroot`). The container runs as uid 65532, has no shell, listens on 8080 inside, and is published on the host as `${OPSKEST_BIND:-127.0.0.1:8085}`.

### 2.1 First start

```bash
git clone <your copy of this repository> opskest && cd opskest
mkdir -p data backups
sudo chown -R 65532:65532 data backups
cp .env.example .env
```

Edit `.env` (every line in `.env.example` is commented out by default). For anything other than a local trial, set at least:

```ini
APP_URL=https://status.example.com
```

Then build and start:

```bash
docker compose up -d --build
docker compose ps
docker compose logs -f opskest
```

Without a `.env`, Compose defaults `APP_URL` to `http://localhost:8085`, which is right for a local trial: open <http://localhost:8085>.

Settings that matter for Compose:

| Setting | Where | Meaning |
| --- | --- | --- |
| `APP_URL` | `.env` | The exact address people open, with scheme, host and port. `https://` turns on Secure cookies. Setup and sign-in are refused with HTTP 403 from any other origin. Compose uses `http://localhost:8085` if you set nothing. |
| `OPSKEST_BIND` | `.env` | Host address the container port is published on. Default `127.0.0.1:8085`. Keep it on loopback behind a reverse proxy. Use `0.0.0.0:8085` only behind an ALB or a firewall that restricts the port. If you change the port, update every `proxy_pass` in your proxy config. |
| `OPSKEST_DATA_DIR` | `.env` | Host folder mounted as `/data`. Default `./data`. |
| `BACKUP_HOST_DIR` | `.env` | Host folder mounted as `/backups`. Default `./backups`. |
| `VERSION` | `.env` | Image tag (`opskest:<VERSION>`, default `latest`) and the version string built into the binary. |
| `SECRET_ENCRYPTION_KEY`, `DISPLAY_TZ`, `SERVER_REGION`, `METRICS_TOKEN`, `CHECK_CONCURRENCY`, `LOG_LEVEL` | `.env` | Optional, see section 6. |

Do not set `DATA_DIR`, `BACKUP_DIR`, `PORT` or `LISTEN_ADDR` in `.env` for Compose. The whole `.env` is passed into the container as environment, and those values would override the paths and port the image sets (`DATA_DIR=/data`, `BACKUP_DIR=/backups`, `LISTEN_ADDR=:8080`).

### 2.2 Ownership of data and backups

`./data` and `./backups` are bind mounts, not named volumes, and the process runs as uid 65532 with no shell to fix permissions from inside the container. They must be writable by that uid on the host before the first start:

```bash
sudo chown -R 65532:65532 data backups
```

If they are not writable, Opskest stops at boot, before it opens the database, with a one-line message that names the exact command (`cannot write <dir>: on the host run  sudo chown -R 65532:65532 <dir>`). If you use `OPSKEST_DATA_DIR` or `BACKUP_HOST_DIR`, chown those folders instead.

### 2.3 The ping sysctl

`docker-compose.yml` sets `net.ipv4.ping_group_range: "0 2147483647"` on the container, so ping monitors can use unprivileged ICMP without granting `NET_RAW`. Keep that line. Removing it makes every ping monitor fail with a permission error unrelated to the monitor's own settings.

### 2.4 Healthcheck, limits and logs

- The healthcheck runs `/opskest healthcheck`, a subcommand that requests `http://127.0.0.1:<port>/healthz` and exits 0 on HTTP 200 (the distroless image has no curl or wget). It runs every 30 s with a 5 s timeout, 3 retries and a 10 s start period in the Compose file.
- Resource limits in the Compose file: 1 CPU and 512 MB.
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

The in-app updater does not replace a container: Docker installs are only notified. To upgrade, back up, pull the new source and rebuild:

```bash
# 1. In the web interface: Settings > Backups > back up now (or wait for the scheduled one).
# 2. Update the source and rebuild:
cd opskest
git pull
docker compose up -d --build
docker compose ps
```

Migrations run automatically on start and only move forward. To keep an easy way back, give each build its own tag so the previous image stays on disk:

```bash
VERSION=1.1.0 docker compose up -d --build
# roll back to an image you still have:
VERSION=1.0.0 docker compose up -d
```

If an older version refuses a database that a newer one has migrated, restore the backup you took before the upgrade (section 7). The update screen in the UI shows the hint `docker compose pull && docker compose up -d`; that does not work for a locally built image (there is no registry image to pull). Use `docker compose up -d --build`.

### 2.7 Backup, restore, logs and removal

Backups and restore are covered in section 7. Both folders are on the host, so they survive `docker compose down` and `docker compose down -v`.

```bash
docker compose down                 # stop and remove the container; data and backups stay on the host
docker compose restart opskest
docker compose stop opskest         # stop without removing
```

---

## 3. Plain Linux (bare metal or VM) with systemd

Use this when you do not want Docker, or when you want the signed in-app updater to replace the binary.

### 3.1 Get a binary

Build on any machine with Go 1.27, Node 22 and bash. `scripts/build-release` builds the web interface and then produces four binaries in `dist/`:

```bash
git clone <your copy of this repository> opskest-src && cd opskest-src
scripts/build-release 1.0.0
ls -l dist
# dist/opskest-linux-amd64  dist/opskest-linux-arm64  dist/opskest-windows-amd64.exe  dist/opskest-darwin-arm64
```

Use a real version number such as `1.0.0`. A build left at the default `dev` is a development build and never updates itself. For signed in-app updates, see section 3.7 about the release public key.

To build one binary by hand (the web interface must be built first, because Vite writes to `internal/webui/dist`, which Go embeds):

```bash
cd web && npm ci && npm run build && cd ..
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=1.0.0" -o opskest ./cmd/opskest
./opskest version
```

Without local Go and Node, the Docker wrappers do the same: `scripts/build-web` builds the web app and `scripts/go build ...` runs the Go toolchain in a container (this produces files owned by root; `chown` them afterwards).

### 3.2 User, directories and files

These steps follow the header of `deploy/opskest.service`. Run them from the repository root, with the built binary at `./opskest` (copy it from `dist/opskest-linux-amd64`, for example `cp dist/opskest-linux-amd64 opskest`):

```bash
sudo useradd --system --home /opt/opskest --shell /usr/sbin/nologin opskest
sudo install -d -o opskest -g opskest /opt/opskest
sudo install -o opskest -g opskest -m 0755 opskest /opt/opskest/opskest
```

Keep the binary in a folder the `opskest` user owns (here `/opt/opskest`). The updater swaps the file by writing next to it, and it refuses to update a binary under package-managed paths such as `/usr/bin`, `/usr/sbin`, `/bin`, `/sbin`, `/usr/lib` or `/snap`. Data and backups are created under `/opt/opskest/data` and `/opt/opskest/backups` because the unit's working directory is `/opt/opskest` and the defaults are relative (`./data`, `./backups`).

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

Why these three:

- `APP_URL` is the exact public origin (section 6).
- `LISTEN_ADDR=127.0.0.1:8085` keeps Opskest on loopback, behind the proxy, and matches the shipped `deploy/nginx.host.conf` (which proxies to `127.0.0.1:8085`). Without it the default is `:8080` on all interfaces.
- `OPSKEST_UPDATE_RESTART=exit` is for the signed in-app updater under a supervisor (section 3.7). Leave it out if you will not use the updater.

For a local trial without a proxy, leave `.env` empty or absent: Opskest listens on `:8080` and `APP_URL` defaults to `http://localhost:8080`. Open it as `localhost`, not `127.0.0.1`, unless `APP_URL` says otherwise (the loopback spellings count as one only when `APP_URL` is itself a loopback address with the same port).

### 3.4 Unprivileged ping

Ping monitors use unprivileged ICMP (UDP-style) sockets. On Linux that needs `net.ipv4.ping_group_range` to include the group of the process. The shipped unit does not set it (it only carries a comment), so set it on the host and persist it:

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

What the shipped unit does, line by line:

| Line | Effect |
| --- | --- |
| `After=network-online.target`, `Wants=network-online.target` | Start after the network is configured. |
| `User=opskest`, `Group=opskest` | Run as the dedicated unprivileged account. Its group id must fall inside `ping_group_range`. |
| `WorkingDirectory=/opt/opskest` | Relative paths and `.env` resolve here, so data is `/opt/opskest/data` and backups `/opt/opskest/backups`. |
| `ExecStart=/opt/opskest/opskest serve` | Starts the server. |
| `Restart=always`, `RestartSec=3` | Restart 3 seconds after any exit, including a crash and the clean exit (status 0) that follows a database restore from Settings or an update in `exit` mode. A deliberate `systemctl stop` still stops it. |
| `NoNewPrivileges=true` | The process and its children cannot gain privileges (setuid binaries, file capabilities). |
| `ProtectSystem=strict` | The whole file system hierarchy is mounted read-only for the service, except `/dev`, `/proc` and `/sys` and the paths listed in `ReadWritePaths`. |
| `ProtectHome=true` | `/home`, `/root` and `/run/user` are inaccessible. |
| `PrivateTmp=true` | The service gets its own private `/tmp` and `/var/tmp`. |
| `ReadWritePaths=/opt/opskest` | The only writable tree: database, backups, encryption key, and the binary's own folder (needed for in-app updates). |
| `WantedBy=multi-user.target` | Start at boot once enabled. |

Consequences to remember:

- If you move `DATA_DIR` or `BACKUP_DIR` outside `/opt/opskest`, add those paths to `ReadWritePaths` (use `sudo systemctl edit opskest` for a drop-in) and create them with the right owner. A nonexistent `ReadWritePaths` entry stops the unit from starting.
- To change `ExecStart` or add `Environment=` lines, use a drop-in rather than editing the shipped file, then `sudo systemctl daemon-reload && sudo systemctl restart opskest`.
- Restart after editing `.env`: `sudo systemctl restart opskest`.

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

Allow only SSH and the proxy ports. Do not open 8080 or 8085, since Opskest should be reachable only through the proxy. For example with ufw:

```bash
sudo ufw allow OpenSSH
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
sudo ufw status
```

### 3.7 Upgrading

There are two ways.

**By hand (always works):**

```bash
# 1. Settings > Backups > back up now. Then build or copy the new binary, and:
sudo systemctl stop opskest
sudo cp /opt/opskest/opskest /opt/opskest/opskest.old
sudo install -o opskest -g opskest -m 0755 opskest /opt/opskest/opskest
sudo systemctl start opskest
journalctl -u opskest -n 50
```

Migrations run automatically and only move forward. To go back, restore the binary and, if the older version refuses the newer database, restore the pre-upgrade backup (section 7).

**Signed in-app update (plain-binary installs only).** In Settings > About and updates an admin sets a release feed (an HTTPS URL of `manifest.json`; plain `http://` is accepted only for `localhost`, `127.0.0.1` and `::1`) and a public key. Opskest then checks the feed, and on **Update now** (or automatically in `auto` mode) it takes a database backup, downloads the new binary, verifies the manifest's ed25519 signature (`<feed URL>.sig`), size and SHA-256, checks the file is an executable for the OS, runs `<new> version` and compares the reported version, swaps the file (keeping the old one as `<exe>.prev`) and restarts. Nothing is contacted until a feed URL is set. Preconditions the code checks before allowing an install: the install method is a plain binary (not Docker, not a package-managed path), the build has a real version number, a public key is available (saved in Settings, or built in with `OPSKEST_UPDATE_PUBLIC_KEY` when you run `scripts/release`/`scripts/build-release`), and the process can create files in the binary's folder. You publish the feed yourself with `scripts/release` (see `Docs.md`, section 12).

How the restart happens depends on `OPSKEST_UPDATE_RESTART`:

| Value | What happens after the swap | Use it when |
| --- | --- | --- |
| `self` (default; any unrecognised value also means `self`) | Opskest shuts down gracefully, closes the database, then starts the new binary itself: on Unix it `exec`s the new file in place (same PID); on Windows it starts a detached copy and exits. If the new binary cannot be started, it swaps the `.prev` file back and starts that instead. | Running the binary by hand, from cron, or any setup with no supervisor. |
| `exit` | Opskest swaps the files, shuts down gracefully and exits with status 0, and relies on the supervisor to start the new file. | systemd with `Restart=always` (as in the shipped unit), a Windows service wrapper such as NSSM or WinSW. This is what `Docs.md` recommends under a supervisor, because spawning a second copy would fight the supervisor's copy for the port. |

Under systemd, `exit` is the documented choice. Reading the code, the in-place `exec` of `self` mode keeps the PID that systemd tracks, so it should also work, but it is not what the project documents. One difference: the automatic "start the old binary if the new one will not start" fallback exists only in `self` mode. In `exit` mode, if the new binary is broken, systemd will keep restarting it every 3 seconds; roll back by hand:

```bash
sudo systemctl stop opskest
sudo mv /opt/opskest/opskest.prev /opt/opskest/opskest
sudo systemctl start opskest
```

The Settings screen also has a one-click **Roll back** that swaps the current binary and `.prev`. Because migrations only move forward, if the rolled-back version refuses the database, restore the backup named in the update.

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

Opskest serves plain HTTP only. The proxy on the host owns ports 80 and 443 for every tool on the box and the certificate lifecycle. The Opskest stack never binds 80 or 443 and never runs its own ACME client.

Requirements for any proxy, all from the code:

1. **`APP_URL` must equal the public origin** (scheme, host, port). Opskest does not read `Host` or `X-Forwarded-Proto` from the proxy. The scheme of `APP_URL` alone decides whether cookies are Secure. Setup and sign-in return 403 ("cross-site request rejected") when the browser's `Origin` differs from `APP_URL`, or when `Sec-Fetch-Site` is `cross-site`.
2. **Do not buffer the two live streams**: `/api/stream` (the signed-in dashboard) and `/api/public/pages/<slug>/stream` (public and private status pages). They are long-lived Server-Sent Events responses. Opskest also sends `X-Accel-Buffering: no`, which nginx honours, but configure it explicitly as well. Give these two paths a long read timeout.
3. **Set the client address and make the proxy the only way in.** The public status page rate limit (and badge, feed and subscribe limits) is keyed by the `X-Real-IP` header, else the first value of `X-Forwarded-For`, else the socket address. A client that reaches Opskest directly can choose that value. Sign-in throttling uses the socket address only (the proxy's address when behind a proxy, so all users share one 60-attempts-per-10-minutes bucket for the source address).
4. **Do not compress the streams.** Opskest already gzips JSON, HTML, CSS, JavaScript and SVG for clients that send `Accept-Encoding: gzip`, and never wraps the streams. Do not enable proxy compression that would buffer them.
5. **Allow large request bodies where needed.** Restore uploads can be up to 1 GiB and monitor imports up to 5 MiB.
6. **Enable HTTP/2 to browsers** (section 9).

### 4.1 nginx on the host

`deploy/nginx.host.conf` is a ready server block for HTTP port 80 that proxies to `127.0.0.1:8085`. It sets `X-Real-IP` from `$remote_addr` (overwriting anything the client sent), has an ACME challenge location (`/.well-known/acme-challenge/`, root `/var/www/html`), a regex location for the two stream paths with `proxy_buffering off`, `proxy_cache off`, `proxy_http_version 1.1`, an empty `Connection` header and `proxy_read_timeout 1h`, a location for `POST /api/backups/upload` with `client_max_body_size 1025m` and 5-minute timeouts, and a general location with 30 s timeouts and `client_max_body_size 2m`.

Install it (replace the hostname; the Debian and Ubuntu package names are shown):

```bash
sudo apt install nginx certbot python3-certbot-nginx
sudo cp deploy/nginx.host.conf /etc/nginx/sites-available/opskest
sudo sed -i 's/opskest\.example\.com/status.example.com/' /etc/nginx/sites-available/opskest
sudo ln -s /etc/nginx/sites-available/opskest /etc/nginx/sites-enabled/opskest
sudo nginx -t
sudo systemctl reload nginx
```

If Opskest listens somewhere other than `127.0.0.1:8085`, change every `proxy_pass` in the file. If you import monitor files larger than 2 MiB (the application accepts up to 5 MiB), raise `client_max_body_size` in the general location.

Complete first-admin setup privately before opening public access (for example through an SSH tunnel to the loopback port), then continue.

### 4.2 Certificates with certbot (on the host)

The certificate belongs to the host proxy, never to the Opskest stack. With the nginx plugin:

```bash
sudo certbot --nginx -d status.example.com
sudo certbot renew --dry-run
systemctl list-timers | grep -i certbot
```

Certbot edits the server block to add the HTTPS listener and a redirect. Check the result with `sudo nginx -t`, load the site over HTTPS, and confirm renewal (the dry run above, and the timer or cron entry your distribution installs). Then set `APP_URL=https://status.example.com` in Opskest's `.env` and restart Opskest, so cookies become Secure and alert links use HTTPS.

Without the nginx plugin you can use the webroot method that the shipped ACME location supports:

```bash
sudo certbot certonly --webroot -w /var/www/html -d status.example.com
```

then add the `listen 443 ssl` block and certificate paths to the nginx config yourself.

**HTTP/2.** Certbot does not add it. Enable it on the HTTPS listener: change the certbot-managed `listen 443 ssl;` to `listen 443 ssl http2;`, or on nginx 1.25.1 or newer add `http2 on;` inside the server block. Reload with `sudo nginx -t && sudo systemctl reload nginx`.

Optional: keep Prometheus metrics off the public internet by allowing only your scraper. This is not part of the shipped file; add it before the general `location /` block:

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

`deploy/apache.htaccess` is a reverse-proxy example for a VirtualHost or a cPanel account's `.htaccess`. It needs `mod_proxy`, `mod_proxy_http` and `mod_rewrite`, redirects HTTP to HTTPS, and forwards everything with `RewriteRule ^(.*)$ http://127.0.0.1:8080/$1 [P,L]`. The file itself says it is **not tested here**, and this guide has not tested it either. In particular:

- Replace `8080` with the port Opskest listens on, and set `APP_URL` to the `https` address.
- Verify that the two streams reach the browser unbuffered (section 10). Whether they do depends on your Apache configuration.
- Check any `LimitRequestBody` or hosting limits against restore uploads (up to 1 GiB) and imports (up to 5 MiB).
- The file does not set `X-Real-IP`. Apache's `mod_proxy` appends the client address to any `X-Forwarded-For` the client sent, and Opskest uses the first value, which the client controls. The effect is limited to the public-page rate limit (sign-in throttling uses the socket address). A possible fix is to set `X-Real-IP` from the connection address, for example `RewriteRule ^ - [E=OPSKEST_CLIENT_IP:%{REMOTE_ADDR}]` followed by `RequestHeader set X-Real-IP "%{OPSKEST_CLIENT_IP}e"`; this is untested.
- Shared hosting rarely allows long-running processes, extra ports or the needed modules. A VPS with root access (WHM on cPanel) is the realistic case: run the binary under systemd (section 3) and proxy to it.

### 4.4 Caddy (example, not tested here)

Caddy on the host can obtain certificates itself, which is fine because it is the host's proxy, not part of the Opskest stack. This Caddyfile uses documented Caddy directives but was not run against Opskest:

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

For an ALB with ACM certificates, set `OPSKEST_BIND=0.0.0.0:8085` (Compose) or `LISTEN_ADDR=0.0.0.0:8085` (plain binary) and restrict that port in the security group to the ALB only. Configure:

- Target group health check: HTTP `GET /healthz`, expecting 200 (the body is `ok`).
- Idle timeout of at least 60 seconds. The signed-in `/api/stream` writes a `: ping` comment every 15 seconds, so it stays alive under that.
- Note that the public status page stream (`/api/public/pages/<slug>/stream`) writes only when the page's snapshot changes; it does not write the periodic ping. On a quiet page it can sit silent longer than the idle timeout, so the ALB will close it. The browser reconnects automatically (backoff of 1, 2, 5 and 10 seconds) and receives a fresh snapshot, but each reconnect also counts against the per-client rate limit. Raise the idle timeout if you want fewer reconnects.
- `APP_URL=https://<your hostname>`. TLS ends at the ALB.
- The ALB appends the client address to `X-Forwarded-For` and does not set `X-Real-IP`; Opskest uses the first `X-Forwarded-For` value, which a client can supply. The effect is limited to the public-page rate limit. Behind an ALB, sign-in throttling sees the load balancer's address.

### 4.6 Cloudflare and other CDNs

- Opskest sets `Content-Type: text/event-stream`, `Cache-Control: no-cache` and `X-Accel-Buffering: no` on the streams. Whether a given CDN passes them through unbuffered was not tested here. Test with section 10, and turn off any response buffering, caching or compression feature for `/api/stream` and `/api/public/pages/*/stream`.
- Never cache `/api/` responses that depend on a session. Opskest marks private responses `private, no-store`, and public feeds and badges cacheable for 30 to 60 seconds.
- Client address: behind a CDN, nginx's `$remote_addr` is the CDN's edge address, so every visitor would share one rate-limit bucket (20 requests per 10 seconds, burst 40) and see HTTP 429. Make nginx recover the real address with the standard `realip` module, using the CDN's published address ranges (list them from the CDN's documentation, not from this guide):

```nginx
set_real_ip_from <CDN address range>;   # repeat per range
real_ip_header CF-Connecting-IP;        # Cloudflare's header; other CDNs use other names
```

- Restrict the origin to the CDN's addresses at the firewall, so nobody can bypass it and forge headers.

---

## 5. Windows, macOS and other platforms

### 5.1 Windows (brief, partly untested)

`scripts/build-release` produces `dist/opskest-windows-amd64.exe`, and the repository's own developer script (`.dev/run-opskest-local.ps1`) runs `opskest.exe serve` on Windows with environment variables, so the binary builds and runs there. The Windows-specific code is the time zone lookup (registry, via `golang.org/x/sys`) and the update install and restart (rename aside, then start a detached copy).

Run it in the foreground to try it:

```powershell
$env:APP_URL = "http://localhost:8080"
$env:DATA_DIR = "C:\opskest\data"
$env:BACKUP_DIR = "C:\opskest\backups"
.\opskest.exe serve
```

**No service wrapper is shipped.** The binary does not implement the Windows service control interface, so `sc.exe create` cannot run it directly; the service would fail to start. Use a wrapper such as NSSM or WinSW, and set `OPSKEST_UPDATE_RESTART=exit` so the wrapper (not Opskest) starts the new file after an update. The code comments name those two wrappers; the NSSM commands below follow NSSM's documented usage and were not run here:

```powershell
nssm install Opskest "C:\opskest\opskest.exe" serve
nssm set Opskest AppDirectory "C:\opskest"
nssm set Opskest AppEnvironmentExtra APP_URL=https://status.example.com LISTEN_ADDR=127.0.0.1:8085 OPSKEST_UPDATE_RESTART=exit DATA_DIR=C:\opskest\data BACKUP_DIR=C:\opskest\backups
nssm set Opskest AppExit Default Restart
nssm start Opskest
```

Alternatively put the same settings in a `.env` file in the working directory. The restart-after-restore behaviour also needs the wrapper to restart the process after a clean exit.

Caveats: ping monitors always request an unprivileged ICMP socket, and the ping library's documentation says Windows needs the privileged mode flag instead, so ping monitors are probably not usable on Windows (not verified). The process handles Ctrl+C and termination signals for graceful shutdown; make sure your wrapper stops it that way. Use a reverse proxy such as IIS ARR, nginx for Windows or Caddy for TLS; none are provided here.

### 5.2 macOS and other platforms

`scripts/build-release` builds `dist/opskest-darwin-arm64` (Apple silicon). Run it with `./opskest serve`, and put your settings in a `.env` file beside it or in the environment. No launchd file or other supervisor is shipped; if you use one, set `OPSKEST_UPDATE_RESTART=exit` and make it restart the process after a clean exit. Other operating systems and architectures can be built with `GOOS` and `GOARCH` as in section 3.1, but only the four release targets are built by the scripts, and none of the others were tested. `scripts/release` publishes only those four targets.

---

## 6. Configuration reference

Settings come from environment variables or a `.env` file. Lookup order: a real environment variable that is set to a non-empty value wins; otherwise the value from the `.env` file in the working directory (or from the file named by `OPSKEST_ENV_FILE`) is used; otherwise the default. A missing file is not an error. Format: `KEY=VALUE` lines, `#` comments, an optional `export ` prefix, optional matching quotes around a value, and a trailing ` # comment` is stripped. Values are trimmed of surrounding spaces.

Invalid values stop startup with a one-line message naming the variable.

### Process settings (`internal/config`, `cmd/opskest`)

| Variable | Default | Meaning |
| --- | --- | --- |
| `APP_URL` | `http://localhost:<port>` (port from the listen address) | The address people open: absolute `http(s)` URL, one trailing slash is removed. `https` makes cookies Secure. Used for sign-in origin checks (403 on mismatch), alert links and push URLs. If unset, a warning is logged. |
| `LISTEN_ADDR` | none (then `PORT`, then `:8080`) | Address to listen on, for example `127.0.0.1:8085`. Wins over `PORT`. The healthcheck subcommand always probes `127.0.0.1` on this address's port. |
| `PORT` | none | Port (1 to 65535) used as `:<port>` when `LISTEN_ADDR` is unset. |
| `DATA_DIR` | `data` (relative to the working directory) | Folder for `opskest.db` and `encryption.key`. Created if missing. Must be writable. |
| `BACKUP_DIR` | `backups` | Folder for backups. Created if missing. Must be writable. |
| `SECRET_ENCRYPTION_KEY` | empty | 64 hex characters (`openssl rand -hex 32`) that encrypt channel, monitor and two-factor secrets. If empty, the key is read from `DATA_DIR/encryption.key`, which is created (mode 0600) on first start. When this variable is set, the key file is neither read nor created. |
| `DISPLAY_TZ` | empty | IANA zone (for example `Europe/Berlin`) for charts, daily rollups, the backup hour and new maintenance windows. If empty: `TZ`, then the server's zone, then UTC. `Local` is rejected. The zone database is embedded in the binary. |
| `TZ` | empty | Used only when `DISPLAY_TZ` is empty. Must be an IANA name. |
| `SERVER_REGION` | empty (UI shows "This server") | Label for where checks run from. |
| `METRICS_TOKEN` | empty | Bearer token for `GET /metrics`. Empty disables the endpoint (404). Wrong or missing header: 401. |
| `CHECK_CONCURRENCY` | `256` | Concurrent checks, 1 to 2048. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` (or `warning`), `error`, case-insensitive. |
| `OPSKEST_ENV_FILE` | `.env` | Read this file instead of `./.env`. Must be a real environment variable (it is read before the file). |
| `OPSKEST_UPDATE_RESTART` | `self` | `self` or `exit`; any other value means `self`. See section 3.7. Can be set in `.env`. |
| `OPSKEST_IN_DOCKER` | unset | Set to `1` to force the "running in Docker" detection (no self-update). Detection is otherwise automatic (`/.dockerenv`, `/run/.containerenv`, or a docker, kubepods, containerd or libpod hint in the cgroup). |

### Compose-only variables (`docker-compose.yml`)

| Variable | Default | Meaning |
| --- | --- | --- |
| `OPSKEST_BIND` | `127.0.0.1:8085` | Host address published for the container's port 8080. |
| `OPSKEST_DATA_DIR` | `./data` | Host folder mounted as `/data`. |
| `BACKUP_HOST_DIR` | `./backups` | Host folder mounted as `/backups`. |
| `VERSION` | `latest` | Image tag (`opskest:<VERSION>`) and the version built into the binary. |

The image itself sets `DATA_DIR=/data`, `BACKUP_DIR=/backups` and `LISTEN_ADDR=:8080`. Compose also supplies `APP_URL` (default `http://localhost:8085`) in `environment`, which takes precedence over a different `APP_URL` in an `env_file`; keep `APP_URL` in `.env` so both agree.

### Command-line

`opskest [serve | healthcheck | version]`. With no argument it runs `serve`. `healthcheck` exits 0 when `http://127.0.0.1:<port>/healthz` answers 200 within 3 seconds, else 1. `version` prints the build version. Any other argument exits with status 2.

### Settings stored in the database (not environment)

Backup hour and number kept, raw retention, storm grouping, check regions, update feed and mode, and so on live in Settings (admin only) and are included in database backups.

---

## 7. Data, backups and restore

### 7.1 What is on disk

| Path | Contents |
| --- | --- |
| `DATA_DIR/opskest.db` | The SQLite database, plus `opskest.db-wal` and `opskest.db-shm` while running. |
| `DATA_DIR/encryption.key` | The generated encryption key (64 hex characters, mode 0600). Absent if you set `SECRET_ENCRYPTION_KEY`. |
| `DATA_DIR/restore-pending.db` | Transient: a restore staged for the next start. |
| `BACKUP_DIR/opskest-YYYYMMDD-HHMM.db` | Backups (consistent copies made with `VACUUM INTO`). The timestamp is the time the backup ran, in the server process's local time zone (UTC in the container image). |
| `BACKUP_DIR/pre-restore-YYYYMMDD-HHMM.db` | A safety copy of the previous database made at the moment of a restore. These do not match the backup file pattern, so Settings does not list them and rotation never deletes them; remove them by hand when no longer needed. |

All writes go through one SQLite writer. Do not copy a running `opskest.db` by hand; its write-ahead log can hold committed data that a plain file copy misses. Use the backup feature.

### 7.2 Backup schedule

Settings > Backups sets the hour (0 to 23 in the display zone, default 2) and the number of snapshots kept (1 to 60, default 7). You can also back up now, download, delete or upload. The scheduler checks hourly from process start, so the minute within the hour varies, and it makes at most one scheduled backup per calendar day for each run of the process. Backups contain the database only, **not** the encryption key. The in-app updater also takes a backup before installing.

Copy backups off the host (another disk, object storage, another machine) on a schedule you control; backups in `./backups` on the same machine do not protect against losing the machine.

### 7.3 The encryption key

Channel configurations, monitor passwords and tokens, secret headers, visitor Slack webhooks and two-factor secrets are encrypted with one key. If `SECRET_ENCRYPTION_KEY` is unset, Opskest creates `DATA_DIR/encryption.key` once and reuses it. A corrupt key file is an error and is never replaced, because a new key would make every stored secret unreadable. **If you lose the key, a database copy cannot be decrypted.** There is no key rotation procedure in the documentation, so treat the key as permanent.

- Back the key up when you first create it: copy `DATA_DIR/encryption.key` to a password manager or secrets store.
- Keep it separate from the database backups where you can, because a backup file plus the key exposes every sealed secret, but make sure it stays available so you can still restore.
- Moving to a new host: restore the database and place the same key in the new `DATA_DIR/encryption.key` (mode 0600, owned by the service user) before the first start, or set `SECRET_ENCRYPTION_KEY` to its value. If you move from the key file to the environment variable, the variable must hold the same 64 hex characters.

```bash
# plain Linux
sudo cat /opt/opskest/data/encryption.key
# Compose
cat data/encryption.key
```

### 7.4 Restore

**From the web interface (admin).** Settings > Backups: choose a backup (or upload a file up to 1 GiB). Opskest validates it (SQLite integrity check, required tables, and that every recorded migration is known to this version, so a database from a newer version is refused), stages it as `restore-pending.db` and exits. On the next start it copies the current database to `BACKUP_DIR/pre-restore-YYYYMMDD-HHMM.db`, removes the old `-wal` and `-shm` files and moves the staged file into place. Something must start the process again: Compose (`restart: unless-stopped`) and the shipped systemd unit (`Restart=always`) do; a plain binary or a cron job must be started by hand. If validation fails at boot, the staged file is deleted and the current database is kept.

**Offline, with the service stopped.** Use the same boot-time mechanism by placing the backup as the pending file:

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

The pending file is consumed (renamed or deleted), so keep your original backup. Restore the matching `encryption.key` first if this is a different host.

Practise restores in an isolated install with alert traffic blocked: restored channels and subscribers can message real people.

### 7.5 Migrations

Schema migrations run automatically at start and only move forward. A rollback to an older version works only if that version understands the database; otherwise restore the pre-upgrade backup.

---

## 8. Security checklist for production

1. **HTTPS and `APP_URL`.** Terminate TLS at the proxy and set `APP_URL` to the exact `https://` origin. This makes cookies Secure (the session cookie is also HttpOnly, and both cookies are SameSite=Lax). A Secure cookie is not sent over plain HTTP, so sign-in appears not to stick on an `http://` address with an `https://` `APP_URL`.
2. **Origin must equal `APP_URL`.** Setup and sign-in return 403 ("cross-site request rejected") when the browser origin differs in scheme, host or port. Loopback spellings (localhost, 127.0.0.1, `[::1]`) count as one only when `APP_URL` is itself loopback.
3. **Bind to loopback.** Compose binds `127.0.0.1:8085`; set `LISTEN_ADDR=127.0.0.1:<port>` for the plain binary. Docker-published ports are not blocked by host firewalls such as ufw (Docker adds its own rules), so the loopback bind is the control that matters. Only widen it behind an ALB and with a restrictive security group.
4. **Create the first administrator before exposing the address.** First-run setup works once, with no seeded credentials. Anyone who reaches an uninitialised install first can claim it. Do setup through an SSH tunnel or on loopback, then open the proxy.
5. **The proxy must be the only way in,** and must set `X-Real-IP` (nginx file does). Otherwise clients can pick their own rate-limit key.
6. **Firewall.** Allow 22, 80 and 443 only (section 3.6). Do not open 8080 or 8085.
7. **Accounts.** Use strong unique passwords (12 to 128 characters), give people the lowest role they need (viewer, user, admin), and turn on authenticator-app two-factor authentication, keeping the recovery codes outside Opskest. Sign-in is throttled to 15 requests per user name and 60 per source address per 10 minutes.
8. **Metrics.** Set `METRICS_TOKEN` to a long random value (`openssl rand -hex 32`) only if you scrape `/metrics`, and limit who can reach it at the proxy (section 4.2). Leave it empty otherwise: the endpoint then answers 404.
9. **Secrets and files.** Keep `.env` at mode 0600. Back up `encryption.key` separately (section 7.3). Opskest never logs secrets, payloads or channel configs.
10. **Updates.** Rebuild or update regularly (section 2.6 or 3.7). For the in-app updater, verify the release public key out of band and publish `manifest.json` last. Treat the update feed host as trusted infrastructure.
11. **Privacy defaults to review.** New public HTTP, ping and DNS monitors are worldwide by default, which sends their hostnames to Globalping until you turn it off in Settings > Check regions or choose "this server only" per monitor. Alerts include the target, reason and the start of the response body, so send them only to channels you trust.
12. **Backups off-host,** and a rehearsed restore.
13. **Status pages.** Public pages never expose targets, reasons, runbooks, tags or team notes; private pages answer 404 to signed-out visitors. If you serve a page on your own domain, forward only `/status/`, `/api/public/`, `/assets/`, `favicon` and `robots.txt` from that domain to Opskest, with the streams unbuffered. Sign-in still works only from `APP_URL`.

---

## 9. Scaling and performance notes

Opskest is a single node with SQLite and no clustering. All database writes go through one writer (20,000-item queue, commit every 1 s or 500 items).

**Live status page viewers.** Every status page viewer holds one long-lived Server-Sent Events connection (`/api/public/pages/<slug>/stream`) while the page is open and visible.

- A browser tab that has been hidden for 30 seconds closes its stream and reconnects when it becomes visible again, so background tabs do not hold connections indefinitely.
- Browsers allow at most 6 HTTP/1.1 connections per site. One open stream uses one of them, so several tabs of the same site (dashboard and status pages) can starve other requests on HTTP/1.1. **Enable HTTP/2 on the proxy** (section 4.2) so all requests and streams share one multiplexed connection per browser. Opskest itself speaks HTTP/1.1 only; the proxy talks HTTP/1.1 to it, using one upstream connection per open stream.
- Each viewer costs the proxy two connections (browser side and upstream side), so the proxy's connection limits are what bound the number of simultaneous viewers (for nginx, `worker_connections` and the open-file limit). The Opskest process does not set a connection cap of its own.
- Opskest builds each public page snapshot once per engine change and shares the same bytes with every viewer of that page. A viewer is sent data only when the page's snapshot actually changed; pings and unrelated monitors' changes send it nothing. The daily history for a page is cached for one minute per page, monitor set and window, and live status is read from memory on each request.
- Every connected client receives each global change notification through a small buffer (4 messages). A client that does not drain it is dropped, and the browser reconnects with backoff and gets a fresh snapshot.
- The public rate limit is per client address: 20 requests per 10 seconds sustained with a burst of 40, in memory, applied to the page, stream, history, incident, latency, feed, summary and badge endpoints of public pages (private pages are exempt). Subscribe requests have a stricter limit of 5 per 10 minutes. Because the key is the client address from `X-Real-IP`, visitors behind one NAT share a bucket, and a proxy that does not pass the real address makes everyone share the proxy's bucket (section 4.6).
- JSON for a status page can run to a few hundred KB uncompressed; Opskest gzips it for clients that accept it.

**Monitoring load.** `CHECK_CONCURRENCY` (default 256, maximum 2048) caps simultaneous checks. If checks are queueing or the lag metric `opskest_scheduler_lag_p99_seconds` rises, look at it first and at the CPU limit. After changing `internal/engine`, `internal/scheduler` or `internal/store/writer.go`, rerun the load harness (`Docs.md`, section 15).

**Disk.** Retention settings control growth: raw checks (1 to 90 days, default 7), notification log 30 days, audit log 365 days, hourly rollups 90 days, daily rollups 730 days. Settings > Storage shows what is held, clears older history and compacts the file.

---

## 10. Verification after deploy and troubleshooting

### 10.1 Verification

1. Health: Opskest answers `ok` on `/healthz` locally, and through the proxy.

```bash
curl -fsS http://127.0.0.1:8085/healthz; echo
curl -fsS https://status.example.com/healthz; echo
docker compose exec opskest /opskest healthcheck; echo "exit code: $?"     # Compose only
```

2. Sign in at `APP_URL` exactly (create the first administrator if this is a new install). The browser address must match `APP_URL`.
3. Live updates through the proxy. The signed-in feed `/api/stream` needs a session, so check it in the browser: open the developer tools, Network tab, and confirm the `stream` request stays open and the dashboard updates within a second or two of a change. For a status page, create a public page with one monitor, then:

```bash
curl -N -sS https://status.example.com/api/public/pages/<slug>/stream
```

You should immediately see `event: snapshot` and a `data:` line. Change a service's state (for example set a manual monitor down) and confirm a new `snapshot` arrives within about a second. If nothing arrives until you stop the command, or it arrives in bursts, the proxy is buffering.

4. Add a test monitor (for example an HTTP monitor for a known-good URL and one for a closed port) and confirm that up and down are detected.
5. Add an alert channel in Settings > Alert delivery and press **Test**. Then take a monitor down and bring it back and confirm both the Down and Recovered messages reach a real destination.
6. Take a backup (Settings > Backups) and confirm a file appears in the backup folder; rehearse a restore somewhere isolated.
7. Confirm a private page is 404 when signed out, and a public page shows no targets or reasons.

### 10.2 Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| Startup stops with `cannot write <dir>: on the host run  sudo chown -R 65532:65532 <dir>` | The data or backup folder is not writable by the process. Compose: `sudo chown -R 65532:65532 data backups`. Plain Linux: `sudo chown -R opskest:opskest /opt/opskest`; if you moved the folders, also add them to `ReadWritePaths`. |
| Setup or sign-in returns 403 "cross-site request rejected" | The browser address differs from `APP_URL` in scheme, host or port (for example `127.0.0.1` against `localhost`, `:8085` against `:8080`, `http` against `https`), or the request is cross-site. Open `APP_URL` exactly, or correct `APP_URL` and restart. Behind a proxy `APP_URL` must be the public `https://` origin. |
| Sign-in succeeds but the session is lost | `APP_URL` is `https://` (Secure cookies) but the browser uses plain HTTP. Use `https`, or an `http://` `APP_URL` for local HTTP only. |
| 429 "too many attempts" at sign-in | 15 attempts per user name or 60 per source address in 10 minutes. Behind a proxy all users share the proxy's address. Wait it out. |
| 429 on a public status page | The per-client public rate limit (20 per 10 s, burst 40). A proxy or CDN that does not pass the real client address (`X-Real-IP`) makes every visitor share one bucket (section 4.6). |
| Ping monitors fail with a permission error | Unprivileged ICMP needs `net.ipv4.ping_group_range` to include the process group. Compose sets it; on plain Linux see section 3.4 and verify with `sysctl net.ipv4.ping_group_range`. Ping is probably unusable on Windows (section 5.1). |
| Failing monitors show unknown, not down | The internet-loss guard thinks the server is offline because TCP 53 to `1.1.1.1`, `8.8.8.8` and `208.67.222.222` is blocked. Allow that outbound traffic. |
| Live updates stall or arrive in bursts | The proxy is buffering `/api/stream` or `/api/public/pages/<slug>/stream`. Turn off buffering for exactly those two paths and raise the read timeout (nginx: `proxy_buffering off`, `proxy_read_timeout 1h`). Check CDN settings too. |
| Status page streams keep reconnecting | Expected for a quiet public page behind a proxy or load balancer with a short idle timeout (an ALB defaults to 60 s), because that stream writes nothing until the snapshot changes. Raise the idle timeout (section 4.5) if it bothers you. |
| 502 or 504 from the proxy | 502: Opskest is not running or the proxy points at the wrong address. Check `curl -fsS http://127.0.0.1:8085/healthz`, `systemctl status opskest` or `docker compose ps`, and the `proxy_pass` port. 504: a stream or upload exceeded a proxy timeout; raise `proxy_read_timeout` for streams and `proxy_send_timeout` and `client_max_body_size` for restore uploads. 413: `client_max_body_size` is too small. |
| `listen :8080: bind: address already in use` (or the same for another address) | Another process holds the port. Find it with `sudo ss -ltnp \| grep -E ':(8080\|8085)'`. Plain binary: change `LISTEN_ADDR` or `PORT`. Compose: set `OPSKEST_BIND`, for example `127.0.0.1:8086`, and update the proxy. |
| `APP_URL: must be an absolute http(s) URL` | `APP_URL` lacks a scheme or host. |
| `SECRET_ENCRYPTION_KEY: must decode to 32 bytes` or an `encryption key:` error naming `encryption.key` | The key is not 64 hex characters, or the key file is corrupt. Opskest never replaces a corrupt file; restore the correct key from your backup. |
| Opskest exited after a restore, update or setting change | A restore (and an update in `exit` mode) makes Opskest exit with status 0 by design. The supervisor must restart it: Compose and the shipped unit do. Start it by hand otherwise. |
| `docker compose pull` finds no image | The image is built locally. Use `docker compose up -d --build`. |
| Update screen says it cannot install | The install is Docker or package-managed, the build is a development build (`dev`), no public key is set, or the binary's folder is not writable by the service user (`ReadWritePaths` must include it). |
| `/metrics` returns 404 or 401 | 404: `METRICS_TOKEN` is unset. 401: missing or wrong `Authorization: Bearer <token>`. |
| Healthcheck fails although the app works | `/opskest healthcheck` always probes `127.0.0.1` on the port from `LISTEN_ADDR` or `PORT`. If `LISTEN_ADDR` is a specific non-loopback address, it will not answer there. |
