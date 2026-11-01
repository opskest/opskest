package api

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"opskest/internal/alert"
	"opskest/internal/auth"
	"opskest/internal/config"
	"opskest/internal/engine"
	"opskest/internal/hub"
	"opskest/internal/model"
	"opskest/internal/regions"
	"opskest/internal/scheduler"
	"opskest/internal/store"
	"opskest/internal/update"
	"opskest/internal/webui"
)

// Deps wires all the server's dependencies.
type Deps struct {
	Cfg        *config.Config
	Store      *store.Store
	Writer     *store.Writer
	Engine     *engine.Engine
	Hub        *hub.Hub
	Auth       *auth.Auth
	Alerts     *alert.Dispatcher
	Regions    *regions.Confirmer // nil in tests that don't wire it
	SchedStats func() scheduler.Stats
	Version    string
	Started    time.Time
	Log        *slog.Logger
	Now        func() time.Time
	Checker    func(context.Context, *model.Monitor) model.Result
	// Notify queues status page subscriber notifications (nil = off).
	Notify Notifier
	// SlackPost proves a subscriber's Slack webhook (nil = alert.PostJSON).
	SlackPost SlackPoster
	// Shutdown, when set, is called by a handler that needs to restart the
	// process (a staged database restore): it triggers the same graceful
	// shutdown path as SIGTERM. Tests that never exercise restore leave it
	// nil; handlers that use it (triggerRestart) check for nil first.
	Shutdown func()
	// Updates is the self-update manager (see updates.go). nil = New
	// builds a default one from the other dependencies.
	Updates *update.Manager
}

// server holds the wired state for all API handlers.
type server struct {
	d           Deps
	pushLimit   *pushLimiter
	publicLimit *rateLimiter
	pages       pageCache
	// requireTwoFactor mirrors the workspace setting; it is read on every
	// API request, so it is kept in memory.
	requireTwoFactor atomic.Bool
	// subscribeLimit is the stricter per-IP limit on subscribe requests,
	// on top of the page limit: each one can send an email.
	subscribeLimit *rateLimiter
}

// New builds the full HTTP handler: API routes, auth, security headers,
// webui fallback.
func New(d Deps) http.Handler {
	h, _ := build(d)
	return h
}

// build is New plus the permission table it registered: every API route with
// the minimum role it needs (see permissions.go). The route table test in
// this package checks it against its own, independent classification.
func build(d Deps) (http.Handler, []routePolicy) {
	if d.Updates == nil {
		d.Updates = NewUpdateManager(d)
	}
	s := &server{
		d:              d,
		pushLimit:      newPushLimiter(),
		publicLimit:    newRateLimiter(20, 10*time.Second, 40),
		subscribeLimit: newSlowRateLimiter(5, 10*time.Minute, 5),
	}
	if st, err := d.Store.GetSettings(context.Background()); err == nil {
		s.requireTwoFactor.Store(st.RequireTwoFactor)
	}

	mux := http.NewServeMux()
	var policy []routePolicy
	// reg registers a signed-in route: 401 without a session, 403 below min.
	reg := func(pattern, min string, h http.HandlerFunc) {
		policy = append(policy, routePolicy{Pattern: pattern, Min: min})
		mux.Handle(pattern, auth.RequireRole(min)(h))
	}
	// pub registers a route with no session check (public pages, push
	// tokens, sign-in, health). Each does its own gating, e.g. 404.
	pub := func(pattern string, h http.HandlerFunc) {
		policy = append(policy, routePolicy{Pattern: pattern, Min: rolePublic})
		mux.HandleFunc(pattern, h)
	}
	pub("GET /api/runtime", s.handleRuntime)

	// ── Health ──────────────────────────────────────────────────────────
	pub("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// ── Auth ────────────────────────────────────────────────────────────
	pub("GET /api/auth/status", d.Auth.HandleStatus)
	pub("POST /api/auth/setup", d.Auth.HandleSetup)
	pub("POST /api/auth/login", d.Auth.HandlePasswordLogin)
	reg("GET /api/auth/security", roleViewer, d.Auth.HandleSecurity)
	reg("POST /api/auth/2fa/enroll", roleViewer, d.Auth.HandleEnrollTOTP)
	reg("POST /api/auth/2fa/enable", roleViewer, d.Auth.HandleEnableTOTP)
	reg("POST /api/auth/2fa/disable", roleViewer, s.keepTwoFactor(d.Auth.HandleDisableTOTP))
	reg("POST /api/auth/2fa/recovery", roleViewer, d.Auth.HandleRecoveryCodes)
	reg("POST /api/auth/password", roleViewer, d.Auth.HandleChangePassword)
	pub("POST /api/auth/logout", d.Auth.HandleLogout)
	reg("GET /api/me", roleViewer, s.handleMe)
	reg("PUT /api/me", roleViewer, s.handleUpdateProfile)

	// ── Monitors ────────────────────────────────────────────────────────
	reg("GET /api/monitors", roleViewer, s.handleListMonitors)
	reg("POST /api/monitors", roleUser, s.handleCreateMonitor)
	reg("GET /api/monitors/{id}", roleViewer, s.handleGetMonitor)
	reg("PUT /api/monitors/{id}", roleUser, s.handleUpdateMonitor)
	reg("DELETE /api/monitors/{id}", roleUser, s.handleDeleteMonitor)
	reg("POST /api/monitors/{id}/check", roleUser, s.handleCheckNow)
	reg("POST /api/monitors/{id}/status", roleUser, s.handleSetManualStatus)
	reg("POST /api/monitors/{id}/pause", roleUser, s.handlePauseMonitor)
	reg("POST /api/monitors/{id}/resume", roleUser, s.handleResumeMonitor)
	reg("POST /api/monitors/{id}/duplicate", roleUser, s.handleDuplicateMonitor)
	reg("POST /api/monitors/test", roleUser, s.handleTestMonitor)
	reg("POST /api/monitors/bulk", roleUser, s.handleBulkMonitors)
	reg("POST /api/monitors/dns/resolve", roleUser, s.handleDNSResolve)
	reg("POST /api/monitors/{id}/dns/rotate", roleUser, s.handleDNSRotate)

	// ── History ─────────────────────────────────────────────────────────
	reg("GET /api/monitors/{id}/beats", roleViewer, s.handleBeats)
	reg("GET /api/monitors/{id}/series", roleViewer, s.handleSeries)
	reg("GET /api/monitors/{id}/daily", roleViewer, s.handleDaily)
	reg("GET /api/monitors/{id}/latency", roleViewer, s.handleMonitorLatency)
	reg("GET /api/monitors/{id}/diagnostics", roleViewer, s.handleDiagnostics)
	reg("GET /api/reports/sla", roleViewer, s.handleSLAReport)
	reg("GET /api/monitors/{id}/outages", roleViewer, s.handleMonitorOutages)

	// ── Events ──────────────────────────────────────────────────────────
	reg("GET /api/events", roleViewer, s.handleListEvents)

	// ── Groups and tags ─────────────────────────────────────────────────
	reg("GET /api/groups", roleViewer, s.handleListGroups)
	reg("POST /api/groups", roleUser, s.handleCreateGroup)
	reg("PUT /api/groups/{id}", roleUser, s.handleUpdateGroup)
	reg("DELETE /api/groups/{id}", roleUser, s.handleDeleteGroup)
	reg("POST /api/groups/reorder", roleUser, s.handleReorderGroups)
	reg("GET /api/tags", roleViewer, s.handleListTags)
	reg("POST /api/tags", roleUser, s.handleCreateTag)
	reg("PUT /api/tags/{id}", roleUser, s.handleUpdateTag)
	reg("DELETE /api/tags/{id}", roleUser, s.handleDeleteTag)

	// ── Push ────────────────────────────────────────────────────────────
	pub("GET /push/{token}", s.handlePush)
	pub("POST /push/{token}", s.handlePush)

	// ── Stream and system ───────────────────────────────────────────────
	reg("GET /api/stream", roleViewer, func(w http.ResponseWriter, r *http.Request) {
		d.Hub.ServeAll(w, r)
	})
	reg("GET /api/system", roleAdmin, s.handleSystem)

	// ── Metrics ─────────────────────────────────────────────────────────
	pub("GET /metrics", s.handleMetrics)

	// ── Worldwide & DNS Tools ───────────────────────────────────────────

	// ── Channels ──────────────────────────────────────────────
	reg("GET /api/templates", roleUser, s.handleListTemplates)
	reg("POST /api/templates", roleUser, s.handleCreateTemplate)
	reg("PUT /api/templates/{id}", roleUser, s.handleUpdateTemplate)
	reg("DELETE /api/templates/{id}", roleUser, s.handleDeleteTemplate)
	reg("GET /api/channels", roleViewer, s.handleListChannels)
	reg("POST /api/channels", roleAdmin, s.handleCreateChannel)
	reg("PUT /api/channels/{id}", roleAdmin, s.handleUpdateChannel)
	reg("DELETE /api/channels/{id}", roleAdmin, s.handleDeleteChannel)
	reg("POST /api/channels/{id}/test", roleAdmin, s.handleTestChannel)
	reg("POST /api/channels/{id}/apply-all", roleAdmin, s.handleApplyChannelToAll)

	// ── Outages ─────────────────────────────────────────────────────────
	reg("GET /api/outages", roleViewer, s.handleListOutages)
	reg("GET /api/outages/summary", roleViewer, s.handleOutageSummary)
	reg("POST /api/outages/{id}/ack", roleViewer, s.handleAckOutage)
	reg("POST /api/outages/{id}/note", roleViewer, s.handleNoteOutage)
	reg("GET /api/outages/{id}/notes", roleViewer, s.handleListOutageNotes)
	reg("POST /api/outages/{id}/notes", roleViewer, s.handleAddOutageNote)
	reg("PUT /api/outages/{id}/notes/{noteId}", roleViewer, s.handleEditOutageNote)
	reg("DELETE /api/outages/{id}/notes/{noteId}", roleViewer, s.handleDeleteOutageNote)

	// ── Incidents ────────────────────────────────────────────────────────
	reg("GET /api/incidents", roleViewer, s.handleListIncidents)
	reg("POST /api/incidents", roleUser, s.handleCreateIncident)
	reg("GET /api/incidents/{id}", roleViewer, s.handleGetIncident)
	reg("PUT /api/incidents/{id}", roleUser, s.handleUpdateIncident)
	reg("DELETE /api/incidents/{id}", roleUser, s.handleDeleteIncident)
	reg("POST /api/incidents/{id}/updates", roleUser, s.handleAddIncidentUpdate)
	reg("PUT /api/incidents/{id}/updates/{updateId}", roleUser, s.handleEditIncidentUpdate)
	reg("PUT /api/incidents/{id}/postmortem", roleUser, s.handleSetPostmortem)

	// ── Maintenance ─────────────────────────────────────────────────────
	reg("GET /api/maintenance", roleViewer, s.handleListMaintenance)
	reg("POST /api/maintenance", roleUser, s.handleCreateMaintenance)
	reg("PUT /api/maintenance/{id}", roleUser, s.handleUpdateMaintenance)
	reg("DELETE /api/maintenance/{id}", roleUser, s.handleDeleteMaintenance)

	// ── Pages ───────────────────────────────────────────────────────────
	reg("GET /api/pages", roleUser, s.handleListPages)
	reg("POST /api/pages", roleUser, s.handleCreatePage)
	reg("GET /api/pages/{id}", roleUser, s.handleGetPage)
	reg("PUT /api/pages/{id}", roleUser, s.handleUpdatePage)
	reg("DELETE /api/pages/{id}", roleUser, s.handleDeletePage)

	// ── Public pages ────────────────────────────────────────────────────
	pub("GET /api/public/pages/{slug}", s.handlePublicPage)
	pub("GET /api/public/pages/{slug}/stream", s.handlePublicStream)
	pub("GET /api/public/pages/{slug}/day", s.handlePublicDay)
	pub("GET /api/public/pages/{slug}/incidents", s.handlePublicIncidents)
	pub("GET /api/public/pages/{slug}/incidents/{id}", s.handlePublicIncident)
	pub("GET /api/public/pages/{slug}/latency", s.handlePublicLatency)
	pub("GET /api/public/pages/{slug}/feed.atom", s.handlePublicFeed)
	pub("GET /api/public/pages/{slug}/feed.rss", s.handlePublicRSS)
	pub("GET /api/public/pages/{slug}/summary.json", s.handlePublicSummary)
	pub("GET /api/public/pages/{slug}/badge.svg", s.handlePublicBadge)
	pub("GET /api/public/pages/{slug}/services/{id}/badge.svg", s.handlePublicServiceBadge)
	pub("GET /api/public/pages/{slug}/services/{id}/uptime.svg", s.handlePublicUptimeBadge)
	pub("POST /api/public/pages/{slug}/subscribe", s.handlePublicSubscribe)
	pub("POST /api/public/subscriptions/confirm", s.handleConfirmSubscription)
	pub("POST /api/public/subscriptions/unsubscribe", s.handleUnsubscribe)
	reg("GET /api/pages/{id}/subscribers", roleUser, s.handleListSubscribers)
	reg("DELETE /api/pages/{id}/subscribers/{sid}", roleUser, s.handleDeleteSubscriber)

	// ── Users ───────────────────────────────────────────────────────────
	reg("GET /api/users", roleAdmin, s.handleListUsers)
	reg("PUT /api/users/{id}", roleAdmin, s.handleUpdateUser)
	reg("POST /api/users", roleAdmin, s.handleCreateUser)
	reg("DELETE /api/users/{id}", roleAdmin, s.handleDeleteUser)
	reg("DELETE /api/users/{id}/2fa", roleAdmin, s.handleResetTwoFactor)

	// ── Settings and logs ───────────────────────────────────────────────
	reg("GET /api/settings", roleAdmin, s.handleGetSettings)
	reg("PUT /api/settings", roleAdmin, s.handlePutSettings)
	reg("GET /api/alert-log", roleAdmin, s.handleAlertLog)
	reg("GET /api/regions", roleAdmin, s.handleRegions)
	reg("POST /api/regions/check", roleAdmin, s.handleRegionCheck)
	reg("PUT /api/regions/token", roleAdmin, s.handleRegionToken)
	reg("GET /api/regions/issues", roleViewer, s.handleRegionIssues)
	reg("GET /api/monitors/{id}/regions", roleViewer, s.handleMonitorRegions)
	reg("POST /api/monitors/{id}/regions/check", roleUser, s.handleMonitorRegionCheck)
	reg("POST /api/alert-log/{id}/resend", roleAdmin, s.handleResendAlert)
	reg("GET /api/data/usage", roleAdmin, s.handleDataUsage)
	reg("POST /api/data/clear", roleAdmin, s.handleClearData)
	reg("POST /api/data/compact", roleAdmin, s.handleCompact)
	reg("GET /api/audit-log", roleAdmin, s.handleAuditLog)

	// ── Backups & restore ───────────────────────────────────────────────
	reg("GET /api/backups", roleAdmin, s.handleListBackups)
	reg("POST /api/backups", roleAdmin, s.handleRunBackupNow)
	reg("POST /api/backups/upload", roleAdmin, s.handleUploadRestore)
	reg("GET /api/backups/{name}/download", roleAdmin, s.handleDownloadBackup)
	reg("DELETE /api/backups/{name}", roleAdmin, s.handleDeleteBackup)
	reg("POST /api/backups/{name}/restore", roleAdmin, s.handleRestoreBackup)

	// ── Bulk export / import of monitors ─────────────────────────────────
	reg("GET /api/export/monitors", roleUser, s.handleExportMonitors)
	reg("GET /api/export/template.csv", roleUser, s.handleExportTemplateCSV)
	reg("POST /api/import/monitors", roleUser, s.handleImportMonitors)

	// ── Updates (admin only) ─────────────────────────────────────────────
	reg("GET /api/updates", roleAdmin, s.handleGetUpdates)
	reg("PUT /api/updates/settings", roleAdmin, s.handlePutUpdateSettings)
	reg("POST /api/updates/check", roleAdmin, s.handleUpdateCheck)
	reg("POST /api/updates/install", roleAdmin, s.handleUpdateInstall)
	reg("POST /api/updates/rollback", roleAdmin, s.handleUpdateRollback)

	// ── Web UI fallback ─────────────────────────────────────────────────
	mux.Handle("/", webui.Handler())
	// A status page's layout is fixed when it is created: only a board page
	// has a wallboard. The shell still loads (the client draws the not-found
	// state) but the response is a real 404, like an unknown page.
	mux.Handle("GET /status/{slug}/board", s.boardGate(webui.Handler()))

	// Wrap with security headers and auth middleware.
	var handler http.Handler = s.twoFactorGate(mux)
	handler = d.Auth.Middleware(handler)
	handler = s.robotsPolicy(handler)
	handler = securityHeaders(handler)
	handler = gzipResponses(handler)

	return handler, policy
}

// securityHeaders sets CSP and security headers on every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// Spec §15, plus font-src 'self' data: for the bundled fonts, which
		// ship as data: URIs (@fontsource): default-src blocks them
		// otherwise, and there is no separate font-src fallback to
		// default-src once one is declared. img-src allows any https: host
		// because a customer-facing status page shows its own logo from
		// wherever the admin hosts it (validated https-only in pages.go);
		// scripts, styles and connections stay same-origin.
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}
