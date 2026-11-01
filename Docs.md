# Opskest user guide

Opskest is a self-hosted uptime monitoring workspace for teams. It is one program (Go, SQLite and a built-in web interface) with no database server, web server or identity provider to set up. It checks your services on a schedule, records outages, alerts your channels and publishes status pages.

This guide explains how to use Opskest. Installing and operating it (Docker Compose, systemd, reverse proxy, backups, security) is covered in [deploy/DEPLOYMENT.md](deploy/DEPLOYMENT.md).

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

- Monitoring: 24 monitor types (web, network, protocol handshakes, database queries, heartbeats, groups and manual monitors), checked on your intervals and, optionally, from other countries.
- Alerting: 24 channel types, with templates, quiet hours, storm grouping and a delivery log.
- Publishing: public or private status pages, a wallboard for wall displays, subscriptions, feeds and SVG badges.

The sidebar has Overview, Monitors, Projects, Providers, Incidents, Maintenance, Status Pages (users and admins) and Settings (admins).

These terms are used throughout:

| Term | Meaning |
| --- | --- |
| Project | A named collection that organizes monitors (the API calls it a group). It is for organization only and has no separate access rules. |
| Monitor | One thing being watched, with its schedule and alert settings. |
| Check | One run of a monitor. |
| Outage | The recorded period during which a monitor was down. Opened and closed automatically. |
| Incident | A report written by a person about a problem, with a timeline. Never created automatically. |
| Maintenance window | A planned period in which chosen monitors do not alert. Checks continue. |
| Status page | A public or private page of selected services, incidents and maintenance. |
| Wallboard | A status page created as a board: a live grid for a wall display. |
| Channel | A destination for alerts (Slack, email, webhook and so on). |
| Region | A country from which a monitor is also checked, through Globalping. |
| Third-party provider feed | A monitor that follows another company's status page or RSS feed. It only alerts. |

---

## 2. Install and run

[deploy/DEPLOYMENT.md](deploy/DEPLOYMENT.md) is the full installation guide: Docker Compose, plain Linux with systemd, Windows and macOS, reverse proxy and TLS, the configuration reference, backups and restore. This section covers only what you need to get started.

### Quick start

```bash
git clone https://github.com/opskest/opskest.git && cd opskest
mkdir -p data backups && sudo chown -R 65532:65532 data backups
docker compose up -d --build
```

Then open <http://localhost:8085>. Compose publishes the container on `127.0.0.1:8085` only, so it is not reachable from other machines until you put a reverse proxy in front of it.

The container runs as uid 65532, which is why `data` and `backups` are chowned first. If they are not writable, Opskest stops at boot and prints the exact command to run.

### Configuration

Every setting is optional. With none, Opskest listens on port 8080, keeps its database in `./data` and backups in `./backups` (relative to where you start it), and generates its own encryption key.

Settings come from environment variables or a `.env` file. Copy `.env.example` to `.env` to change anything. These are the ones most installs set:

| Variable | Purpose |
| --- | --- |
| `APP_URL` | The address people open, with scheme (for example `https://status.example.com`). Sign-in and setup are refused from any other address. |
| `PORT` or `LISTEN_ADDR` | Where to listen. Default `:8080`. |
| `DISPLAY_TZ` | Time zone for charts, daily rollups, the backup hour and new maintenance windows. |
| `DATA_DIR`, `BACKUP_DIR` | Database and backup folders. |
| `SECRET_ENCRYPTION_KEY` | Encryption key for stored secrets. Generated into `DATA_DIR/encryption.key` if unset. |

The full list, with defaults and the Compose-only variables, is in the [configuration reference](deploy/DEPLOYMENT.md#6-configuration-reference).

Opskest serves plain HTTP on one address. It has no TLS or certificate management of its own, so use a reverse proxy for HTTPS (see [Reverse proxy and TLS](deploy/DEPLOYMENT.md#4-reverse-proxy-and-tls)). `APP_URL` must be the exact public origin: Opskest does not read `Host` or `X-Forwarded-Proto` from the proxy.

### The encryption key

Channel configurations, monitor passwords and tokens, and two-factor secrets are encrypted with one key. A corrupt key file is an error and is never replaced, because a new key would make every stored secret unreadable. Keep the key with your backups: a database copy cannot be decrypted without it. See [The encryption key](deploy/DEPLOYMENT.md#73-the-encryption-key).

### First run

Open `APP_URL` exactly as configured. The first screen creates the first administrator. It works once (an atomic operation, so concurrent requests cannot create two), and there are no seeded credentials or login bypass. Create the administrator before you share the address, then add teammates in Settings > Team access.

### Backups, restore and upgrades

Settings > Backups sets the daily backup hour and how many snapshots to keep, and lets you back up now, download, delete, upload and restore. Restoring makes Opskest exit so the restored database can be swapped in at the next start; Compose and the supplied systemd unit restart it automatically. To upgrade, take a backup, replace the binary or run `docker compose up -d --build`, and restart. Migrations run on start and only move forward, so to go back, restore the pre-upgrade backup. Details are in [Data, backups and restore](deploy/DEPLOYMENT.md#7-data-backups-and-restore).

---

## 3. Roles and team

There are three roles, admin > user > viewer, plus `disabled` (the account is kept but cannot sign in).

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
| Channels: list (id, name, type, default) | yes | yes | yes |
| Channels: configuration, create, edit, delete, test | no | no | yes |
| Settings, regions, delivery log, audit log, storage | no | no | yes |
| Backups, restore, updates | no | no | yes |
| Team: add, change roles, disable, remove | no | no | yes |

- A signed-out request gets 401; a role below the minimum gets 403. Admin-only data is never returned to lower roles.
- The role is read on every request, so changes apply at once. Disabling a user ends their sessions.
- Nobody can change their own role. The last administrator cannot be demoted, disabled or deleted.
- Admins add teammates in Settings > Team access with a username (3 to 32 characters: letters, digits, `.`, `_`, `-`) and a password (12 to 128 characters). The default role is viewer.
- Everyone manages their own password and two-factor authentication under Account security in the account menu.

### Two-factor authentication

Two-factor authentication (TOTP) is optional. Enrolling shows a secret to scan and 10 single-use recovery codes. Sign-in then asks for a current code or a recovery code. The secret is stored encrypted. Store the remaining recovery codes outside Opskest.

Passwords use PBKDF2-HMAC-SHA256 (600,000 rounds). Sessions last 30 days and slide forward with use. Sign-in is limited to 15 requests per user name and 60 per source address per 10 minutes. Every request counts, including the code step.

An administrator can require two-factor authentication for everyone in Settings > Team access, under Two-factor authentication:

- Anyone without an authenticator app is sent to Account security and can use nothing else until they set one up. Public status pages keep working for them but treat them as signed out, so private pages stay private.
- People cannot turn off their own two-factor authentication while it is required.
- The team table shows who has two-factor authentication on. An administrator can use Reset two-factor on another user (for example after a lost phone), which removes that user's authenticator and recovery codes; the user is asked to set it up again at the next sign-in. You cannot reset your own.
- An administrator without an authenticator can still reach the settings endpoint (`PUT /api/settings`), so the requirement can be switched off through the API if needed.
- If the only administrator loses both their device and their recovery codes, nobody else can reset them. Keep the recovery codes safe.

---

## 4. Monitors

A monitor has a name (1 to 120 characters), type, target, schedule and alert settings. The form validates as you type and has a Test button that runs the check without saving.

Tags label monitors so you can find and group them. Anyone who can edit monitors can create, rename, recolour and delete tags from Manage tags in the monitor form (a tag you create there is added to that monitor). Administrators also see them all in Settings > Tags, with the number of monitors using each. A tag name is 1 to 40 characters and unique, and a colour is `#rrggbb`. Deleting a tag removes it from its monitors and changes nothing else.

Check location: a monitor created in the web form checks from this server only. Worldwide or a custom list of countries is opt-in, and applies to HTTP, ping and DNS monitors only, because those checks send the target to a third party (see [section 10](#10-worldwide-checks)). Monitors created by importing a URL list, or a file without settings, also start on this server only.

### Types

| Group | Type (API id) | Target | Down when |
| --- | --- | --- | --- |
| Web | HTTP(S) (`http`) | URL | Connection, TLS or request fails; status not accepted; keyword, JSON check or assertion fails |
| Web | WebSocket (`websocket`) | `ws(s)://` URL | Connect or send fails, no reply, or reply lacks expected text |
| Web | SSL certificate (`tls`) | `host[:443]` | Handshake fails (expired, wrong name, untrusted) |
| Network | TCP port (`tcp`) | `host:port` | Cannot connect; TLS fails; expected reply missing |
| Network | Ping (`ping`) | host or IP | No reply (partial loss above the maximum is degraded) |
| Network | DNS (`dns`) | hostname | Lookup fails, is empty, or does not match the expected values |
| Network | UDP (`udp`) | `host:port` | No reply, or reply lacks expected bytes |
| Network | NTP (`ntp`) | `host[:123]` | No valid reply, stratum 0, or offset above your maximum |
| Services | SSH, SMTP, Redis, PostgreSQL, MySQL/MariaDB, MongoDB, MQTT, AMQP, Memcached, gRPC health, banner (`ssh`, `smtp`, `redis`, `postgres`, `mysql`, `mongodb`, `mqtt`, `amqp`, `memcached`, `grpc`, `banner`) | `host:port` | The server does not answer its protocol handshake (details below) |
| Databases | PostgreSQL queries, MySQL/MariaDB queries (`postgres_sql`, `mysql_sql`) | `host:port` | Login fails, the probe cannot run, or the probe condition is met |
| Heartbeat | Push (`push`) | none (own URL) | No call within interval plus grace, or a call says down |
| Heartbeat | Manual (`manual`) | none | You set it down |
| Logic | Group (`group`) | none | Its rule over other monitors is not met |

Service monitors prove that a server answers and never log in (Redis and MQTT authenticate only if you set a password).

| Type | What it does |
| --- | --- |
| SSH | Expects a first line starting `SSH-`. |
| SMTP | Reads the greeting, then sends `EHLO`. |
| Redis | Sends `PING`. |
| PostgreSQL | Sends the SSL request. |
| MySQL | Reads the greeting. |
| MongoDB | Sends `isMaster`. |
| MQTT | Sends `CONNECT`. |
| AMQP | Requires `Connection.Start`. |
| Memcached | Sends `version`. |
| gRPC | Calls the standard health check. |
| Banner | Requires your text in the first line. |

They never put a password or server reply in a failure reason (SMTP includes the first line of an error reply). A refused, reset or timed-out connection is always down.

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

Through the API, an omitted retries value is 0. Interval, retry interval and timeout default as above.

- Retries: with retries set to 2, the first failure makes the monitor pending and the third consecutive failure makes it down, with the retry interval between attempts. With 0, the first failure is down.
- Degraded means a successful check that is slow, or whose certificate has expired or is within the warning period. It alerts only if "alert when degraded" is on.
- Upside down inverts the result.
- Parent monitors: while a parent is down or in maintenance, a child's outages are still recorded but its alerts are held.
- Certificate expiry alerts apply to every monitor that sees a TLS certificate. Each threshold alerts once per certificate, a renewed certificate restarts the ladder, and a restart does not repeat alerts.
- Response-time alert (not for push): fires when a percentile (50, 90, 95 or 99) over 5 to 60 minutes exceeds a limit in ms, a multiple (1.5 to 20) of the usual value, or either. The window cannot exceed the last 100 checks.
- Internet-loss guard: after a failure, Opskest tries TCP to `1.1.1.1:53`, `8.8.8.8:53` and `208.67.222.222:53` (2 s timeout, cached for 30 s). If none answers, the server is treated as offline: the result is recorded as unknown and nothing alerts. The guard does not apply to push monitors. A server that cannot reach those addresses never marks monitors down.

### Type details

HTTP: any common method (default GET); body up to 64 KiB; up to 30 headers (any can be secret and is stored encrypted); authentication none, basic, bearer or OAuth2 client credentials; accepted codes (default `200-299`); 0 to 20 redirects (default 10); ignore TLS errors; keyword check; JSON check; up to 20 assertions (status, response time, header, body, JSON path); outbound proxy; extra CA certificates; client certificate (mutual TLS); and a daily domain-expiry check through RDAP. Checks run in that order and stop at the first failure. Alerts include the start of the response body, so do not monitor a URL whose response is sensitive. Each check opens a new connection and sends `User-Agent: opskest/<version>`.

Ping: 1 to 10 packets (default 3), 500 ms apart, and a maximum loss percentage (default 34).

DNS: record types A, AAAA, CNAME, MX, TXT and NS; resolver blank or `ip[:port]`; up to 32 expected values with match `equals`, `contains`, `all` or `exact` (default `equals` for one value, `exact` for several). A mismatch reason lists the answers received.

TCP and UDP: `send` and `expect` are text or `hex:` bytes (TCP `expect` can be a regex). Replies are never put in reasons.

Push: the job calls `<APP_URL>/push/<token>` (GET or POST) with optional `status=up|down`, `msg=<reason>` and `ping=<ms>`. The monitor is down if no call arrives within the interval plus grace (0 to 3600 s, default 30). The token is the only authentication, so keep the URL private. An unknown token gets 404. The limit is one call per second per token (extra calls get 200 and are ignored), and calls to a paused monitor are ignored.

Group: follows 1 to 100 other monitors by rule `all` (default), `any`, or `min` (at least N up). Paused, maintenance and unchecked children are left out; pending children count as up. Groups may nest but not recurse. A group runs on its own interval (10 to 30 s for fast) with its own retries (usually 0), has no timeout or upside-down mode, and its children still alert separately. Import cannot carry groups.

Manual: set up, degraded or down from the monitor page or with `POST /api/monitors/<id>/status` and `{"status": "...", "reason": "..."}`. It takes effect at once, repeats every interval so uptime accrues, survives restarts, alerts normally, and cannot be set while the monitor is paused.

### Database monitors

These are the only checks that log in. Use a dedicated least-privilege account. Each check opens one connection, runs one read-only transaction (PostgreSQL also sets `default_transaction_read_only = on`) and closes the connection. With TLS on, the certificate and host are verified unless "ignore TLS errors" is on. The password is stored encrypted.

| Probe | Down when |
| --- | --- |
| `query` (your SQL) | The query fails, or a comparison on the first column of the first row fails (or there are no rows). With no comparison it only has to run. |
| `deadlocks` | New deadlocks since the previous check exceed the tolerated count (default 0). |
| `long_running` | More than the tolerated count (default 0) of queries run longer than 1 to 86400 s (default 30). |
| `blocked` | More than the tolerated count (default 0) of sessions wait on a lock. |
| `connections` | The percentage of the connection limit in use exceeds 1 to 100 (default 80). |

A custom query must start with `SELECT`, `WITH`, `SHOW`, `EXPLAIN`, `VALUES` or `TABLE`, be a single statement and be at most 4000 characters. It is checked again at run time. Comparisons are `eq`, `ne`, `gt`, `gte`, `lt`, `lte` and `contains`. The deadlock baseline is kept in memory, so the first check after a start cannot alert. To see other sessions, grant `pg_monitor` (PostgreSQL) or `PROCESS` (MySQL/MariaDB). Reasons carry counts and durations, never query text or rows. The database's own error message (its code and first line) is shown and could quote values.

### Organizing monitors

- Projects group monitors (each has a dashboard); tags label them. Tags never appear on public pages.
- Bulk actions work on 1 to 500 monitors: pause, resume, delete, move to a project, set retries, add or remove a channel. The whole selection is validated first.
- Import and export is under Settings > Import & export. Export produces JSON or CSV with project, tag and channel names and no secrets. Import takes a JSON array, an export file, CSV (a template is offered) or a plain URL list, up to 5000 rows and 5 MiB, with a dry-run preview of create, skip or error per row. A duplicate is the same name, type and target.
- Templates: none are built in. Save the current form as a template (name 1 to 80 characters, unique ignoring case) and apply it to new monitors. A template keeps the type, schedule, alert settings, channels, tags, runbook, type settings and non-secret headers; the server removes the name, target, project, secrets, push token and parents. Non-secret settings are kept as typed, including user names, database names and query text, so mark any token header as secret first. Users and admins manage templates.

---

## 5. Third-party provider feeds

Providers (`/third-party`) follows other companies' published incidents. Browse a catalog of 99 providers in nine categories, or connect any Statuspage.io, Meta, Google, Slack or RSS/Atom feed URL. A feed is an HTTP monitor with the `vendor` setting, placed in the "3rd Party Providers" project. Users and admins can connect feeds.

A feed only alerts:

- It is down or degraded only when the vendor publishes an incident. A feed that cannot be fetched or understood never alerts.
- It sends Down, Reminder, Recovered and (if enabled) Degraded alerts to its channels, labelled as a provider incident. There is nothing to acknowledge, and Down is sent once per episode.
- It is never an outage, uptime, SLA, incident link, status page component, dependency, group child or maintenance target (the API answers 400). It is also excluded from outage lists, counts, clusters, region checks, reports, exports, the events feed and `/metrics`.

The URL decides how a feed is read: RSS/Atom (the newest entry decides), `metastatus.com`, Google status JSON, `status.slack.com`, or Atlassian Statuspage `/api/v2/summary.json` (including `status.*` hosts). A plain HTML page cannot be read for incidents and is unsupported. A vendor-hosted domain that is not `status.*` is usually readable only through `/history.rss`. The catalog was checked once, from one machine. Vendors move their pages, so use Check now on a feed to confirm it reads.

---

## 6. Alerts

Admins create channels in Settings > Alert delivery, and each monitor chooses its channels. Mark a channel as a default to add it to new monitors. Apply to all adds a channel to every monitor that lacks it (paused ones included). The monitor list can also add or remove a channel for up to 500 selected monitors.

A channel's configuration is stored encrypted. The API reports only whether a URL, `apiKey`, `routingKey`, `password`, `hmacSecret` or `headers` is set. Other fields are returned as saved, to admins only.

### Channel types

Use Test after creating a channel. A successful test only means the service accepted the request. Required fields use their JSON names:

| Channel (type id) | Required | Optional |
| --- | --- | --- |
| Slack (`slack`), Teams (`teams`), Google Chat (`gchat`), Discord (`discord`) | `url` (`https` webhook) | none |
| Mattermost (`mattermost`), Rocket.Chat (`rocketchat`) | `url` (`http(s)` webhook) | none |
| Telegram (`telegram`) | `apiKey` (bot token), `target` (chat id) | `url` (self-hosted Bot API) |
| ntfy (`ntfy`) | `url` including the topic | `apiKey`, or `username` and `password` |
| Gotify (`gotify`) | `url`, `apiKey` | none |
| Pushover (`pushover`) | `apiKey`, `target` (user or group key) | `url` |
| Matrix (`matrix`) | `url` (homeserver), `apiKey`, `target` (room id) | none |
| Email (`email`) | `host`, `from`, `to` (list) | `port` (set it; the form starts at 587), `tlsMode` (`starttls`, `tls`, `none`), `username`, `password` |
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

- Email uses Opskest's own SMTP client (also used for subscriber mail). With a `username` it authenticates with `PLAIN`, which Go refuses over an unencrypted remote connection, so use `starttls` or `tls`.
- PagerDuty and Opsgenie get one alert per monitor (`opskest-<monitor id>`), triggered on down and closed on recovery. They never get grouped messages or informational events.
- Apprise API reaches services not listed here: save their URLs under a key in Apprise and point the channel at it.

### What alerts

- Down and Recovered, always.
- Degraded and its all-clear, if enabled on the monitor.
- Reminder while down, at the per-monitor interval. Acknowledging the outage stops it.
- Certificate and domain expiring.
- Failures from other countries, and response-time alerts.
- Test.

Maintenance suppresses alerts, although a recovery for an outage that already alerted is still sent. Opskest has no escalation chains; use PagerDuty or Opsgenie for on-call. An alert shows the target, project, reason, HTTP status and start of the response body, duration, response time, monitor id and runbook, so send alerts only to channels you trust.

### Templates and quiet hours

A channel `template` (up to 1000 characters) replaces the details of a single-monitor alert. Placeholders are `{name}`, `{status}`, `{reason}`, `{duration}`, `{url}` and `{time}`. An unknown `{name}` is rejected on save. A webhook receives the result in `message`, PagerDuty ignores the template, and grouped messages are not templated.

Quiet hours silence a channel in a recurring window: days, plus a start and end on the display-zone wall clock. Days name the day the window starts, and a window that ends before it starts runs past midnight. Alerts in the window are dropped for that channel, not queued. "Bypass critical" still lets down alerts, their reminders and the recovery through. Test and Resend ignore quiet hours.

### Webhook payload and signature

Webhooks `POST` JSON with these fields:

- `event`: one of `down`, `recovered`, `degraded`, `degraded_cleared`, `reminder`, `cert_expiring`, `domain_expiring`, `regional`, `regional_cleared`, `latency_high`, `latency_cleared` or `test`
- `monitor`: `id`, `name`, `group`, `target`, `runbook`
- `status`, `reason`, `since` (Unix ms), `duration_s`, `latency_ms`, `code`, `response`, `url`, `message`
- `grouped`, with `monitors`, for grouped messages

With `hmacSecret` set, Opskest adds this header:

```
X-Opskest-Signature: sha256=<hex digest>
```

The value is the HMAC-SHA256 of the exact request body. Verify it against the raw body, in constant time. Responses outside 200 to 299 count as failed.

### Delivery and grouping

- Each channel has its own queue (1000 items) and worker. The intake queue holds 10,000. Overflow is dropped and counted.
- Each attempt has a 10 s timeout. Failures retry after 5 s, 30 s and 2 min (four attempts in all). Every attempt is logged in Settings > Delivery history (kept 30 days) with a redacted error, and failed entries can be resent. A test is sent once.
- Storm grouping: down events are held for 3 s. If at least the storm threshold of monitors (default 5) go down within the storm window (default 30 s), one grouped message replaces the individual ones, and recoveries are grouped the same way. Only Slack, email, Teams, Google Chat and webhook channels group. All others, PagerDuty and Opsgenie included, get one message per monitor.

---

## 7. Outages and incidents

Outages are automatic. When a monitor goes down (after its retries) the engine opens an outage, and recovery closes it. Outages drive uptime, SLA and alerts. You cannot create one by hand.

Incidents are created by people only. Nothing opens one for you. A user or admin declares an incident to communicate about a problem, typically on a status page. An incident has a title, impact (minor, major, maintenance), severity (critical, major, minor), an optional commander, linked monitors and pages, a status (investigating, identified, monitoring, resolved), a timeline and an optional postmortem. Third-party feeds cannot be linked.

### Working an outage

Open Incidents > Outages, or a monitor's Events tab.

- Acknowledge (any role) stops that outage's reminders and records who did it.
- Each outage has a thread of up to 200 notes (1 to 2000 characters each). Everyone signed in can read and add notes. Authors edit or delete their own, and an admin can edit or delete any. Notes are team-only and never public. List search matches any note, and "Needs note" finds empty threads. Audit entries omit note text.

### Working an incident

- Declare the incident, then post updates with a status and text. Public updates notify the subscribers of the linked pages. An update marked internal is a team note: it is hidden from public pages and not sent.
- Users and admins can edit an update's text or team-note flag in place. The update keeps its status and time and shows "(edited)". Editing does not change the incident status and does not notify anyone again. The public page shows the corrected text, and an entry turned into a team note leaves the page. Change the title, impact, severity and commander with Edit on the incident.
- Resolve the incident with an update whose status is resolved, then add a postmortem.

---

## 8. Maintenance windows

Users and admins create windows under Maintenance. Alerts are silenced while checks keep running.

- Kinds: a one-off window has a start and end, at most 30 days apart. A weekly window has weekdays, a local start time and a duration of 1 to 1440 minutes.
- Time zone: each window has an IANA zone (default: the display zone). Weekly windows follow that zone's wall clock through daylight saving. A start time that does not exist on a spring-forward day has no occurrence that day, and a repeated fall-back hour runs once.
- Targets: at least one monitor or project (a project also covers monitors added to it later). Third-party feeds cannot be targets. A window can be disabled without deleting it.
- Effect: windows are re-evaluated every 15 s and on each edit. Covered monitors do not alert (a recovery for an outage that already alerted is still sent). Outages do not open during maintenance, so SLA is unaffected, and children of a monitor in maintenance have their alerts held. Public pages show windows in progress and those starting within seven days, only if they target a service on that page. They never show a window's targets or author, and a page can turn this off.

---

## 9. Status pages and wallboards

Status Pages (users and admins) is a searchable directory with the filters Public, Private, Board and List. Creating a page is a short flow: type, title, address (slug) and description; then services; then public or private.

A page's type is fixed. A page is a status page (list) or a wallboard (board) from creation, never both, and the API refuses a change with 400. A board's `/status/<slug>` redirects to `/status/<slug>/board`, and `/board` on a list page is a 404.

The editor has these sections: Basics; Services (ordered services and project groups, display names, public descriptions); Appearance; Content; Updates and subscribers; Access. A sticky bar holds Save and Discard and warns before you leave with unsaved changes. View saved page shows the last saved version, not a draft.

- Appearance: logo, accent color, website and support links (`https` only), and a fixed set of theme options: header (plain, tinted, solid), font (chosen from fonts already on the visitor's device), corners, and the default appearance for new visitors. There is no custom CSS, because it could hide an outage or load from another site. Status colors never change with branding.
- Content: description (2000 characters), footer (600) and a pinned announcement (300 characters, one line, four styles), written in a small Markdown subset. Text is rendered as elements, never as HTML, and the API rejects raw tags and unsafe links.
- Access: public or private; history of 30, 60 or 90 days; optional uptime percentages (on by default), last-check time, certificate days and response times (off by default); auto-refresh (30 s to 5 min); public address; and show scheduled maintenance (on by default).

### Visibility

A private page answers 404 to signed-out visitors and shows signed-in users everything, including reasons and response times. A public page shows display names, statuses, uptime and incident text, and omits targets, failure reasons, runbooks, tags and team notes. Public endpoints are rate-limited per client (20 requests per 10 s, burst 40).

### What visitors see

The page is a single column: an overall-status banner, active incidents and maintenance, then the services in the page's groups, each with a daily bar per day and its status in words, optional response times, and 14 days of incidents with a history page and month calendar. Expandable parts start collapsed (a page can be set to open everything on load), and deep links such as `#incident-12` open them. Dates use the visitor's or the server's time zone, named on the page.

### Subscriptions, feeds and badges

- Subscribe offers email (double opt-in, only if the page has an email channel), RSS and Atom feeds, chat-tool instructions, and visitor-supplied Slack webhooks (stored encrypted). Subscribing always returns the same neutral reply. The confirm link lasts 48 hours, every email has an unsubscribe link that acts on click (not on open), and subscribing is limited to 5 attempts per 10 minutes per client.
- Feeds are under `/api/public/pages/<slug>/`: `feed.atom` (Atom 1.0, 50 newest), `feed.rss`, and `summary.json` (shaped like Atlassian Statuspage's summary). A page can switch its feeds off, after which they answer 404. Responses are cached for 30 to 60 seconds with an `ETag`, and private pages are never cacheable.
- Badges are SVG, work for public pages only, and are cached for 60 s: `badge.svg` (page), `services/<id>/badge.svg` (service) and `services/<id>/uptime.svg?window=24h|7d|30d|90d` (default 30d; truncated, never rounded up to 100%). Badges show only a status word or percentage and the public display name. Any unknown or private target returns the same "not found" image. The editor's Embed box builds Markdown and HTML snippets.

### Your own domain

A page's public address (an `https` origin) sets its canonical, feed and embed URLs. It does not create DNS, a certificate or a proxy rule. To serve a page on your own domain:

1. Point DNS at your reverse proxy and let the proxy obtain the certificate.
2. Forward only `/status/`, `/api/public/`, `/assets/`, `favicon` and `robots.txt` to Opskest, with the stream path unbuffered.
3. Enter the address in the page's Access section.

Sign-in still works only from `APP_URL`.

### Wallboard

A board page at `/status/<slug>/board` is a live grid for a TV. It shows tiles per service or per project, fit-all or 8, 12 or 16 per screen rotating every 10 s (problems stay on every screen), and an optional alarm sound when a service goes down or degrades. You can choose sounds or mute them, and browsers need one click before they allow sound. Add `?kiosk=1` to hide the toolbar and cursor.

### Search engines

Only public status pages are indexable. Everything else (the app, the API, feeds, badges, private pages and the wallboard) sends `X-Robots-Tag: noindex, nofollow, noarchive`.

---

## 10. Worldwide checks

Your server checks every monitor from where it runs. Opskest can also check public HTTP, ping and DNS monitors from other countries through the free Globalping network. Admins configure it in Settings > Check regions. Each monitor chooses worldwide, this server only, or a custom country list.

- Rotation: eligible monitors are checked from the configured countries in turn, paced to an hourly budget (default 225 tests per hour; Globalping allows 250 free, or 500 with a token you can save in Settings). If one country fails while others succeed, Opskest treats it as a regional issue: it re-checks once, records the result and, if region alerts are on, sends an alert such as "failing from China, Russia".
- Outage confirmation (off by default) also checks a new outage from the monitor's countries and records the verdict.
- Defaults: the rotation and region alerts are on by default, but they only act on monitors set to worldwide or a custom list. Monitors created in the web form or by import start as "this server only". A monitor created through the API with no `regionMode`, and one that existed before the setting, counts as worldwide. If you create public HTTP, ping or DNS monitors through the API, set `regionMode` to `local` unless you want them sent to Globalping.
- What leaves your server: only the scheme, hostname and path (HTTP) or the hostname (ping and DNS), never query strings, credentials, headers or bodies. Never sent at all: monitors with custom headers, authentication, a body, a method other than GET or HEAD, a proxy, a custom CA or client certificate, and private or internal names. The private-name test looks at the name only (private IPs, names without a dot, suffixes such as `.local` and `.internal`) and does no DNS lookup, so a public-looking name that points at a private address is still sent.

Other connections Opskest makes itself:

- The internet-loss guard: TCP port 53 to three DNS servers, only after a failure.
- Domain-expiry lookups: `data.iana.org` and registry RDAP servers, only for HTTP monitors with that check on.
- Update checks: the release feed, see [Updates](#updates).

---

## 11. Reports, SLA, percentiles and metrics

- Uptime comes from rollups: hourly (kept 90 days) and daily (kept 730 days), so history outlives raw checks.
- Percentiles (p50, p95, p99) come from latency histograms stored with each rollup. Missing or corrupt data shows "no data", never 0.
- Monthly SLA is on the SLA card of a monitor or project page (with CSV export) and at `GET /api/reports/sla`. Availability is time-based: outage minutes against the minutes the monitor existed that month, with maintenance excluded. The report also gives check success percentage, downtime, outage count, longest outage, MTTR, percentiles and the error budget against a target (default 99.9%), for any of the last 24 months. The scope can be a monitor, project, status page or everything. Third-party feeds have no SLA.
- Prometheus: set `METRICS_TOKEN` and scrape `/metrics` with `Authorization: Bearer <token>` (404 if unset, 401 if wrong). The series are `opskest_monitor_up` (1 up or degraded, 0 down, -1 otherwise), `opskest_monitor_latency_ms`, `opskest_monitor_uptime_24h` and `opskest_monitor_cert_days` (labels `id`, `name`, `group`, `type`), plus `opskest_scheduler_lag_p99_seconds`, `opskest_writer_dropped_total` and `opskest_checks_inflight`. Third-party feeds are not exported.
- Retention: raw checks are kept for the number of days set in Settings > Monitoring rules (1 to 90, default 7). The notification log is kept 30 days and the audit log 365 days. Settings > Storage shows what is held, clears history older than a period you choose (ongoing outages are kept) and compacts the database file.

---

## 12. Settings and updates

Settings is admin-only. Its tabs are:

| Tab | Holds |
| --- | --- |
| Alert delivery | Channels: add, edit, test, set as default, apply to all |
| Monitoring rules | Raw retention (1 to 90 days), default reminder interval (0 to 1440 min), storm threshold (2 to 100) and window (5 to 600 s) |
| Team access | Accounts, roles, the two-factor requirement, recent activity (audit log) |
| Check regions | Rotation, region alerts, confirmation, countries (1 to 25), hourly budget, Globalping token, ad-hoc check |
| Import & export | JSON or CSV export; import with preview |
| Tags | All tags, with usage counts |
| Backups | Schedule, snapshots, download, upload, restore |
| Storage | Database size, clear history, compact |
| Delivery history | Notification attempts; resend failed ones |
| About & updates | Version, install method, engine figures, metrics path, update controls |

### Updates

Opskest installed as a plain binary (Linux, Windows or macOS) can check a release feed, show the new version and its notes, and update itself: back up, download, verify, swap the file, restart, with a one-click rollback.

Docker installs never replace their container. They can be told that a new version exists, but you update them yourself: `git pull`, then `docker compose up -d --build`. Roll back by running the previous image tag.

Official release builds come with the project's feed address and signing key built in. They check GitHub for new releases once installed, and you can turn this off with Check for updates. Builds made from source, and the Docker image, have no built-in feed or key, so nothing is contacted until you set a feed URL in Settings.

| Option | Meaning |
| --- | --- |
| Check for updates | Master switch. When off, nothing is fetched, even by Check now. |
| Release feed | HTTPS URL of `manifest.json` (plain `http://` is accepted only for `localhost`, `127.0.0.1` and `::1`). A feed saved here wins over a built-in one. |
| Mode `off`, `notify` (default) or `auto` | `notify` checks about every 24 h (the first check is two minutes after start) and shows a banner; installing is a click on Update now. `auto` also installs a verified newer version during the hour you choose (default 03:00, display zone), at most once a day, never within 24 h of a failure, and never a version you rolled back from. |
| Public key | Write-only (the interface shows a fingerprint). A saved key wins over a built-in one. Without any key you are notified of new versions but cannot install them. |
| Check now, Update now, Roll back to X | Fetch (at most once per 10 s); back up, download, verify, install and restart; swap back to the kept `<exe>.prev`. |

Verification: the manifest needs a valid ed25519 signature (`<feed URL>.sig`). The download must match the manifest's size and SHA-256, start with the executable magic bytes for the OS, and print exactly the manifest's version when run as `<new> version`. Everything is fetched over HTTPS with timeouts and size limits, and any failure before the swap changes nothing. Pre-releases are offered only to installs already on one.

Restart and rollback: the old binary is kept as `<exe>.prev`. By default (`OPSKEST_UPDATE_RESTART=self`), on Linux and macOS the new binary replaces the process in place, and on Windows a detached copy starts. Under a supervisor such as a service wrapper or systemd, set `OPSKEST_UPDATE_RESTART=exit` so Opskest swaps the files and exits with status 0 for the supervisor to restart (systemd needs `Restart=always`). If the new binary cannot start in `self` mode, the old one is restored. Migrations only move forward, so if a rolled-back version refuses the database, restore the backup named in the update. To roll back by hand with Opskest stopped, run `mv opskest.prev opskest` (on Windows, delete `opskest.exe` and rename `opskest.exe.prev`). The operational details are in [Upgrading](deploy/DEPLOYMENT.md#37-upgrading).

### Publishing a release

Official releases are built and signed by GitHub Actions (`.github/workflows/release.yml`). Pushing a tag such as `v1.4.0` runs the tests, builds the four binaries, signs `manifest.json` with a key stored as a repository secret, publishes the GitHub release and records build provenance. The maintainer steps, including the one-time setup of the signing key, are in [.github/CONTRIBUTING.md](.github/CONTRIBUTING.md).

To publish by hand instead, for example from your own fork or to an internal feed:

```bash
go run ./cmd/releasetool keygen      # once: release.key (private, never commit) and release.pub
OPSKEST_UPDATE_FEED_URL=https://example.com/opskest/manifest.json scripts/release 1.4.0 https://example.com/opskest/1.4.0 notes.md
```

`scripts/release` builds the four binaries, writes and signs `dist/manifest.json`, verifies it, and embeds the public key and feed address. Upload the binaries first and `manifest.json` plus `manifest.json.sig` last, because the manifest is what makes a release visible. To rotate keys, give every install the new key in Settings before you sign with it.

---

## 13. API and automation

The web app uses a JSON API under `/api/`. It is not a versioned public contract, but you can script against it.

- Sessions: `POST /api/auth/login` takes a local user name and password (a second request supplies a TOTP or recovery code). It sets the HttpOnly cookie `opskest.sid` and a readable cookie `opskest.csrf`. Copy the second into an `X-CSRF-Token` header on every non-GET request. Setup and sign-in also require an `Origin` that matches `APP_URL`. There are no API keys or personal access tokens.
- No session needed: `GET /healthz`, `GET /api/runtime`, `GET|POST /push/<token>`, `GET /metrics` (bearer token), and the public status endpoints under `/api/public/`.
- Session routes (roles as in [Roles and team](#3-roles-and-team)): `monitors` (CRUD plus `/check`, `/status`, `/pause`, `/resume`, `/duplicate`, `/test`, `/bulk`, history and diagnostics), `groups` (projects), `tags`, `templates`, `channels`, `outages` (`/ack`, `/notes`), `incidents`, `maintenance`, `pages`, `reports/sla`, `events`, `export/monitors` and `import/monitors`. Admin routes are `users`, `settings`, `regions`, `alert-log`, `data/*`, `audit-log`, `backups`, `updates` and `system`. `GET /api/stream` is the live Server-Sent Events feed. Lists default to 50 rows and cap at 200.

```bash
curl -fsS "https://opskest.example.com/push/<token>?status=up&msg=ok&ping=120"   # heartbeat
```

`POST /api/monitors/bulk` takes `{"ids": [...], "action": "pause|resume|delete|move|retries|add_channel|remove_channel", ...}` (1 to 500 ids). `POST /api/import/monitors` takes a JSON array, an export file, `text/csv`, or `{"urls": [...], "groupName": "", "type": "http", "intervalS": 60}`. Add `?dryRun=true` to preview.

---

## 14. Security and privacy

- Authentication: local accounts only. There is no external identity provider, seeded credential or bypass. Sign-in is throttled and writes are CSRF-protected.
- Secrets at rest: channel configurations, monitor passwords and tokens, secret headers, client keys, proxy and OAuth secrets, visitor Slack webhooks and TOTP secrets are encrypted with the [encryption key](#the-encryption-key). They are write-only in the interface and API, and templates and exports strip them.
- Never logged: logs hold only an error string trimmed to 300 bytes. They contain no monitor settings, headers, payloads, channel configurations, subscriber addresses or webhook URLs. Errors in the notification log are redacted.
- Public pages never leak: pages, feeds, the JSON summary and badges use dedicated public representations that omit targets, reasons, runbooks, tags and team notes. Automated tests search every public endpoint, including the first live-stream snapshot, for a deliberately sensitive target and reason.
- 404, never 403, for private or unknown things: a private page without a session, an unknown push token, monitor id or badge target. A 403 would confirm that the thing exists.
- What leaves your server: alerts to your channels (target, reason, start of the response body); checks to your targets; and, only when used, hostnames to Globalping, domain names to IANA and RDAP servers, and requests to the update feed.

To report a vulnerability, see [.github/SECURITY.md](.github/SECURITY.md). The production hardening checklist is in [deploy/DEPLOYMENT.md](deploy/DEPLOYMENT.md#8-security-checklist-for-production).

---

## 15. Performance, limits and contributing

### Performance

The design target is 2,000 monitors at a 30 s interval with scheduler lag p99 under 1 s, RSS under 150 MB and under 0.5 vCPU. The figures below come from a local load harness that is not part of this repository. It runs the real scheduler, engine, writer and HTTP checker against a local mock server. In the latest run (Docker with 2 CPUs and 1 GB, 3 minutes), scheduler lag p99 was 1.38 ms, RSS 61.4 MB and CPU 0.100 cores, with no skipped runs. Earlier five-minute runs under the same limits peaked between 54.9 and 67.7 MB and between 0.075 and 0.093 cores. Starting 2,000 monitors took 455 to 579 ms, and the writer dropped nothing. These are point-in-time numbers from development machines, not guarantees for yours.

Other limits:

- Lists default to 50 rows and cap at 200.
- Bulk requests take up to 500 monitors, imports up to 5000 rows or 5 MiB, and restore uploads up to 1 GiB.
- All database writes go through one writer (a 20,000-item queue, committing every 1 s or 500 items).

### For contributors

Read [.github/CONTRIBUTING.md](.github/CONTRIBUTING.md) first. It covers the rules the code follows (single SQLite writer, no secrets in logs, public-page sanitizing, route roles) and the pull request process. New dependencies need an issue first; the Go and npm dependency lists are deliberately short.

```bash
npm --prefix web ci
npm --prefix web run build            # tsc + vite; output goes to internal/webui/dist, which Go embeds
npm --prefix web run test:theme
go vet ./... && go test ./... -count=1
CGO_ENABLED=1 go test -race ./internal/api/ ./internal/hub/ ./internal/app/
go build -o bin/opskest ./cmd/opskest
(cd web && node --experimental-strip-types --test tests/*.test.mjs)   # other Node unit tests
(cd web && npm run test:e2e)          # browser tests; slow
```

`scripts/go` and `scripts/web` run the same tools in Docker. The Go version is the one in `go.mod`, and the web app is built with Node 24. Builds use `CGO_ENABLED=0`; set `CGO_ENABLED=1` only for `-race`.

The end-to-end suite drives headless Chrome or Edge against a freshly built server with temporary data on loopback ports. It skips itself when no browser is found; set `CHROME_PATH` to point at one, or `OPSKEST_E2E_BIN` to reuse a prebuilt binary. It uses fakes for alert delivery and third-party pages, and does not test Docker or reverse-proxy deployments. Optional integration tests run only when `OPSKEST_IT_PG`, `OPSKEST_IT_MYSQL` or `OPSKEST_IT_PG_TLS` (`host:port,user,password,database`) or `GLOBALPING_LIVE` is set. Use throwaway servers for them.

Checking a new install: `bin/opskest healthcheck` exits 0 when `/healthz` answers, which shows the process is up but not that alerts work. Then:

1. Create the administrator.
2. Trigger a controlled failure and recovery, and confirm both messages reach a real destination.
3. Test each channel.
4. View a public page while signed out, and search its JSON for any target, reason or runbook.
5. Confirm that a private page answers 404.
6. Confirm that live updates arrive about once a second through your proxy.
7. Take a backup and rehearse a restore in isolation.

---

## 16. Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| Setup or sign-in answers 403 "cross-site request rejected" | The browser address differs from `APP_URL` in scheme, host or port (for example `127.0.0.1` against `localhost`, or `:8085` against `:8080`). Open `APP_URL` exactly, or fix it and restart. |
| Sign-in does not persist | An `https://` `APP_URL` makes cookies Secure, and browsers do not send those over plain HTTP. Use an `http://` `APP_URL` for local HTTP, and match the browser address. |
| 429 "too many attempts" | Sign-in throttling (15 per user name, 60 per source address, per 10 minutes). Behind a proxy, all users share the proxy's address. Wait it out. |
| Live updates stall or arrive in bursts | The proxy is buffering `/api/stream` or `/api/public/pages/<slug>/stream`. Disable buffering for those two paths and raise the read timeout. |
| Startup stops with "cannot write" | Bind mounts must be owned by uid 65532: `sudo chown -R 65532:65532 data backups`. Or make them writable by the binary's user. |
| Ping monitors fail with permission errors | Unprivileged ICMP needs `net.ipv4.ping_group_range` to include the process group. Compose sets it; on plain Linux set the sysctl. |
| Failing monitors show unknown, not down | The internet-loss guard thinks the server is offline. Allow outbound TCP 53 to `1.1.1.1`, `8.8.8.8` and `208.67.222.222`. |
| `/metrics` answers 404 or 401 | 404 means `METRICS_TOKEN` is unset. 401 means the bearer header is missing or wrong. |
| Opskest exited after a restore | This is by design. Start it again (Compose and the supplied systemd unit do so automatically). |
| A status page answers 404 | Private pages answer 404 when you are signed out, list pages have no `/board`, and the slug may be wrong. |
| Alerts missing overnight | Channel quiet hours drop alerts rather than queueing them. Test ignores quiet hours. |
| A monitor never alerts | Look for a maintenance window, a down parent, pending retries, a third-party feed with no incident, or no channel assigned. |
| `docker compose pull` finds no image | Compose builds `opskest:<VERSION>` locally. Use `docker compose up -d --build`, or publish your own image. |

More symptoms and fixes (proxy errors, port conflicts, key errors, update problems) are in [deploy/DEPLOYMENT.md](deploy/DEPLOYMENT.md#102-troubleshooting).
