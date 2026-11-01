# Opskest

**Your services. In sight.**

Opskest is a self-hosted uptime monitoring workspace for teams. It runs
checks against your services, alerts the right people, records outages, lets
your team publish incidents and maintenance, and serves public or private
status pages, all from one Go program with an embedded web interface and a
SQLite database. It exists for teams that want the whole workflow on a server
they control, without running a database server, a message queue or a separate
status page product.

For setup, configuration and every feature in detail, see [Docs.md](Docs.md).

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

Prebuilt binaries are published on the
[Releases](https://github.com/opskest/opskest/releases) page when a version is
tagged. Until the first release, build from source as below. The `main` branch
is the stable one to clone or fork; day-to-day work happens on `develop`.

### Docker Compose

```bash
git clone https://github.com/opskest/opskest.git && cd opskest
mkdir -p data backups && sudo chown -R 65532:65532 data backups
docker compose up -d --build
```

Open <http://localhost:8085> and create the first administrator. The
container runs as uid 65532, so `./data` and `./backups` must be owned by it on
the host before the first start. Compose publishes the app on
`127.0.0.1:8085` only; it never binds ports 80 or 443.

Opskest serves plain HTTP and has no TLS or certificate management. For a
public install, put your host nginx in front to terminate TLS (a sample is in
`deploy/nginx.host.conf`) and set `APP_URL` in a `.env` file to the exact
`https://` address people use. Sign-in and setup are refused with HTTP 403 when
the browser's address differs from `APP_URL`. The proxy must not buffer the two
live-update paths, `/api/stream` and `/api/public/pages/<slug>/stream`.

### Plain binary

```bash
./opskest serve
```

With no configuration it listens on port 8080 and keeps its data in `./data`
and `./backups`. It generates its own encryption key in `data/encryption.key`
on first start; keep that key with your backups, because a database copy
cannot be read without it. Open <http://localhost:8080> (not `127.0.0.1`,
which does not match the default `APP_URL`) and create the first administrator
before sharing the address. Every setting is optional; see `.env.example`.

## How Opskest compares

Five tools are compared below, as of October 2026. Uptime Kuma is the leading
self-hosted open-source tool. UptimeRobot, Better Stack and Pingdom are the
best-known hosted uptime services. Atlassian Statuspage is included as the
standard hosted status page product, although it does not run checks itself
(its monitoring rows are "n/a"). Statuspage is used rather than StatusCake or
Datadog Synthetics because the status page and incident workflow is half of
what Opskest does.

Cells say "n/a" where the vendor's pages did not state something, and "paid
plans only" where a feature exists only above the free tier. Prices change;
check the vendor before deciding.

| | Opskest | Uptime Kuma | UptimeRobot | Better Stack | Pingdom | Atlassian Statuspage |
| --- | --- | --- | --- | --- | --- | --- |
| Self-hosted, data on your server | Yes | Yes | No | No | No | No |
| Hosting and price model | Self-hosted; free to run, you pay for your server | Self-hosted; free | Hosted; free plan, paid plans | Hosted; free plan, paid add-ons and per-seat pricing | Hosted; paid tiers by number of checks, free trial | Hosted; free plan, paid plans by audience size |
| Monitor types | 24, including database queries and group monitors | HTTP, keyword, JSON, TCP, WebSocket, ping, DNS, push, Docker and others | HTTP, keyword, ping, port, DNS, UDP, SSL, domain, heartbeat | Website, API, ping, SSL, domain, DNS, SMTP, POP3, IMAP, heartbeat, Playwright browser checks | Uptime, page speed, transaction (protocols n/a) | None (communication only) |
| Minimum check interval | 10 seconds | 20 seconds | 5 min on the free plan, shorter on paid plans | 30 seconds (3 min on free) | n/a | n/a |
| Alert channels | 24 types | 90+ services | Email, SMS, voice, push, 20+ integrations, webhook | SMS, email, voice, push, Slack, Teams | Email, SMS, push, webhooks | Subscriber email, Slack, Teams; SMS and webhook on paid plans |
| Public status pages | Yes | Yes, multiple, custom domains | Yes; 1 free, more on paid | Yes; 1 free, extras paid | Yes | Yes (core product) |
| Private status pages | Yes (signed-in users only) | n/a | Password protection (plan tier n/a) | Password protection, paid add-on | n/a | Yes, on separate paid plans |
| Incident management | Manual incidents with updates and postmortems; outages automatic | n/a | Incidents with notes and timelines | Yes, with on-call and escalation | Root-cause tools; incident posts n/a | Yes, with templates |
| Maintenance windows | Yes | Yes | Yes | Yes | Yes | Yes |
| Multi-region checks | Optional via Globalping (third party) | n/a | Four regions (plan tier n/a) | Yes | Probes across five continents | n/a |
| Roles and team access | Admin, user, viewer | Single account (multi-user is an open request) | Login and notify-only seats; limits by plan | Team member and responder; RBAC listed | Unlimited users; roles n/a | Team members by plan; RBAC on Business and above |
| 2FA | Optional TOTP with recovery codes | Yes | Listed (plan tier n/a) | Yes | n/a | n/a |
| Open source and licence | Yes, Apache-2.0 | MIT | No | No | No | No |
| API | Session-based REST API, plus public JSON, feeds, badges and push URLs; no API tokens | n/a (Prometheus metrics available) | REST API v3, all plans, rate limited | REST API with tokens | Yes | REST API, all plans |
| SLA and percentile reports | Monthly SLA reports, latency percentiles | n/a | Enterprise only (SLA); email reports on paid plans | Uptime SLA reporting listed | n/a | n/a |

### Where Opskest is weaker

- There is no hosted service. You run, patch, back up and secure it yourself.
- There is no native mobile app and no built-in phone or on-call scheduling.
  Voice and SMS alerting depend on third-party channels such as Twilio,
  PagerDuty or Opsgenie.
- There are fewer integrations than Uptime Kuma, and no SSO or external
  identity provider.
- There are no browser or transaction monitors yet. They would need a browser
  binary that the distroless image does not ship.
- It is a single node with SQLite. The historical load target was 2,000
  monitors on one small server; there is no clustering.
- The API uses browser-style sessions; there are no API tokens.
- Status pages have no custom CSS and no automatic TLS for custom domains.
- The project is new, so there is no large community or ecosystem yet.

### When to choose Opskest, and when not to

Choose Opskest if you want checks, alerts, incidents, maintenance and status
pages in one tool, on your own server, with admin, user and viewer roles and
no per-seat or per-monitor pricing. It suits teams that monitor internal and
public services and cannot, or prefer not to, send that data to a SaaS.

Choose something else if you need a managed service with no operations work,
checks from many regions as a core feature, browser-based transaction
monitoring, phone-call on-call rotation, SSO, or a vendor support contract.
Uptime Kuma is the better fit for a single-person setup that wants the widest
set of notification services. Statuspage is the better fit if you already run monitoring elsewhere
and only need a hosted communication page.

Sources, all read in October 2026: Uptime Kuma
(<https://github.com/louislam/uptime-kuma>, its README, releases page and open
issue #128 on multi-user support); UptimeRobot
(<https://uptimerobot.com/pricing/>, <https://uptimerobot.com/>,
<https://uptimerobot.com/api/>); Better Stack
(<https://betterstack.com/pricing>, <https://betterstack.com/uptime>,
<https://betterstack.com/docs/uptime/api/>); Pingdom
(<https://www.pingdom.com/pricing/>,
<https://www.pingdom.com/product/uptime-monitoring/>); Atlassian Statuspage
(<https://www.atlassian.com/software/statuspage/pricing>,
<https://www.atlassian.com/software/statuspage>).

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
