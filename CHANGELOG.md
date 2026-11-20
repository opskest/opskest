# Changelog

Each release is described here. The text under a version heading becomes the
GitHub release notes and the notes shown in Settings, About & updates.

## 1.0.5 (2026-11-20)

### Security

- Rebuilt with Go 1.27.4, which fixes a vulnerability in the Go standard library's `net/http` package (GO-2026-6859). There are no other changes since 1.0.4.

## 1.0.4 (2026-11-20)

### Status pages

- Every status page and wallboard now has an "Opskest credit" switch (Appearance). It is on by default, so existing pages look the same. Turn it off to remove "Powered by Opskest", the Opskest mark and the Opskest tab icon, leaving only your own logo, favicon and title. Leaving it on helps the project.
- Pages can set their own favicon, separate from the logo.
- The wallboard shows the page's logo in place of the Opskest brand.

### Updates

- The update notice on Overview now lists the first few changes in the new release, with a link to the full notes.
- Settings, About & updates keeps the notes of the version you are running, so you can see what your last update changed.

### Maintenance

- Updated dependencies: SQLite driver 1.62.0, PostgreSQL driver 5.13.0, gjson 1.20.2, golang.org/x/sys 0.50.0 and Vite 6.4.6.

## 1.0.3 (2026-11-03)

### Monitoring

- Request timing on a monitor's Diagnostics tab is now a waterfall: each layer starts where the previous one ended, with a dashed outline for its usual time, and a strip of recent checks to inspect an earlier one.
- Time spent outside the final request, such as redirects, shows as its own layer, so the layers add up to the check's total.
- Response time leads with the median and a fastest-to-slowest spread bar, with p90, p95, p99 and the average beside it.

### Operations

- The documentation and `.env.example` now say how to change the host port: set `OPSKEST_PORT` in `.env`.

## 1.0.2 (2026-11-02)

First public release of Opskest, a self-hosted uptime monitoring workspace for teams.

### Monitoring

- 24 monitor types: HTTP(S), WebSocket, SSL certificate, TCP, ping, DNS, UDP, NTP, protocol handshakes (SSH, SMTP, Redis, PostgreSQL, MySQL, MongoDB, MQTT, AMQP, Memcached, gRPC, banner), read-only PostgreSQL and MySQL queries, push heartbeats, groups and manual monitors.
- Intervals from 10 seconds, retries, degraded thresholds, certificate and domain expiry warnings, and assertions on status, headers, body and JSON.
- New monitors check from this server only. Worldwide and custom country checks through Globalping are opt-in.
- Projects, tags, templates, bulk actions, and import and export.
- 99 third-party provider status feeds, alert-only: they notify but never become outages, uptime or incidents.

### Alerting

- 24 channel types, including Slack, Teams, Discord, Telegram, email, signed webhooks, PagerDuty and Opsgenie.
- Message templates, quiet hours, reminders, grouped messages during widespread failures, and logged retries.

### Incidents and maintenance

- Outages are recorded automatically and can be acknowledged and annotated, including by read-only viewers.
- Incidents are written by people, with timelines, public updates and postmortems.
- Scheduled maintenance windows silence alerts while checks continue.

### Status pages and wallboards

- Public or private status pages with branding, component groups, daily history, response-time charts, subscriptions, RSS and Atom feeds and SVG badges.
- Wallboards for wall displays, with a rotating no-scroll layout and an optional alarm.
- A four-step page creation flow: name, theme, services, publish.

### Team and security

- Three roles: admin, user and viewer.
- Local accounts with authenticator-app two-factor sign-in, recovery codes and a QR code for setup. Administrators can require two-factor for everyone and reset a lost device.
- Secrets sealed with a key kept beside your data, throttled sign-in, CSRF protection, and public pages that never expose targets or failure reasons.

### Operations

- One binary or one distroless container, with SQLite, scheduled and manual backups, and a guided restore.
- Docker Compose runs the official multi-architecture image (amd64 and arm64) from `ghcr.io/opskest/opskest`. Set the published port with `OPSKEST_PORT` and the default sign-in address follows it.
- Signed in-app updates for plain-binary installs, with backup, verification and one-click rollback. Docker installs are notified only, with the command to run.
- Building from source on a release tag (`scripts/compose-build --latest`, `scripts/build-release --latest`) gives a release build with the project key and feed built in, so it checks for signed updates too. Any other checkout is a development build.
- Responses are compressed, live streams stay light for many viewers, and Prometheus metrics are available behind a bearer token.

### Known limits

- No hosted service, mobile app, SSO, API tokens, or browser and transaction monitors yet.
- Ping on Windows, the Windows service wrapper, and the Apache and Caddy proxy examples are untested.
