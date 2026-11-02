# Changelog

Each release is described here. The text under a version heading becomes the
GitHub release notes and the notes shown in Settings, About & updates.

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
