# Opskest — implementation guidance

Opskest is a self-hosted uptime monitoring workspace for teams. Read `README.md` first for what it does and how to run
it. This file records the things that are easy to get wrong, and why the code
is the way it is.

## Toolchain

**Go and Node may run locally; Docker wrappers provide a reproducible toolchain.**
`scripts/go` and `scripts/web` wrap `docker run` with named volumes for the
module cache / npm cache, so repeat runs are fast:

```bash
scripts/go test ./...
scripts/go vet ./...
CGO_ENABLED=1 scripts/go test -race ./internal/api/ ./internal/hub/ ./internal/app/
scripts/web npm run build
```

Go image `golang:1.27.3-bookworm`, module path `opskest`. Builds use
`CGO_ENABLED=0` (the runtime image is distroless static); race tests need
`CGO_ENABLED=1` because `modernc.org/sqlite` is pure Go but the race detector
itself needs cgo's runtime support on some platforms — set it only for `-race`
runs, never for the production build.

**Dependency allowlists are closed.** Go:
`modernc.org/sqlite`,
`github.com/prometheus-community/pro-bing`, `github.com/coder/websocket`,
`github.com/tidwall/gjson`, `github.com/jackc/pgx/v5` and
`github.com/go-sql-driver/mysql` (+ `github.com/miekg/dns` test-only). The
owner approved pgx v5 and go-sql-driver/mysql for the database monitors
(`postgres_sql`, `mysql_sql`) only; use them nowhere else. The Google OIDC and
oauth2 dependencies (`go-oidc`, `golang.org/x/oauth2`) were removed with Google
sign-in and must not come back. `golang.org/x/sys` is also allowed, for the
Windows time zone lookup only (`internal/config/timezone_windows.go`); that code
predates this note and was already using it, so it is recorded as approved and
limited to that use.
Web: React 18, wouter and the `@fontsource` font packages in `web/package.json`
at runtime; Vite/TypeScript/Tailwind/PostCSS/autoprefixer dev-only. Adding
anything else needs a ledger ruling, not a quiet `go get`.

## Namespacing — non-negotiable

Opskest can coexist with other services. Every shared-namespace identifier
here is `opskest`-prefixed:

- compose project `opskest`, service `opskest`, container `opskest-app`
- cookies `opskest.sid` / `opskest.csrf`, header `X-CSRF-Token`
- `localStorage` key `opskest-theme`, `BroadcastChannel` `opskest_auth`
- **the stack binds `127.0.0.1:8085` and nothing else** (`OPSKEST_BIND`). The
  host nginx owns 80/443 for every tool on the box and certbot manages
  certificates there. Never add an 80 or 443 binding here, and never give
  this stack its own ACME client.
- host directories `./data` and `./backups`, both bind mounts (not named
  volumes) owned by uid 65532 — see "uid 65532" below.

## Things that will bite you

**Third-party feeds are alert-only: never outages, uptime, incidents or
status-page components.** A monitor with `settings.vendor` still updates its own
status and alerts its channels (`engine.vendorDown`/`vendorRecovered`, no outage
row, `OutageID` 0), but writes no rollups (`Heartbeat.NoRollup`), and is kept out
of clusters, groups, dependencies, maintenance, reports, metrics and
`MonitorIDsInGroup`. The API rejects it as a page component, dependency, group
child, maintenance target or incident monitor. Keep it that way when adding a
new list, count or export.

**There is exactly one writer to SQLite.** `store.Writer` owns a single
goroutine and a 20,000-item queue; heartbeats, outage transitions and audit
rows all funnel through it and commit in batches (1s or 500 items, whichever
first). Adding a second write path (a handler that calls `db.Exec` directly
instead of going through the writer or a synchronous `store.*` method that
already serializes correctly) reintroduces `SQLITE_BUSY` under load — this is
a central architectural constraint for reliable SQLite operation under load. If you're tempted to write from a new goroutine, route it through the
writer instead.

**Rollup latency histograms merge in SQL.** `rollup_hourly`/`rollup_daily`
carry `lat_hist`, a sparse log-bucket histogram (`internal/latency`). The
writer's upsert merges it with `opskest_hist_merge`, a Go scalar function
registered in `store/histfunc.go`'s `init()`. A second process or tool that
writes rollups through plain `sqlite3` won't have that function and will
fail on the upsert, so keep rollup writes inside the writer. Percentiles
read from a NULL or corrupt blob come back as "no data", never 0.

**Alert holds read a leaf-locked set, never another entry.** `engine.downParent`
reads `e.parentDown` under `affMu`, which is kept in step by `setAffected`. A
monitor's dependency check must not take another entry's `pub`/`mu`: two
monitors that depend on each other's state would deadlock. Response-time
alerts (`internal/latalert`) only read the engine's rings and rollups, and
publish straight to the dispatcher. Keep them off the check path.

**Subscriber endpoints must not reveal who is subscribed.** `POST
/api/public/pages/{slug}/subscribe` answers every email with the same 202.
Confirm and unsubscribe take an opaque 64-hex token and return 404
otherwise. Never log subscriber addresses or Slack webhook URLs. The
webhooks are sealed with the secret box, like channel configs.

**Never log secrets, payloads, monitor settings, headers or channel
configs.** Use `logx.Err(log, msg, err)` everywhere — it logs only the error
string, trimmed to 300 bytes on a rune boundary. A monitor's bearer token, a
channel's webhook URL (which often embeds its own secret) and a raw request
body must never reach stdout, because the `json-file` log driver retains it
indefinitely.

**Public status pages must never leak target, reason, runbook or tag data.**
`hub.Sanitize`, the public DTOs (`publicIncident`, `publicMaintenance`,
`publicLatencyPoint`) and the `/api/public/pages/*` handlers are the only place this
is enforced — any new field added to a monitor's public representation must
go through there, not be bolted onto the private DTO and shipped as-is.
`TestPublicPageNeverLeaks` (internal/api) checks this by byte-searching the
JSON body *and* the first SSE snapshot for a deliberately sensitive target/
reason; `TestPublicEndpointsNeverLeak` does the same for every other public
endpoint (incidents, incident permalink, latency, Atom feed, the three status
badges in `Docs.md ("Status pages and wallboards")`), including an
internal-only incident update. A new public endpoint gets added to that list.
A change that breaks sanitization fails loudly there rather than quietly in
production.

**List endpoints stay bounded.** Default limit 50, max 200, enforced by
`clampLimit` — a beats/outages/events query that ignores the cap on 2,000
monitors' worth of history is how a single API response goes from "instant"
to "the proxy gives up before it arrives." `TestListsBounded` seeds 250 rows
and checks a `limit=1000` request still comes back at 200.

**404, never 403, for anything private or unknown.** A private status page
with no session, an unknown push token, an unknown monitor id — all answer
404. A 403 confirms the resource exists, which is itself information a
public-facing endpoint (the push endpoint especially — no auth at all) must
not hand out.

**Three roles: admin, user, viewer.** Rank admin > user > viewer
(`internal/auth/roles.go`); every route is registered through `reg(pattern,
minRole, handler)` in `internal/api/server.go`, and every new route must be
classified in the `routeMatrix` of `internal/api/roles_test.go` (and
`Docs.md ("Roles and team")`). Admin-only data (channel configs, settings, users, logs,
backups) never goes to a lower role; viewers keep only the annotation writes.

**Local accounts are the supported authentication path.** First-run setup
creates the initial administrator exactly once, using the normal public setup
flow and an atomic store operation. Do not seed credentials or introduce a
development-login bypass. Passwords must be securely hashed, never stored or
logged in plaintext. Optional authenticator-based two-factor authentication
uses encrypted TOTP secrets and single-use recovery codes. Never log password,
TOTP, or recovery-code payloads. Preserve setup concurrency, login throttling,
CSRF, session, and last-admin protections when changing authentication.


**Nothing is required to start, and the key lives in the data dir.** With no
`.env`, Opskest serves on :8080 using `./data`. If `SECRET_ENCRYPTION_KEY` is
unset, `config.ResolveSecretKey` creates `DATA_DIR/encryption.key` once and reuses
it; a corrupt key file is an error, never silently replaced, because replacing it
makes every sealed secret unreadable. `.env` is read by `cmd/opskest/dotenv.go`
and never overrides real environment variables.

**Service monitors (`internal/check/services.go`, `services_more.go`) stay
handshake-only.** They speak just enough protocol to prove the server answers
(Postgres SSLRequest, MySQL greeting, MongoDB isMaster, MQTT CONNECT, ...), use
only the standard library, and never put a password or a server reply into a
failure reason. Redis and MQTT reuse `Settings.BasicUser` and
`Secrets.BasicPass`. Logging in to a database is a different monitor type; see
"Database monitors" below. The `postgres` and `mysql` service types never log
in.

**Database monitors (`internal/check/sqlmon.go`, types `postgres_sql` and
`mysql_sql`).** These are the only checks that authenticate and run SQL.

- Everything runs inside a read-only transaction
  (`BeginTx` with `ReadOnly: true`); PostgreSQL sessions also set
  `default_transaction_read_only = on`. One connection, opened and closed per
  check.
- A custom query is a single statement and must pass `model.QueryProblem`
  (starts with SELECT, WITH, SHOW, EXPLAIN, VALUES or TABLE; at most 4000
  characters; no second statement). The check re-validates it at run time. Do
  not weaken the rule or bypass it for built-in probes that take user input.
- Query text, result rows and credentials must never appear in a failure
  reason or a log line. Reasons carry counts and durations; a compared value is
  echoed only when it is a number. Driver errors go through `sqlFail`, which
  reduces them to the error code and the first line of the server message.
- The deadlock baseline is held in memory per monitor id inside the checker. It
  is not persisted. The first check after a start records the baseline and
  cannot alert, and a counter that goes down is treated as a reset. Do not
  move it into SQLite without going through the single writer.
- The password is `Secrets.BasicPass`, sealed like every other monitor secret.
  Monitor templates strip secrets but keep non-secret settings, including the
  query text; see `internal/api/templates.go`.

**`net.ipv4.ping_group_range` is not optional.** Without it, ping monitors
need `NET_RAW`, which the distroless nonroot image should not be granted.
`docker-compose.yml` sets `sysctls: net.ipv4.ping_group_range: "0
2147483647"` so unprivileged ICMP works instead. Dropping this line silently
breaks every ping monitor with a permission error that has nothing to do with
the monitor's own config.

**SSE needs `proxy_buffering off` and a long read timeout, on exactly two
paths.** `/api/stream` (the live dashboard) and `/api/public/pages/*/stream`
(public status pages) are long-lived responses that get flushed once a
second; anything that buffers them (nginx's default, most CDNs) turns "live"
into "once a minute or never." `deploy/nginx.host.conf` matches both paths
with one regex location block ahead of the general proxy_pass. An ALB needs
its idle timeout raised to ≥ 60s instead (the SSE `: ping` every 15s keeps it
alive under that).

**The bind mounts are uid 65532, not root.** The runtime image is
`gcr.io/distroless/static-debian12:nonroot`, which runs as uid 65532 with no
shell to `chown` from inside the container. `./data` and `./backups` must be
`chown -R 65532:65532`'d on the *host* before the first `docker compose up`.
`internal/app.checkWritable` checks this at boot (before `store.Open`, so the
failure is a clear one-line message naming the exact command, not a buried
SQLite "unable to open database file") — if you're touching startup wiring,
keep that check ahead of anything that opens the database.

**Distroless has no shell, curl or wget.** The compose healthcheck runs
`/opskest healthcheck`, a Go subcommand that GETs `/healthz` itself — there
is nothing else in the image capable of doing that. If you add a startup
dependency the healthcheck should account for, extend `runHealthcheck` in
`cmd/opskest/main.go`, not a shell one-liner.

**`APP_URL` controls secure-cookie behavior.** Use
`APP_URL=http://localhost:8085` for private local testing without TLS. For
production, use the actual HTTPS origin. Opskest does not read `X-Forwarded-Proto`
or `Host` from a proxy: the `APP_URL` scheme decides whether cookies are Secure,
and sign-in and setup are refused (403) when the browser's origin differs from
`APP_URL` (the loopback spellings localhost, 127.0.0.1 and [::1] count as one
when `APP_URL` is itself a loopback address). A Secure cookie is not sent over
plain HTTP. Complete first-admin setup before allowing untrusted access to the
new installation.


## Load harness

The historical test target is 2,000 monitors at a 30s interval with p99 lag
< 1s, RSS < 150MB and < 0.5 vCPU (spec §2, S1-S5). A
change to `internal/engine`, `internal/scheduler` or `internal/store/writer.go`
that might affect any of those should be checked against the load harness
before it lands, not assumed safe:

```bash
docker run --rm -v "$PWD":/src -w /src -v opskest-gomod:/go/pkg/mod -v opskest-gocache:/root/.cache/go-build \
  --cpus 2 --memory 1g golang:1.27.3-bookworm go run ./tests/loadtest -monitors 2000 -interval 30s -duration 5m
```

Results so far are recorded in `Docs.md ("Performance and limits")`, with the date and host they
were measured on — append to it rather than overwriting, so a regression is
visible against history.
