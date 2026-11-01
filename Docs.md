# Opskest user guide

Opskest is a self-hosted uptime monitoring workspace for teams. It is one program (Go, SQLite, and a web interface built in) with no database server, web server or identity provider to set up. It checks your services on a schedule, records outages, alerts your channels and publishes status pages.

This guide covers installing it, what each part does, and what to check when something misbehaves. Where older notes and the code disagreed, this guide follows the code.

## Contents

1. [What Opskest is](#1-what-opskest-is)
2. [Install and run](#2-install-and-run)
3. [Roles and team](#3-roles-and-team)
4. [Monitors](#4-monitors)
5. [Third-party provider feeds](#5-third-party-provider-feeds)
6. [Alerts](#6-alerts)
7. [Outages and incidents](#7-outages-and-incidents)
8. [Maintenance windows](#8-maintenance-windows)
9. [Status pages and wallboards](#9-status-pages-and-wallboards)
10. [Worldwide checks](#10-worldwide-checks)
11. [Reports, SLA, percentiles and metrics](#11-reports-sla-percentiles-and-metrics)
12. [Settings and updates](#12-settings-and-updates)
13. [API and automation](#13-api-and-automation)
14. [Security and privacy](#14-security-and-privacy)
15. [Performance, limits and contributing](#15-performance-limits-and-contributing)
16. [Troubleshooting](#16-troubleshooting)

---

## 1. What Opskest is

- **Watches**: 24 monitor types (web, network, protocol handshakes, database queries, heartbeats, groups, manual) on your intervals, optionally also from other countries.
- **Tells people**: 24 alert channel types with templates, quiet hours, storm grouping and a delivery log.
- **Publishes**: public or private status pages, a wallboard for wall displays, subscriptions, feeds and SVG badges.

The sidebar has Overview, Monitors, Projects, Providers, Incidents, Maintenance, Status Pages (users and admins) and Settings (admins).

| Term | Meaning |
| --- | --- |
| **Project** | A named collection that organizes monitors (the API calls it a group). Organization only; no separate access rules. |
| **Monitor** | One thing being watched, with its schedule and alert settings. |
| **Check** | One run of a monitor. |
| **Outage** | The recorded period a monitor was down. Created and closed automatically. |
| **Incident** | A human-written report about a problem, with a timeline. Created manually only. |
| **Maintenance window** | A planned period in which chosen monitors do not alert. Checks continue. |
| **Status page** | A public or private page of selected services, incidents and maintenance. |
| **Wallboard** | A status page created as a board: a live grid for a wall display. |
| **Channel** | A destination for alerts (Slack, email, webhook...). |
| **Region** | A country from which a monitor is also checked, via Globalping. |
| **Third-party provider feed** | A monitor that follows another company's status page or RSS feed. Alert-only. |

---

## 2. Install and run

The full, step-by-step guide for every deployment type (Docker Compose, plain Linux with systemd, Windows, reverse proxy and TLS) is in [deploy/DEPLOYMENT.md](deploy/DEPLOYMENT.md). This section is the short version.

Every setting is optional. With none, Opskest listens on port 8080 and keeps data in `./data`, backups in `./backups` (relative to where you start it) and its own encryption key.

### Configuration

Settings come from environment variables or a `.env` file in the start folder (or the file named by `OPSKEST_ENV_FILE`). A real variable that is set (not empty) wins over the file. Copy `.env.example` to `.env` to change anything.

| Setting | Purpose and default |
| --- | --- |
| `APP_URL` | The address people open, with scheme. `https://` makes cookies Secure and is used in alert links. Setup and sign-in are refused (403) from any other origin. Default `http://localhost:<port>`. |
| `PORT`, `LISTEN_ADDR` | Where to listen; `LISTEN_ADDR` (e.g. `127.0.0.1:8080`) wins. Default `:8080`. |
| `DATA_DIR`, `BACKUP_DIR` | Database and backup folders. Defaults `./data`, `./backups`. |
| `SECRET_ENCRYPTION_KEY` | 64 hex characters (`openssl rand -hex 32`). Empty means a key is generated into `DATA_DIR/encryption.key`. |
| `DISPLAY_TZ` | IANA zone for charts, daily rollups, the backup hour and new maintenance windows. Falls back to `TZ`, the server zone, then UTC. |
| `SERVER_REGION` | Label for where checks run from. Default "This server". |
| `METRICS_TOKEN` | Bearer token for `GET /metrics`; empty disables it. |
| `CHECK_CONCURRENCY` | Concurrent checks, 1 to 2048. Default 256. |
| `LOG_LEVEL` | `debug`, `info` (default), `warn`, `error`. |
| `OPSKEST_UPDATE_RESTART` | `self` (default) or `exit`; see [Updates](#updates). |
| `OPSKEST_BIND`, `OPSKEST_DATA_DIR`, `BACKUP_HOST_DIR`, `VERSION` | Compose only: published host address (default `127.0.0.1:8085`), host folders mounted as `/data` and `/backups`, image tag and build version. |

Opskest serves plain HTTP on one address. It has no TLS, certificate management or ACME client; use a reverse proxy for HTTPS.

### The encryption key

Channel configurations, monitor passwords and tokens, and two-factor secrets are encrypted with one key. Without `SECRET_ENCRYPTION_KEY`, Opskest creates `DATA_DIR/encryption.key` (mode 0600) on first start and reuses it. A corrupt key file is an error and is never replaced, because a new key would make every stored secret unreadable. **Keep the key with your backups**; a database copy cannot be decrypted without it.

### Option 1: Docker Compose

```bash
git clone <your copy of this repository> opskest && cd opskest
mkdir -p data backups && sudo chown -R 65532:65532 data backups
docker compose up -d --build        # then open http://localhost:8085
```

- The container listens on 8080 and is published on `127.0.0.1:8085` only. The stack never binds ports 80 or 443.
- Compose defaults `APP_URL` to `http://localhost:8085`. For any other address, set `APP_URL` in `.env` before first start. Do not set `DATA_DIR`, `BACKUP_DIR`, `PORT` or `LISTEN_ADDR` there; the image sets them and `.env` would override them.
- `./data` and `./backups` are bind mounts owned by uid 65532 (the distroless image runs as that user and has no shell). If they are not writable, Opskest stops at boot and prints the exact `chown` command.
- Compose sets `net.ipv4.ping_group_range` so ping monitors work without `NET_RAW`. Keep that line.
- The healthcheck runs `/opskest healthcheck`, which requests `/healthz`. Limits: 1 CPU, 512 MB; logs capped at 3 files of 10 MB. Names: project and service `opskest`, container `opskest-app`, image `opskest:<VERSION>`.

### Option 2: the plain binary

```bash
scripts/build-release 1.0.0     # dist/opskest-<os>-<arch>: linux amd64/arm64, windows amd64, darwin arm64
./opskest serve                 # data/ and backups/ in the current folder
```

Commands: `serve` (default), `healthcheck`, `version`. On Linux use `deploy/opskest.service` (dedicated `opskest` user, `WorkingDirectory=/opt/opskest`, `Restart=always`). On Windows run `opskest.exe serve` from a scheduled task or a service wrapper; none is shipped. For ping on Linux, if you see permission errors: `sudo sysctl -w net.ipv4.ping_group_range="0 2147483647"` (persist in `/etc/sysctl.d/`).

### Option 3: cPanel

Untested. A VPS with root (WHM) works: run the binary under systemd and proxy a domain to it with `deploy/apache.htaccess` (needs `mod_proxy`, `mod_proxy_http`, `mod_rewrite`; streaming through it is untested). Shared hosting rarely works, because hosts stop long-running processes and limit ports and outbound connections.

### Behind a reverse proxy

Use `deploy/nginx.host.conf`: replace `opskest.example.com`, point DNS at the proxy, and get a certificate with your usual tool (e.g. `certbot --nginx`). The proxy owns ports 80 and 443; Opskest stays on `127.0.0.1:8085`.

- **Streaming.** `/api/stream` and `/api/public/pages/<slug>/stream` are long-lived Server-Sent Events flushed about once a second. Turn off proxy buffering for exactly these two paths and allow a long read timeout (the example uses `proxy_buffering off` and `proxy_read_timeout 1h`). On an AWS ALB, raise the idle timeout to at least 60 s and set `OPSKEST_BIND=0.0.0.0:8085` with a restrictive security group.
- **`APP_URL` is the exact public origin.** Opskest does not read `Host` or `X-Forwarded-Proto`; the scheme, host and port of `APP_URL` decide Secure cookies and the accepted sign-in origin. Loopback spellings count as one when `APP_URL` is loopback.
- **Client address.** Public pages, badges and subscriptions are rate-limited by `X-Real-IP`, else the first `X-Forwarded-For` value, else the socket address. A client reaching Opskest directly can choose that value, so expose Opskest only through the proxy. Sign-in throttling uses the socket address (the proxy's, behind one).
- **Sizes.** The example raises nginx's limit for restore uploads (up to 1 GiB); raise the general limit above 2 MiB to import larger files (up to 5 MiB).

### First run

Open `APP_URL` exactly. The first screen creates the first administrator. It works once (an atomic operation, so concurrent requests cannot create two), and there are no seeded credentials or login bypass. **Create the administrator before sharing the address**, then add teammates in Settings.

### Backups, restore, upgrade

- **Backups.** Settings > Backups sets the hour (0 to 23 in the display zone, default 2) and snapshots kept (1 to 60, default 7); you can also back up now, download, delete or upload. Files are `opskest-YYYYMMDD-HHMM.db`. The scheduler checks hourly from process start, so the minute varies. Do not copy a running `opskest.db` by hand; its write-ahead log may hold committed data.
- **Restore** validates and stages a file and makes Opskest exit. The swap happens at next start, after copying the old database to `pre-restore-YYYYMMDD-HHMM.db` in the backup folder. Something must restart the process: Compose and the supplied systemd unit do; a plain binary or cron job must be started by hand. Practise restores in an isolated install with alert traffic blocked, or restored channels may message real people.
- **Upgrade.** Back up, replace the binary or run `docker compose up -d --build`, restart. Migrations run automatically and only forward; to go back, restore the pre-upgrade backup.
- **Alongside an older deployment.** Opskest does not migrate one. Use a separate port (e.g. `OPSKEST_BIND=127.0.0.1:8086`), folders and key, and recreate monitors, channels and pages (import/export helps but is not a full migration). Switch alert routing deliberately to avoid gaps or duplicate pages.

---

## 3. Roles and team

Three roles, **admin > user > viewer**, plus `disabled` (kept, cannot sign in).

| Area | Viewer | User | Admin |
| --- | :---: | :---: | :---: |
| Dashboard, monitors, history, reports, events, outages, incidents | read | read | read |
| Acknowledge outages; own outage notes | yes | yes | yes (any note) |
| Maintenance windows | read | create, edit, delete | same |
| Private status pages (signed in) | view | view | view |
| Monitors: create, edit, delete, pause, test, bulk, manual status, dependencies | no | yes | yes |
| Projects, tags, templates, import and export | no | yes | yes |
| Declare and edit incidents and updates | no | yes | yes |
| Status page editor, publishing, subscribers | no | yes | yes |
| Alert channels: list (id, name, type, default) | yes | yes | yes |
| Alert channels: configuration, create, edit, delete, test | no | no | yes |
| Settings, regions, delivery log, audit log, storage | no | no | yes |
| Backups, restore, updates | no | no | yes |
| Team: add, change roles, disable, remove | no | no | yes |

- Signed out gets 401; a role below the minimum gets 403. Admin-only data is never returned to lower roles.
- The role is read on every request, so changes apply at once, and disabling ends the user's sessions.
- Nobody can change their own role. The last administrator cannot be demoted, disabled or deleted.
- Admins add teammates in Settings > Team access with a username (3 to 32 characters: letters, digits, `.`, `_`, `-`) and a password (12 to 128 characters). The default role is viewer.
- Everyone manages their own password and two-factor authentication under their account.

**Two-factor (optional TOTP).** Enrolling shows a secret to scan and 10 single-use recovery codes. Sign-in then asks for a current code or a recovery code. The secret is stored encrypted; store remaining recovery codes outside Opskest. Passwords use PBKDF2-HMAC-SHA256 (600,000 rounds). Sessions last 30 days, sliding. Sign-in is throttled to 15 requests per user name and 60 per source address per 10 minutes (every request counts, including the code step).

---

**Requiring two-factor authentication.** In Settings > Security an administrator can switch on "Require two-factor authentication". Everyone without an authenticator app is then sent to Account security and can use nothing else until they set one up; administrators can always still reach the settings API, so the policy can be switched off again. People cannot turn their own two-factor off while it is required. The team table shows who has two-factor on, and an administrator can reset another user's authenticator (a lost device); that user is asked to set it up again at their next sign-in. If the only administrator loses both their device and recovery codes, no one can reset them from the interface, so keep the recovery codes safe.

## 4. Monitors

A monitor has a name (1 to 120 characters), type, target, schedule and alert settings. The form validates as you type and has a **Test** button that runs the check without saving.

**Tags.** Tags label monitors so you can find and group them. Anyone who can edit monitors creates, renames, recolours and deletes tags from **Manage tags** in the monitor form (a tag you create there is added to that monitor); administrators also see them all in Settings > Tags, with how many monitors use each. A tag name is 1 to 40 characters and unique, and a colour is `#rrggbb`. Deleting a tag removes it from its monitors and changes nothing else.

**Check location.** New monitors check from this server only. Worldwide or a custom list of countries is an opt-in on HTTP, ping and DNS monitors, because those checks send the target to a third party (see section 10). Monitors created by importing a URL list or a file without settings also start on this server only.

### Types

| Group | Type (API id) | Target | Down when |
| --- | --- | --- | --- |
| Web | HTTP(S) (`http`) | URL | Connection, TLS or request fails; status not accepted; keyword, JSON check or assertion fails |
| Web | WebSocket (`websocket`) | `ws(s)://` URL | Connect/send fails, no reply, or reply lacks expected text |
| Web | SSL certificate (`tls`) | `host[:443]` | Handshake fails (expired, wrong name, untrusted) |
| Network | TCP port (`tcp`) | `host:port` | Cannot connect; TLS fails; expected reply missing |
| Network | Ping (`ping`) | host or IP | No reply (partial loss above the maximum is degraded) |
| Network | DNS (`dns`) | hostname | Lookup fails, empty, or no match with expected values |
| Network | UDP (`udp`) | `host:port` | No reply, or reply lacks expected bytes |
| Network | NTP (`ntp`) | `host[:123]` | No valid reply, stratum 0, or offset above your maximum |
| Services | SSH, SMTP, Redis, PostgreSQL, MySQL/MariaDB, MongoDB, MQTT, AMQP, Memcached, gRPC health, banner (`ssh`, `smtp`, `redis`, `postgres`, `mysql`, `mongodb`, `mqtt`, `amqp`, `memcached`, `grpc`, `banner`) | `host:port` | The server does not answer its protocol handshake (details below) |
| Databases | PostgreSQL queries, MySQL/MariaDB queries (`postgres_sql`, `mysql_sql`) | `host:port` | Login fails, probe cannot run, or probe condition met |
| Heartbeat | Push (`push`) | none (own URL) | No call within interval plus grace, or a call says down |
| Heartbeat | Manual (`manual`) | none | You set it down |
| Logic | Group (`group`) | none | Its rule over other monitors is not met |

**Service monitors** prove a server answers and never log in (Redis and MQTT authenticate only if you set a password). SSH expects a first line starting `SSH-`; SMTP reads the greeting and `EHLO`; Redis sends `PING`; PostgreSQL sends the SSL request; MySQL reads the greeting; MongoDB sends `isMaster`; MQTT does `CONNECT`; AMQP requires `Connection.Start`; Memcached sends `version`; gRPC calls the standard health check; banner requires your text in the first line. They never put a password or server reply in a failure reason (SMTP includes the first line of an error reply). A refused, reset or timed-out connection is always down.

### Scheduling and status

| Setting | Range | Form default |
| --- | --- | --- |
| Check interval | 10 to 86400 s | 60 |
| Retries | 0 to 10 | 2 |
| Retry interval | 10 to 86400 s | 20 |
| Timeout | 1000 ms to interval minus 500 ms | 10000 ms |
| Slow threshold (degraded) | off, 1 to 120000 ms | off |
| Certificate warning | off, 1 to 90 days | 14 |
| Certificate expiry alerts | up to 8 thresholds, 1 to 365 days | 14, 7, 3, 1 |
| Reminder | off, 1 to 1440 min | off |
| Description, runbook | 1000 characters each | empty |
| Parent monitors | up to 5 | none |

Through the API, an omitted retries value is 0 (interval, retry interval and timeout default as above).

- **Retries.** With retries 2, the first failure makes the monitor *pending* and the third consecutive failure makes it *down*; retry interval applies between. With 0, the first failure is down.
- **Degraded** is a success that is slow or whose certificate has expired or is within the warning period. It alerts only if "alert when degraded" is on.
- **Upside down** inverts the result.
- **Parent monitors.** While a parent is down or in maintenance, a child's outages are still recorded but its alerts are held.
- **Certificate expiry alerts** apply to every monitor that sees a TLS certificate. Each threshold alerts once per certificate; a renewed certificate restarts the ladder; a restart does not repeat alerts.
- **Response-time alert** (not for push) fires when a percentile (50, 90, 95, 99) over 5 to 60 minutes exceeds a limit in ms, a multiple (1.5 to 20) of the usual value, or either. The window cannot exceed the last 100 checks.
- **Internet-loss guard.** After a failure, Opskest tries TCP to `1.1.1.1:53`, `8.8.8.8:53` and `208.67.222.222:53` (2 s timeout, cached 30 s). If none answers, the server is treated as offline: the result is recorded as unknown, nothing alerts. It does not apply to push. A server that cannot reach those addresses will never mark monitors down.

### Type details

**HTTP.** Any common method (default GET); body up to 64 KiB; up to 30 headers (any can be secret, stored encrypted); auth none, basic, bearer or OAuth2 client credentials; accepted codes (default `200-299`); redirects 0 to 20 (default 10); ignore TLS errors; keyword check; JSON check; up to 20 assertions (status, response time, header, body, JSON path); outbound proxy; extra CA certificates; client certificate (mutual TLS); daily domain-expiry check via RDAP. Checks run in that order and stop at the first failure. **The start of the response body is shown in alerts**, so do not monitor a URL whose response is sensitive. Each check opens a new connection and sends `User-Agent: opskest/<version>`.

**Ping**: packets 1 to 10 (default 3) 500 ms apart, maximum loss percent (default 34). **DNS**: A, AAAA, CNAME, MX, TXT, NS; resolver blank or `ip[:port]`; up to 32 expected values with match `equals`, `contains`, `all` or `exact` (default `equals` for one value, `exact` for several); a mismatch reason lists the answers received. **TCP/UDP**: `send` and `expect` are text or `hex:` bytes (TCP `expect` can be a regex); replies are never put in reasons.

**Push.** The job calls `<APP_URL>/push/<token>` (GET or POST) with optional `status=up|down`, `msg=<reason>`, `ping=<ms>`. Down if no call arrives within the interval plus grace (0 to 3600 s, default 30). The token is the only authentication; keep the URL private. Unknown token: 404. Limit one call per second per token (extra calls get 200 and are ignored); calls to a paused monitor are ignored.

**Group.** Follows 1 to 100 other monitors by rule `all` (default), `any`, or `min` (at least N up). Paused, maintenance and unchecked children are left out; pending children count as up. Groups may nest, not recurse. It runs on its own interval (10 to 30 s for fast), with its own retries (usually 0), no timeout or upside-down mode, and children still alert separately. Import cannot carry groups.

**Manual.** Set up, degraded or down from the monitor page or `POST /api/monitors/<id>/status` with `{"status": "...", "reason": "..."}`. It takes effect at once, repeats every interval so uptime accrues, survives restarts, alerts normally, and cannot be set while paused.

### Database monitors

These are the only checks that log in. Use a dedicated least-privilege account. Every check opens one connection, runs one **read-only transaction** (PostgreSQL also sets `default_transaction_read_only = on`), and closes it. TLS: with TLS on the certificate and host are verified unless "ignore TLS errors" is on. The password is stored encrypted.

| Probe | Down when |
| --- | --- |
| `query` (your SQL) | The query fails, or a comparison on the first column of the first row fails (or no rows). With no comparison it only has to run. |
| `deadlocks` | New deadlocks since the previous check exceed the tolerated count (default 0). |
| `long_running` | More than the tolerated count (default 0) of queries run longer than 1 to 86400 s (default 30). |
| `blocked` | More than the tolerated count (default 0) of sessions wait on a lock. |
| `connections` | Percent of the connection limit in use exceeds 1 to 100 (default 80). |

A custom query must start with `SELECT`, `WITH`, `SHOW`, `EXPLAIN`, `VALUES` or `TABLE`, be one statement and at most 4000 characters (re-checked at run time). Comparisons: `eq`, `ne`, `gt`, `gte`, `lt`, `lte`, `contains`. The deadlock baseline lives in memory, so the first check after a start cannot alert. To see other sessions, grant `pg_monitor` (PostgreSQL) or `PROCESS` (MySQL/MariaDB). Reasons carry counts and durations, never query text or rows; a database's own error message (code and first line) is shown and could quote values.

### Organizing monitors

- **Projects** group monitors (each has a dashboard); **tags** label them. Tags never appear on public pages.
- **Bulk actions** (1 to 500 monitors): pause, resume, delete, move to a project, set retries, add or remove a channel. The whole selection is validated first.
- **Import and export** (Settings; users and admins). Export JSON or CSV with project, tag and channel names and no secrets. Import takes a JSON array, an export file, CSV (a template is offered) or a plain URL list, up to 5000 rows and 5 MiB, with a dry-run preview of create, skip or error per row. A duplicate is the same name, type and target.
- **Templates.** None are built in. Save the current form as a template (name 1 to 80 characters, unique ignoring case) and apply it to new monitors. It keeps the type, schedule, alert settings, channels, tags, runbook, type settings and non-secret headers; the server removes the name, target, project, secrets, push token and parents. Non-secret settings are kept as typed, including user names, database names and **query text**, so mark any token header as secret first. Users and admins manage templates.

---

## 5. Third-party provider feeds

**Providers** (`/third-party`) follows other companies' published incidents. Browse a catalog of 99 providers in nine categories, or connect any Statuspage.io, Meta, Google, Slack or RSS/Atom feed URL. A feed is an HTTP monitor with the `vendor` setting, placed in the "3rd Party Providers" project. Users and admins can connect them.

A feed is **alert-only**:

- It is down or degraded only when the vendor publishes an incident. A feed that cannot be fetched or understood never alerts.
- It sends Down, Reminder, Recovered and (if enabled) Degraded alerts to its channels, labelled as a provider incident. Nothing to acknowledge; Down is sent once per episode.
- It is never an outage, uptime, SLA, incident link, status page component, dependency, group child or maintenance target (the API answers 400), and is excluded from outage lists, counts, clusters, region checks, reports, exports, the events feed and `/metrics`.

The URL form decides how it is read: RSS/Atom (newest entry decides), `metastatus.com`, Google status JSON, `status.slack.com`, or Atlassian Statuspage `/api/v2/summary.json` (including `status.*` hosts). A plain HTML scan cannot read incidents and is unsupported; a vendor-hosted domain that is not `status.*` is usually readable only through `/history.rss`. The catalog was verified once from one machine; vendors move pages, so use **Check now** on a feed to confirm it reads.

---

## 6. Alerts

Admins create channels under Settings > Alert delivery; each monitor chooses its channels. Mark a channel as a default for new monitors. **Apply to all** adds a channel to every monitor lacking it (paused included); the monitor list adds or removes a channel for up to 500 selected monitors.

A channel's configuration is stored encrypted. The API reports only whether a URL, `apiKey`, `routingKey`, `password`, `hmacSecret` or `headers` is set; other fields are returned as saved, to admins only.

### Channel types

Use **Test** after creating a channel; success only means the service accepted the request. Required fields (JSON names):

| Channel (type id) | Required | Optional |
| --- | --- | --- |
| Slack (`slack`), Teams (`teams`), Google Chat (`gchat`), Discord (`discord`) | `url` (`https` webhook) | none |
| Mattermost (`mattermost`), Rocket.Chat (`rocketchat`) | `url` (`http(s)` webhook) | none |
| Telegram (`telegram`) | `apiKey` (bot token), `target` (chat id) | `url` (self-hosted Bot API) |
| ntfy (`ntfy`) | `url` including the topic | `apiKey`, or `username` + `password` |
| Gotify (`gotify`) | `url`, `apiKey` | none |
| Pushover (`pushover`) | `apiKey`, `target` (user/group key) | `url` |
| Matrix (`matrix`) | `url` (homeserver), `apiKey`, `target` (room id) | none |
| Email (`email`) | `host`, `from`, `to` (list) | `port` (set it; form starts at 587), `tlsMode` (`starttls`, `tls`, `none`), `username`, `password` |
| Webhook (`webhook`) | `url` (`https`) | `headers`, `hmacSecret` |
| PagerDuty (`pagerduty`) | `routingKey` (Events v2) | none |
| Opsgenie (`opsgenie`) | `apiKey` | `region` (`us`, `eu`) |
| Zulip (`zulip`) | `url`, `username`, `apiKey`, `target` (stream) | `topic` |
| Webex (`webex`) | `apiKey`, `target` (room id) | `url` |
| Pushbullet (`pushbullet`) | `apiKey` | `target`, `url` |
| IFTTT (`ifttt`) | `apiKey`, `target` (event) | `url` |
| Apprise API (`apprise`) | `url`, `target` (config key) | `username`, `password` |
| Twilio SMS (`twilio`) | `username` (SID), `password`, `from`, `to` (1 to 10) | `url` |
| Home Assistant (`homeassistant`) | `url`, `apiKey`, `target` (notify service) | none |
| Bark (`bark`) | `apiKey` | `url` |
| Signal (`signal`) | `url` (signal-cli REST), `from`, `to` (1 to 10) | `username`, `password` |

- **Email** uses Opskest's own SMTP client (also for subscriber mail). With a `username` it uses `PLAIN`, which Go refuses over an unencrypted remote connection, so use `starttls` or `tls`.
- **PagerDuty and Opsgenie** get one alert per monitor (`opskest-<monitor id>`), triggered on down and closed on recovery; they never get grouped messages or informational events.
- **Apprise API** reaches services not listed: save their URLs under a key in Apprise and point the channel at it.

### What alerts

Down and Recovered (always); Degraded and its all-clear (if enabled on the monitor); Reminder while down (per-monitor interval; acknowledging the outage stops it); certificate and domain expiring; other-country failures and response-time alerts; Test. Maintenance suppresses alerts, though a recovery for an already-alerted outage is still sent. Opskest has no escalation chains; use PagerDuty or Opsgenie for on-call. An alert shows the target, project, reason, HTTP status and start of the response body, duration, response time, monitor id and runbook, so send alerts only to channels you trust.

### Templates and quiet hours

A channel `template` (up to 1000 characters) replaces the details of a single-monitor alert. Placeholders: `{name}`, `{status}`, `{reason}`, `{duration}`, `{url}`, `{time}`. An unknown `{name}` is rejected on save. A webhook receives the result in `message`; PagerDuty ignores it; grouped messages are not templated.

**Quiet hours** silence a channel in a recurring window (days, start and end on the display-zone wall clock; days name the day the window starts, and a window ending before it starts runs past midnight). Alerts in the window are dropped for that channel, not queued. "Bypass critical" still lets down alerts, their reminders and the recovery through. Test and Resend ignore quiet hours.

### Webhook payload and signature

Webhooks `POST` JSON: `event` (`down`, `recovered`, `degraded`, `degraded_cleared`, `reminder`, `cert_expiring`, `domain_expiring`, `regional`, `regional_cleared`, `latency_high`, `latency_cleared`, `test`), `monitor` (`id`, `name`, `group`, `target`, `runbook`), `status`, `reason`, `since` (Unix ms), `duration_s`, `latency_ms`, `code`, `response`, `url`, `message`, and `grouped` with `monitors` for grouped messages. With `hmacSecret` set, Opskest adds

```
X-Opskest-Signature: sha256=<hex digest>
```

the HMAC-SHA256 of the exact request body. Verify on the raw body, in constant time. Responses outside 200 to 299 count as failed.

### Delivery and grouping

- Each channel has its own queue (1000) and worker; the intake queue holds 10,000. Overflow is dropped and counted.
- Each attempt has a 10 s timeout; failures retry after 5 s, 30 s and 2 min (four attempts). Every attempt is logged in Settings > Delivery history (kept 30 days) with a redacted error; failed entries can be resent. A test is sent once.
- **Storm grouping.** Down events are held 3 s. If at least the storm threshold of monitors (default 5) go down within the storm window (default 30 s), one grouped message replaces individual ones; recoveries likewise. Only Slack, email, Teams, Google Chat and webhook channels group; all others, PagerDuty and Opsgenie included, get one message per monitor.

---

## 7. Outages and incidents

**Outages are automatic.** When a monitor goes down (after its retries) the engine opens an outage; recovery closes it. Outages drive uptime, SLA and alerts, and you cannot create one by hand.

**Incidents are created manually only.** Nothing opens one for you. A user or admin declares an incident to communicate about a problem, typically on a status page. It has a title, impact (minor, major, maintenance), severity (critical, major, minor), optional commander, linked monitors and pages, a status (investigating, identified, monitoring, resolved), a timeline and an optional postmortem. Third-party feeds cannot be linked.

**Working an outage** (Incidents > Outages, or a monitor's Events tab):

- **Acknowledge** (any role) stops that outage's reminders and records who did it.
- **Notes.** Each outage has a thread of up to 200 notes (1 to 2000 characters). Everyone signed in can read and add; the author edits or deletes their own, an admin any. Notes are team-only and never public. List search matches any note; "Needs note" finds empty threads. Audit entries omit note text.

**Working an incident:**

- **Declare**, then **post updates** with a status and text. Public updates notify the linked pages' subscribers; an update marked internal is a team note, hidden from public pages and not sent.
- **Edit** an update's text or team-note flag in place (users and admins). It keeps its status and time, shows "(edited)", does not change the incident status and does not re-notify; the public page shows the corrected text, and an entry turned into a team note leaves the page. Title, impact, severity and owner are changed with **Edit** on the incident.
- **Resolve** with an update of status resolved, then add a **postmortem**.

---

## 8. Maintenance windows

Users and admins create windows under Maintenance. Alerts are silenced while checks keep running.

- **Kinds.** *One-off*: start and end, at most 30 days. *Weekly*: weekdays, a local start time and a duration of 1 to 1440 minutes.
- **Time zone.** Each window has an IANA zone (default: the display zone). Weekly windows follow that zone's wall clock through daylight saving; a start time that does not exist on a spring-forward day has no occurrence that day, and a repeated fall-back hour runs once.
- **Targets.** At least one monitor or project (a project also covers monitors added later). Third-party feeds cannot be targets. A window can be disabled without deleting it.
- **Effect.** Windows are re-evaluated every 15 s and on each edit. Covered monitors do not alert (a recovery for an already-alerted outage is still sent), outages do not open during maintenance so SLA is unaffected, and children of a monitor in maintenance have their alerts held. Public pages show windows in progress and those starting within seven days, only if they target a service on that page, never their targets or author (a page can turn this off).

---

## 9. Status pages and wallboards

**Status Pages** (users and admins) is a searchable directory (filters Public, Private, Board, List). Creating one is a short flow: type, title, address (slug), description; services; then public or private.

**Type is fixed.** A page is a *status page* (list) or a *wallboard* (board) from creation, never both; the API refuses a change (400). A board's `/status/<slug>` moves to `/status/<slug>/board`; a list page's `/board` is a 404.

**Editor sections:** Basics; Services (ordered services and project groups, display names, public descriptions); Appearance; Content; Updates and subscribers; Access. A sticky bar holds Save and Discard and warns before you leave unsaved. **View saved page** shows the last saved version, not a draft.

- **Appearance.** Logo, accent color, website and support links (`https` only), and closed theme options: header (plain, tinted, solid), font (from fonts already on the visitor's device), corners, and default appearance for new visitors. There is no custom CSS, on purpose: it could hide an outage or load from another site. Status colors never change with branding.
- **Content.** Description (2000 characters), footer (600) and a pinned announcement (300, one line; four styles) in a small Markdown subset. Text is rendered as elements, never HTML, and the API rejects raw tags and unsafe links.
- **Access.** Public or private; history 30, 60 or 90 days; optional uptime percentages (on by default), last-check time, certificate days and response times (off); auto-refresh (30 s to 5 min); public address; show scheduled maintenance (on by default).

**Visibility.** A *private* page answers 404 to signed-out visitors and shows signed-in users everything, including reasons and response times. A *public* page shows display names, statuses, uptime and incident text, and omits targets, failure reasons, runbooks, tags and team notes. Public endpoints are rate-limited per client (20 requests per 10 s, burst 40).

**What visitors see.** One column: an overall-status banner, active incidents and maintenance, services in the owner's groups with a daily bar per day and the status in words, optional response times, and 14 days of incidents with a history page and month calendar. Expandable parts start collapsed (owners can open all on load) and deep links like `#incident-12` open them. Dates use the visitor's or the server's zone, named on the page.

**Subscribe, feeds, badges.**

- **Subscribe** offers email (double opt-in, only if the owner chose an email channel), RSS and Atom feeds, chat-tool instructions, and visitor-supplied Slack webhooks (stored sealed). Subscribe always returns the same neutral reply, the confirm link lasts 48 hours, every email has an unsubscribe link that acts on click (not on open), and subscribing is limited to 5 attempts per 10 minutes per client.
- **Feeds** under `/api/public/pages/<slug>/`: `feed.atom` (Atom 1.0, 50 newest), `feed.rss`, and `summary.json` (shaped like Atlassian Statuspage's summary). Owners can switch the feeds off (then 404). Responses are cached about a minute with an `ETag`; private pages are never cacheable.
- **Badges** (SVG, public pages only, cached 60 s): `badge.svg` (page), `services/<id>/badge.svg` (service) and `services/<id>/uptime.svg?window=24h|7d|30d|90d` (default 30d; truncated, never rounded up to 100%). Badges show only a status word or percentage and the public display name; any unknown or private target returns the identical "not found" image. The editor's **Embed** box builds Markdown and HTML snippets.

**Your own domain.** The page's *public address* (an `https` origin) sets canonical, feed and embed URLs; it creates no DNS, certificate or proxy rule. Point DNS at your proxy, let it obtain the certificate, forward only `/status/`, `/api/public/`, `/assets/`, `favicon` and `robots.txt` to `127.0.0.1:8085` (stream path unbuffered), and enter the address. Sign-in still works only from `APP_URL`.

**Wallboard.** A board page at `/status/<slug>/board` is a live grid for a TV: tiles per service or per project, fit-all or 8, 12 or 16 per screen rotating every 10 s (problems stay on every screen), and an optional alarm sound when a service goes down or degrades (choose sounds or mute; browsers need one click before allowing sound). Add `?kiosk=1` to hide the toolbar and cursor.

**Search engines.** Only public status pages are indexable; everything else (app, API, feeds, badges, private pages, wallboard) sends `X-Robots-Tag: noindex, nofollow, noarchive`.

---

## 10. Worldwide checks

Your server checks every monitor from where it runs. Opskest can also check **public HTTP, ping and DNS monitors from other countries** through the free Globalping network. Admins configure it in Settings > Check regions; each monitor chooses *worldwide*, *this server only* or a custom country list.

- **Rotation.** Eligible monitors are checked from the configured countries in turn, paced to an hourly budget (default 225 tests per hour; Globalping allows 250 free, 500 with a token you can save in Settings). One country failing while others succeed is a regional issue: re-checked once, recorded, and alerted ("failing from China, Russia") if region alerts are on.
- **Outage confirmation** (off by default) also checks a new outage from the monitor's countries and records the verdict.
- **Defaults.** The rotation and region alerts are on by default, and new HTTP, ping and DNS monitors start as worldwide. **A new install with public HTTP, ping or DNS monitors therefore contacts Globalping until you turn the rotation off in Settings or choose "this server only" per monitor.**
- **What leaves your server.** Only scheme, hostname and path (HTTP) or hostname (ping, DNS); never query strings, credentials, headers or bodies. Never sent: monitors with custom headers, authentication, a body, a method other than GET/HEAD, a proxy, custom CA or client certificate, and private or internal names. The private-name test is by name only (private IPs, names without a dot, suffixes like `.local`, `.internal`), with no DNS lookup, so a public-looking name pointing at a private address is still sent.

Other connections Opskest makes itself: the internet-loss guard (TCP 53 to three DNS servers, only after a failure), domain-expiry lookups (`data.iana.org` and registry RDAP, only for HTTP monitors with that check on) and your update feed (only once a URL is set).

---

## 11. Reports, SLA, percentiles and metrics

- **Uptime** comes from rollups: hourly (kept 90 days) and daily (kept 730 days), so history outlives raw checks.
- **Percentiles** (p50, p95, p99) come from latency histograms stored with each rollup; missing or corrupt data shows "no data", never 0.
- **Monthly SLA** (the SLA card on a monitor or project page, with CSV; `GET /api/reports/sla`). Availability is time-based: outage minutes against the minutes the monitor existed that month, with maintenance excluded. It also reports check success percent, downtime, outage count, longest outage, MTTR, percentiles and the error budget against a target (default 99.9%), for any of the last 24 months. Scope: monitor, project, status page or all. Third-party feeds have no SLA.
- **Prometheus.** Set `METRICS_TOKEN` and scrape `/metrics` with `Authorization: Bearer <token>` (404 if unset, 401 if wrong). Series: `opskest_monitor_up` (1 up or degraded, 0 down, -1 otherwise), `opskest_monitor_latency_ms`, `opskest_monitor_uptime_24h`, `opskest_monitor_cert_days` (labels `id`, `name`, `group`, `type`), `opskest_scheduler_lag_p99_seconds`, `opskest_writer_dropped_total`, `opskest_checks_inflight`. Third-party feeds are not exported.
- **Retention.** Raw checks: the days set in Settings > Monitoring rules (default 7; the server accepts 1 to 90, though the form hints 7 to 365, so use 90 or less). Notification log 30 days, audit log 365 days. Settings > Storage shows what is held, clears history older than a period you pick (ongoing outages are kept) and compacts the file.

---

## 12. Settings and updates

Settings is admin-only.

| Tab | Holds |
| --- | --- |
| Alert delivery | Channels: add, edit, test, default, apply to all |
| Monitoring rules | Raw retention, a reminder-interval field (0 to 1440), storm threshold (2 to 50) and window (10 to 600 s) |
| Team access | Accounts, roles, recent activity (audit) |
| Check regions | Rotation, region alerts, confirmation, countries (1 to 25), hourly budget, Globalping token, ad-hoc check |
| Import and export | JSON/CSV export; import with preview |
| Backups | Schedule, snapshots, download, upload, restore |
| Storage | Database size, clear history, compact |
| Delivery history | Notification attempts; resend failed |
| About and updates | Version, install method, engine figures, metrics path, update controls |

### Updates

Plain-binary installs (Linux, Windows, macOS) can check a release feed **you** publish, show the new version and notes, and update themselves with backup, download, verification, swap, restart and one-click rollback. There is no central server, and nothing is contacted until a feed URL is set. Docker installs are only notified: a container never replaces itself, so rebuild or pull and run `docker compose up -d`, and roll back by running the previous image tag.

| Option | Meaning |
| --- | --- |
| Check for updates | Master switch; off means nothing is fetched, even by Check now. |
| Release feed | HTTPS URL of `manifest.json` (plain `http://` only for `localhost`, `127.0.0.1`, `::1`). |
| Mode `off` / `notify` (default) / `auto` | Notify checks about every 24 h (first check two minutes after start) and shows a banner; installing is a click on **Update now**. Auto also installs a verified newer version during the hour you choose (default 03:00, display zone), at most once a day, never within 24 h of a failure, never a version you rolled back from. |
| Public key | Write-only (the UI shows a fingerprint); a saved key beats one built into the binary. Without a key you are notified but cannot install. |
| Check now / Update now / Roll back to X | Fetch (once per 10 s); backup, download, verify, install, restart; swap back to the kept `<exe>.prev`. |

**Verification.** The manifest needs a valid ed25519 signature (`<feed URL>.sig`). The download must match the manifest's size and SHA-256, start with the OS's executable magic bytes, and print exactly the manifest's version when run as `<new> version`. Everything is fetched over HTTPS with timeouts and size limits, and any failure before the swap changes nothing. Pre-releases go only to installs already on one.

**Restart and rollback.** The old binary is kept as `<exe>.prev`. On Linux and macOS the new binary replaces the process in place; on Windows a detached copy starts. Under a supervisor (a service wrapper, systemd) set `OPSKEST_UPDATE_RESTART=exit` so Opskest swaps files and exits with status 0 for the supervisor to restart (systemd needs `Restart=always`). If the new binary cannot start, the old one is restored. Migrations only move forward, so if a rolled-back version refuses the database, restore the backup named in the update. By hand, with Opskest stopped: `mv opskest.prev opskest` (Windows: delete `opskest.exe`, rename `opskest.exe.prev`).

**Publishing a release.** Official releases are built and signed by GitHub Actions (`.github/workflows/release.yml`): push a tag such as `v1.4.0` and it tests, builds the four binaries, signs `manifest.json` with the key stored as a repository secret, publishes the GitHub release and records build provenance. The maintainer's one-time setup is in `.github/CONTRIBUTING.md`. Official builds already know their feed address (`releases/latest/download/manifest.json` of the repository) and public key, so an install needs no setup; a feed or key saved in Settings still wins.

To publish by hand instead, for example for your own fork or an internal feed:

```bash
go run ./cmd/releasetool keygen      # once: release.key (private, never commit) and release.pub
OPSKEST_UPDATE_FEED_URL=https://example.com/opskest/manifest.json scripts/release 1.4.0 https://example.com/opskest/1.4.0 notes.md
```

`scripts/release` builds the four binaries, writes and signs `dist/manifest.json`, verifies it and embeds the public key and feed address. Upload binaries first and `manifest.json` plus `.sig` last (the manifest makes a release visible). To rotate keys, give every install the new key in Settings before you sign with it.

---

## 13. API and automation

The web app uses a JSON API under `/api/`. It is not a versioned public contract, but it is scriptable.

- **Sessions.** `POST /api/auth/login` (local username and password; a second request supplies a TOTP or recovery code) sets the HttpOnly cookie `opskest.sid` and a readable cookie `opskest.csrf`. Copy the latter into an `X-CSRF-Token` header on every non-GET request. Setup and sign-in also require an `Origin` matching `APP_URL`. There are no API keys or personal access tokens.
- **No session needed:** `GET /healthz`, `GET /api/runtime`, `GET|POST /push/<token>`, `GET /metrics` (bearer), and the public status endpoints under `/api/public/`.
- **Session routes** (roles as in [Roles](#3-roles-and-team)): `monitors` (CRUD plus `/check`, `/status`, `/pause`, `/resume`, `/duplicate`, `/test`, `/bulk`, history and diagnostics), `groups` (projects), `tags`, `templates`, `channels`, `alert-log`, `outages` (`/ack`, `/notes`), `incidents`, `maintenance`, `pages`, `reports/sla`, `events`, `export/monitors`, `import/monitors`, and admin routes `users`, `settings`, `regions`, `data/*`, `audit-log`, `backups`, `updates`, `system`. `GET /api/stream` is the live SSE feed. Lists default to 50 rows and cap at 200.

```bash
curl -fsS "https://opskest.example.com/push/<token>?status=up&msg=ok&ping=120"   # heartbeat
```

`POST /api/monitors/bulk` takes `{"ids": [...], "action": "pause|resume|delete|move|retries|add_channel|remove_channel", ...}` (1 to 500 ids). `POST /api/import/monitors` takes a JSON array, an export file, `text/csv`, or `{"urls": [...], "groupName": "", "type": "http", "intervalS": 60}`; add `?dryRun=true` to preview.

---

## 14. Security and privacy

- **Authentication.** Local accounts only: no external identity provider, seeded credentials or bypass. Sign-in is throttled and writes are CSRF-protected.
- **Secrets at rest.** Channel configs, monitor passwords and tokens, secret headers, client keys, proxy and OAuth secrets, visitor Slack webhooks and TOTP secrets are encrypted with the [encryption key](#the-encryption-key). They are write-only in the UI and API; templates and exports strip them.
- **Never logged.** Logs hold only an error string trimmed to 300 bytes: no monitor settings, headers, payloads, channel configs, subscriber addresses or webhook URLs. Notification-log errors are redacted.
- **Public pages never leak.** Pages, feeds, the JSON summary and badges use dedicated public representations that omit targets, reasons, runbooks, tags and team notes. Automated tests search every public endpoint, including the first live-stream snapshot, for a deliberately sensitive target and reason.
- **404, never 403, for private or unknown things:** a private page without a session, an unknown push token, monitor id or badge target. A 403 would confirm existence.
- **What leaves your server:** alerts to your channels (target, reason, start of response body); checks to your targets; and, only when used, hostnames to Globalping, domain names to IANA/RDAP, and requests to your update feed.

---

## 15. Performance, limits and contributing

### Performance

The target is 2,000 monitors at a 30 s interval with scheduler lag p99 under 1 s, RSS under 150 MB and under 0.5 vCPU. `tests/loadtest` runs the real scheduler, engine, writer and HTTP checker against a local mock server. The latest recorded run (Run 9: Docker on Windows, 2 CPUs and 1 GB, 3 minutes) measured lag p99 1.38 ms, RSS 61.4 MB, CPU 0.100 cores and no skipped runs. Earlier five-minute runs (2026-09-25 to 2026-10-12, Apple M5 host, same container limits) peaked at 54.9 to 67.7 MB and 0.075 to 0.093 cores; cold boot of 2,000 monitors took 455 to 579 ms (target 3 s); the writer dropped nothing. These are point-in-time numbers on those hosts, not guarantees for yours; rerun before relying on them:

```bash
docker run --rm -v "$PWD":/src -w /src -v opskest-gomod:/go/pkg/mod -v opskest-gocache:/root/.cache/go-build \
  --cpus 2 --memory 1g golang:1.27.3-bookworm go run ./tests/loadtest -monitors 2000 -interval 30s -duration 5m
```

Other limits: lists default to 50 rows and cap at 200; bulk requests take 500 monitors; imports 5000 rows or 5 MiB; restore uploads 1 GiB. All database writes go through one writer (20,000-item queue, commit every 1 s or 500 items).

### For contributors

```bash
npm --prefix web ci
npm --prefix web run build            # tsc + vite; output in internal/webui/dist, which Go embeds
npm --prefix web run test:theme
go vet ./... && go test ./... -count=1
CGO_ENABLED=1 go test -race ./internal/api/ ./internal/hub/ ./internal/app/
go build -o bin/opskest ./cmd/opskest
(cd web && node --experimental-strip-types --test tests/*.test.mjs)   # other Node unit tests
(cd web && npm run test:e2e)          # about 12 to 15 minutes
```

`scripts/go` and `scripts/web` run the same tools in Docker. Go is 1.27 (module `opskest`); the image builds the web app with Node 22 and the browser tests need Node 24. Builds use `CGO_ENABLED=0`; set `1` only for `-race`. Dependency allowlists are closed (see `CLAUDE.md`).

The e2e suite drives headless Chrome or Edge against a freshly built server with temporary data on loopback ports (it skips without a browser; set `CHROME_PATH` or `OPSKEST_E2E_BIN`). It does not cover real alert delivery, third-party pages, Docker or proxy deployments, real SQL servers, email subscriptions or two-factor enrollment. Optional integration tests run only with `OPSKEST_IT_PG`, `OPSKEST_IT_MYSQL`, `OPSKEST_IT_PG_TLS` (`host:port,user,password,database`) or `GLOBALPING_LIVE`; use throwaway servers.

**Checking a new install.** `bin/opskest healthcheck` exits 0 when `/healthz` answers (the process is up, not that alerts work). Then: create the admin; trigger a controlled failure and recovery and confirm both messages reach a real destination; test each channel; view a public page signed out and search its JSON for any target, reason or runbook; confirm a private page is 404; confirm live updates arrive about once a second through your proxy; take a backup and rehearse a restore in isolation.

**Rules that bite.** Route all writes through the single SQLite writer, including rollups. Register every route with a minimum role and add it to the route-matrix test. New public fields go through the public representations and leak tests. Never log secrets. Keep third-party feeds out of every list, count and export. All shared names are `opskest`-prefixed; never add ports 80/443 or an ACME client to the stack.

**Demo data.** `tests/demo-seed` can write two opt-in datasets into a local installation: a loopback-only walkthrough (with `tests/demo-fixture`) and a "live" set that monitors real public endpoints with synthetic history. Nothing is seeded at startup and there is no demo mode. Use them only on local test installs.

---

## 16. Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| Setup or sign-in answers **403 "cross-site request rejected"** | The browser address differs from `APP_URL` in scheme, host or port (e.g. `127.0.0.1` vs `localhost`, `:8085` vs `:8080`). Open `APP_URL` exactly, or fix it and restart. |
| **Sign-in does not persist** | An `https://` `APP_URL` makes cookies Secure, which browsers do not send over plain HTTP. Use an `http://` `APP_URL` for local HTTP, and match the browser origin. |
| **429 "too many attempts"** | Throttling (15 per user name, 60 per source address, per 10 minutes). Behind a proxy all users share the proxy's address. Wait it out. |
| **Live updates stall** or arrive in bursts | The proxy is buffering `/api/stream` or `/api/public/pages/<slug>/stream`. Disable buffering and raise the read timeout. |
| Startup **cannot write data or backups** | Bind mounts must be owned by uid 65532: `sudo chown -R 65532:65532 data backups` (or be writable by the binary's user). |
| **Ping monitors fail with permission errors** | Unprivileged ICMP needs `net.ipv4.ping_group_range` to include the process group. Compose sets it; on plain Linux set the sysctl. |
| Failing monitors show **unknown**, not down | The internet-loss guard thinks the server is offline. Allow outbound TCP 53 to `1.1.1.1`, `8.8.8.8`, `208.67.222.222`. |
| **`/metrics` 404 or 401** | 404: `METRICS_TOKEN` unset. 401: missing or wrong bearer header. |
| Opskest exited after a restore | By design; start it again (Compose and the supplied systemd unit do so automatically). |
| A page is 404 | Private pages 404 when signed out; list pages have no `/board`; check the slug. |
| Alerts missing overnight | Channel quiet hours drop alerts (not queued); Test ignores them. |
| A monitor never alerts | Check for a maintenance window, a down parent, pending retries, a third-party feed with no incident, or no channel assigned. |
| `docker compose pull` finds no image | Compose builds `opskest:<VERSION>` locally; use `docker compose up -d --build`, or publish your own image. |
