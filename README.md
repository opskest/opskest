# Opskest

**Your services. In sight.**

[![CI](https://github.com/opskest/opskest/actions/workflows/ci.yml/badge.svg)](https://github.com/opskest/opskest/actions/workflows/ci.yml)
[![Licence: Apache-2.0](https://img.shields.io/badge/licence-Apache--2.0-blue.svg)](LICENSE)

Opskest is a self-hosted uptime monitoring workspace for teams. It runs
checks against your services, alerts the right people, records outages, lets
your team publish incidents and maintenance, and serves public or private
status pages, all from one Go program with an embedded web interface and a
SQLite database. It exists for teams that want the whole workflow on a server
they control, without running a database server, a message queue or a separate
status page product.

![The Opskest overview: what needs a response, scheduled maintenance and recent activity](assets/screenshots/overview-dark.png)

For setup, configuration and every feature in detail, see [Docs.md](Docs.md).

## See it

**Status pages your customers can read.** Branded, with daily history, response
times, incident updates and subscriptions. Groups start collapsed so the page
reads at a glance.

![A public status page: overall status, service groups with 90 days of history and response-time charts](assets/screenshots/status-page.png)

![Touring a public status page: expanding groups and scrolling through the services](assets/screenshots/status-page-tour.gif)

**Know where a check ran from.** New monitors check from your own server.
Worldwide and custom country lists are opt-in, so no hostname leaves your
server unless you choose it.

![Choosing where a monitor is checked from: this server only, worldwide, or a custom list of countries](assets/screenshots/check-locations.gif)

**A wallboard for the wall.** A rotating, no-scroll view that fits a TV, with
problems pinned and an optional alarm.

![A wallboard slideshow cycling through service groups](assets/screenshots/wallboard.gif)

**Internal tools your team lives in.**

| | |
| --- | --- |
| ![Monitor list with state filters and a project sidebar](assets/screenshots/monitors.png) | ![A monitor page with check history, uptime and response-time percentiles](assets/screenshots/monitor-detail.png) |
| Every monitor in one list, filtered by project and state. | Check history, 90-day uptime and response-time percentiles per monitor. |
| ![Incident response: an inbox of open incidents and monitor outages](assets/screenshots/incidents.png) | ![Check regions settings: confirm outages from other countries](assets/screenshots/check-regions.png) |
| Incidents are written by people; outages are recorded automatically. | Optionally confirm an outage from other countries before you page someone. |

## Highlights

**Monitoring**

- 24 monitor types: HTTP(S), WebSocket, SSL certificate, TCP, ping, DNS, UDP,
  NTP, protocol handshakes (SSH, SMTP, Redis, PostgreSQL, MySQL/MariaDB,
  MongoDB, MQTT, AMQP, Memcached, gRPC health, banner), read-only database
  queries and health probes for PostgreSQL and MySQL/MariaDB, push heartbeats,
  group monitors and manual monitors.
- Check intervals from 10 seconds, retries, timeouts, a slow-response
  (degraded) threshold, certificate expiry ladders, domain expiry, and
  assertions on status code, headers, body and JSON paths.
- Projects, tags, parent monitors, templates, bulk actions, and monitor import
  and export.
- Third-party provider status feeds are alert-only: they notify their channels
  but never become outages, uptime figures, incidents or status page
  components.

**Alerting**

- 24 channel types: Slack, Microsoft Teams, Google Chat, Discord, Telegram,
  Mattermost, Rocket.Chat, Matrix, ntfy, Gotify, Pushover, Zulip, Webex,
  Pushbullet, IFTTT, Apprise API, Twilio SMS, Home Assistant, Bark, Signal,
  email (SMTP), signed webhooks, PagerDuty and Opsgenie.
- Per-channel message templates and quiet hours, reminders while down,
  retried and logged deliveries, and one grouped message during widespread
  failures on the channels that support it.

**Incidents and maintenance**

- Outages are detected and recorded automatically. They can be acknowledged
  and annotated, including by read-only viewers.
- Incidents are created manually only, with timelines, public updates and
  postmortems.
- Scheduled maintenance windows suppress alerts while checks continue.

**Status pages and wallboards**

- Public or private status pages with branding, component groups, daily
  history bars, response-time charts, incident history, email and Slack
  subscriptions, RSS and Atom feeds, a JSON summary and SVG badges.
- Wallboards for wall displays, with an optional alarm. A page is one or the
  other from the day it is created.

**Insight**

- Response-time percentiles, response-time alerts and monthly SLA reports
  (JSON or CSV).
- Optional checks of public HTTP, ping and DNS monitors from other countries
  through the Globalping network. This sends hostnames to a third party and is
  controlled in Settings and per monitor.

**Team and security**

- Three roles: admin, user and viewer.
- Local accounts only, with optional authenticator-app two-factor
  authentication and single-use recovery codes. There is no preset account.
- Monitor, channel and two-factor secrets are sealed with an encryption key
  kept alongside your data. Sign-in is throttled and state-changing requests
  carry a CSRF token.

**Operations**

- One binary with the web interface built in, or a Docker image on a
  distroless base. SQLite, with scheduled and on-demand backups and a guided
  restore.
- Live dashboard updates over Server-Sent Events, and optional
  bearer-protected Prometheus metrics.
- Signed in-app updates for plain-binary installs: backup, download, verify,
  swap, restart, with one-click rollback. Nothing is contacted until you set a
  feed URL. Docker installs are notified only.

## Quick start

Every way to run Opskest (Docker Compose, plain Linux with systemd, Windows,
reverse proxy and TLS) is covered step by step in
[deploy/DEPLOYMENT.md](deploy/DEPLOYMENT.md). The short version follows.

Prebuilt binaries and the container image are published with every
[release](https://github.com/opskest/opskest/releases). The `main` branch is
the stable one to clone or fork; day-to-day work happens on `develop`.

### Docker Compose

```bash
git clone https://github.com/opskest/opskest.git && cd opskest
mkdir -p data backups && sudo chown -R 65532:65532 data backups
docker compose up -d
```

Open <http://localhost:8085> and create the first administrator. The
container runs as uid 65532, so `./data` and `./backups` must be owned by it on
the host before the first start. Compose pulls the official image and
publishes the app on `127.0.0.1:8085` only; it never binds ports 80 or 443.
**To change the port** people open (the host port), copy `.env.example` to
`.env` and set `OPSKEST_PORT`, for example `OPSKEST_PORT=9000`, then run
`docker compose up -d` again. The sign-in address follows it
(`http://localhost:9000`). The port inside the container stays 8080; only
`OPSKEST_PORT` changes.

Opskest serves plain HTTP and has no TLS or certificate management. For a
public install, put your host nginx in front to terminate TLS (a sample is in
`deploy/nginx.host.conf`) and set `APP_URL` in a `.env` file to the exact
`https://` address people use. Sign-in and setup are refused with HTTP 403 when
the browser's address differs from `APP_URL`. The proxy must not buffer the two
live-update paths, `/api/stream` and `/api/public/pages/<slug>/stream`.

The image is multi-architecture (amd64 and arm64) and lives on GitHub
Container Registry as `ghcr.io/opskest/opskest`. Upgrade with
`docker compose pull && docker compose up -d`. It shows a notice in Settings,
About & updates when a newer version exists; it never updates itself.

To build the code yourself instead, let the script pick the latest release and
build it. It reports its version and checks for signed updates like the official
image does:

```bash
scripts/compose-build --latest
```

Any other checkout (a branch, or changed files) builds as a development build
that never checks for updates.

### Binary, as a service

Docker Compose is the recommended install. If you prefer a binary, run it as a
service so it starts at boot and keeps running when you close a terminal: a
systemd unit on Linux, or a service wrapper on Windows (both are in
[deploy/DEPLOYMENT.md](deploy/DEPLOYMENT.md)). The binary is also what the
in-app updater installs, with a backup first and one-click rollback.

To try it from a terminal, which stops when you close it:

```bash
./opskest serve
```

With no configuration it listens on port 8080 and keeps its data in `./data`
and `./backups`. It generates its own encryption key in `data/encryption.key`
on first start; keep that key with your backups, because a database copy
cannot be read without it. Open <http://localhost:8080> (not `127.0.0.1`,
which does not match the default `APP_URL`) and create the first administrator
before sharing the address. Every setting is optional; see `.env.example`.

## Why teams switch to Opskest

- **One tool instead of three.** Checks, alerts, incidents, maintenance windows
  and public or private status pages live together. You do not pay for a
  separate status page product or wire a monitor to a page by hand.
- **Your data stays on your server.** One SQLite file and its backups, secrets
  sealed with a key you hold, no third party in the check path. Pricing is the
  cost of a small server, with no per-monitor or per-seat fees.
- **Built for teams, not one login.** Admin, user and viewer roles, authenticator
  app two-factor sign-in that an admin can require for everyone, and viewers who
  can acknowledge and annotate outages without being able to change anything.
- **A real incident workflow.** Outages open and close automatically. Incidents
  are written by people, with timelines, public updates and postmortems, and
  scheduled maintenance silences alerts while checks keep running.
- **Checks that keep up.** Intervals from 10 seconds, 24 monitor types including
  read-only PostgreSQL and MySQL queries, retries and confirmations before an
  alert, and 24 alert channels with templates, quiet hours and reminders.
- **Privacy-first multi-region.** Confirm an outage from other countries through
  Globalping before you wake anyone, but only for the monitors you choose.
- **Safe by default.** No preset accounts, throttled sign-in, CSRF protection,
  signed in-app updates with one-click rollback, and public pages that never
  expose targets or failure reasons, which the tests check.
- **Light to run.** One binary or one distroless container. It was load-tested
  at 2,000 monitors on a small server.

### Switching in an afternoon

1. Start Opskest with Docker Compose or the plain binary, and create the first
   administrator ([quick start](#quick-start)).
2. Paste your URL list into **Settings, Import & export**, or import a CSV or
   JSON file. You see a preview of what will be created before anything is
   written, and importing the same file twice is safe.
3. Add your alert channels once (Slack, Teams, email, PagerDuty and 20 more)
   and attach them to your monitors in bulk.
4. Create a status page in four steps: name, theme, services, publish.
5. Keep your old tool running next to Opskest for a week. Retire it when the
   alerts match.

### How it compares

Cells say "n/a" where the vendor's pages did not state something. Prices change
and plans differ, so check a vendor before deciding. As of October 2026.

| | Opskest | Uptime Kuma | UptimeRobot | Better Stack | Pingdom | Atlassian Statuspage |
| --- | --- | --- | --- | --- | --- | --- |
| Self-hosted, data on your server | Yes | Yes | No | No | No | No |
| Pricing model | Free; you run the server | Free; you run the server | Free plan, paid plans | Free plan, paid add-ons, per seat | Paid tiers by check count | Free plan, paid by audience size |
| Fastest check interval | 10 seconds | 20 seconds | Shorter on paid plans | 30 seconds | n/a | n/a (no checks) |
| Monitor types | 24, incl. database queries | HTTP, TCP, DNS, push and more | HTTP, ping, port, DNS, SSL | Website, API, ping, SSL, heartbeat, browser | Uptime, page speed, transaction | None |
| Alert channels | 24 | 90+ | Email, SMS, voice, push, webhook | SMS, email, voice, Slack, Teams | Email, SMS, push, webhook | Email, Slack, Teams |
| Public and private status pages | Both, built in | Public | Public; private by password | Public; private as add-on | Public | Both, on separate plans |
| Incidents, maintenance, postmortems | Built in | Maintenance | Incidents and notes | Yes, with on-call | n/a | Yes |
| Roles and enforced 2FA | 3 roles, 2FA can be required | Single account | By plan | Yes | n/a | By plan |
| Checks from other countries | Optional, opt-in | n/a | Four regions | Yes | Yes | n/a |
| Wallboard for a TV | Yes | n/a | n/a | n/a | n/a | n/a |
| Open source | Apache-2.0 | MIT | No | No | No | No |

### Where others are ahead

- There is no hosted Opskest. You run, patch, back up and secure it yourself.
- No native mobile app, and no built-in on-call scheduling. Voice and SMS depend
  on channels such as Twilio, PagerDuty or Opsgenie.
- Uptime Kuma has more notification integrations. Opskest has no SSO.
- No browser or transaction monitors yet; they need a browser binary that the
  distroless image does not ship.
- A single node with SQLite, with no clustering.
- The API uses browser-style sessions, so there are no API tokens yet.
- It is new, so the community around it is small. Issues and pull requests help.

Choose something else if you need a managed service with no operations work,
checks from many regions as a core feature, browser-based transaction
monitoring, phone-call on-call rotation, SSO, or a vendor support contract.
Uptime Kuma suits a single-person setup that wants the widest set of
notification services. Statuspage suits a team that already monitors elsewhere
and only needs a hosted communication page.

Sources, read in October 2026: Uptime Kuma (<https://github.com/louislam/uptime-kuma>);
UptimeRobot (<https://uptimerobot.com/pricing/>, <https://uptimerobot.com/api/>);
Better Stack (<https://betterstack.com/pricing>, <https://betterstack.com/uptime>);
Pingdom (<https://www.pingdom.com/pricing/>); Atlassian Statuspage
(<https://www.atlassian.com/software/statuspage/pricing>).

## Licence

Opskest is open source under the [Apache License 2.0](LICENSE). You may use,
modify and redistribute it, including commercially, as long as you keep the
copyright and [NOTICE](NOTICE) files and state your changes. The licence also
grants a patent licence from the contributors. The Opskest name and logo are
not licensed for use as your own product's name.

## Using Opskest in your product or service

If you embed Opskest in a product, bundle it, or run it as part of a paid
service, please credit it and tag the maintainer. It costs you a line and
helps the project a lot:

> Powered by [Opskest](https://github.com/opskest/opskest), created by Manav
> ([github.com/opskest](https://github.com/opskest)).

The licence only requires you to keep the NOTICE file with copies you
distribute, so the credit above is a request, not a condition. A mention in
your docs, footer or release notes, or a post tagging the project, is what
keeps it maintained. If you build something with it, tell us.

## Contributing

Issues, ideas and pull requests are welcome. Read
[.github/CONTRIBUTING.md](.github/CONTRIBUTING.md) first: it covers how to set
up, the rules the code follows, and the sign-off every commit needs.
Report security problems privately, as described in
[.github/SECURITY.md](.github/SECURITY.md), never in a public issue.

## Support Opskest

Opskest is free and stays free. If it saves your team time, sponsoring keeps
it maintained and up to date: security fixes, dependency updates, new monitor
types and better docs. Use the Sponsor button on the GitHub repository.

## Development

Use the versions pinned in `go.mod` (Go 1.27) and the `Dockerfile` (Node 24).
Build the web interface first, because Vite writes to `internal/webui/dist`,
which Go embeds.

```bash
cd web && npm ci && npm run build && cd ..   # runs tsc, then vite build
go build -o bin/opskest ./cmd/opskest
```

Test with:

```bash
go vet ./...
go test ./...
cd web && npm run test:theme
```

`npm run build` also type-checks the interface. The Docker wrappers
`scripts/go` and `scripts/web` run the same commands in containers. Read
[.github/CONTRIBUTING.md](.github/CONTRIBUTING.md) before opening a pull request;
it lists the rules the code follows (one SQLite writer, no secrets in logs, 404
rather than 403 for private resources, and public status pages that never
expose targets or failure reasons).
