package api

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"opskest/internal/alert"
	"opskest/internal/notify"
	"opskest/internal/store"
)

// Subscriber notifications for public status pages: double opt-in email
// (through the page's email channel) or a Slack incoming webhook. Every
// answer to a subscribe request is the same, so the endpoint can't be
// used to learn who is subscribed.

// Notifier is what handlers use to queue subscriber notifications;
// *notify.Notifier in production. nil disables them.
type Notifier interface {
	SendConfirmation(page *store.Page, sub store.Subscriber) bool
	IncidentUpdate(pageIDs []int64, inc notify.Incident)
}

// SlackPoster posts the "you're subscribed" message that proves a webhook
// works; alert.PostJSON in production.
type SlackPoster func(ctx context.Context, url string, payload any) error

const subscribeAccepted = "If that address can receive mail, a confirmation link is on its way. Open it within 48 hours to start getting updates."

// subscribeOptions reports which subscription kinds a page offers.
func subscribeOptions(p *store.Page) (email, slack bool) {
	if !p.SubscriptionsEnabled || p.Visibility != "public" {
		return false, false
	}
	return p.SubscriberChannelID != nil, true
}

func validSlackWebhook(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "hooks.slack.com" && strings.HasPrefix(u.Path, "/services/") && len(raw) <= 300
}

func validEmail(raw string) bool {
	if len(raw) > 254 || strings.ContainsAny(raw, "\r\n") {
		return false
	}
	a, err := mail.ParseAddress(raw)
	return err == nil && a.Address == raw && a.Name == ""
}

// handlePublicSubscribe serves POST /api/public/pages/{slug}/subscribe
// {email} | {slackWebhookUrl}.
func (s *server) handlePublicSubscribe(w http.ResponseWriter, r *http.Request) {
	page, ok := s.resolvePublicPage(w, r)
	if !ok {
		return
	}
	emailOK, slackOK := subscribeOptions(page)
	if !emailOK && !slackOK {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	if !s.subscribeLimit.allow(clientIP(r), s.d.Now()) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	if !isJSON(r) {
		writeErr(w, http.StatusUnsupportedMediaType, "send application/json", nil)
		return
	}
	var in struct {
		Email           string `json:"email"`
		SlackWebhookURL string `json:"slackWebhookUrl"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request", nil)
		return
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.SlackWebhookURL = strings.TrimSpace(in.SlackWebhookURL)
	now := s.d.Now()

	switch {
	case in.Email != "" && in.SlackWebhookURL == "":
		if !emailOK {
			writeErr(w, http.StatusBadRequest, "this page doesn't offer email updates", nil)
			return
		}
		if !validEmail(in.Email) {
			writeErr(w, http.StatusBadRequest, "enter a valid email address", map[string]string{"email": "invalid"})
			return
		}
		sub, send, err := s.d.Store.AddEmailSubscriber(r.Context(), page.ID, in.Email, now)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "subscribe failed", nil)
			return
		}
		if send && s.d.Notify != nil {
			s.d.Notify.SendConfirmation(page, sub)
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "message": subscribeAccepted})

	case in.SlackWebhookURL != "" && in.Email == "":
		if !validSlackWebhook(in.SlackWebhookURL) {
			writeErr(w, http.StatusBadRequest, "use a Slack incoming webhook URL (https://hooks.slack.com/services/…)", map[string]string{"slackWebhookUrl": "invalid"})
			return
		}
		post := s.d.SlackPost
		if post == nil {
			post = alert.PostJSON
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if err := post(ctx, in.SlackWebhookURL, map[string]any{
			"text": "You're subscribed to " + page.Title + " status updates. Incident updates will be posted here.",
		}); err != nil {
			writeErr(w, http.StatusBadRequest, "couldn't post to that webhook — check the URL", nil)
			return
		}
		if err := s.d.Store.AddSlackSubscriber(r.Context(), page.ID, in.SlackWebhookURL, now); err != nil {
			writeErr(w, http.StatusInternalServerError, "subscribe failed", nil)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "message": "Subscribed. A confirmation was posted to that channel."})

	default:
		writeErr(w, http.StatusBadRequest, "send an email or a Slack webhook URL", nil)
	}
}

// isJSON reports whether r declares a JSON body. Requiring it means a
// cross-site form or text/plain POST (no CORS preflight) can't drive these
// session-less endpoints from other people's browsers.
func isJSON(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mt == "application/json"
}

func readToken(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !isJSON(r) {
		writeErr(w, http.StatusUnsupportedMediaType, "send application/json", nil)
		return "", false
	}
	var in struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in); err != nil || len(in.Token) != 64 {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return "", false
	}
	return in.Token, true
}

// handleConfirmSubscription serves POST /api/public/subscriptions/confirm {token}.
func (s *server) handleConfirmSubscription(w http.ResponseWriter, r *http.Request) {
	if !s.publicLimit.allow(clientIP(r), s.d.Now()) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	token, ok := readToken(w, r)
	if !ok {
		return
	}
	if err := s.d.Store.ConfirmSubscriber(r.Context(), token, s.d.Now()); err != nil {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleUnsubscribe serves POST /api/public/subscriptions/unsubscribe {token}.
func (s *server) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if !s.publicLimit.allow(clientIP(r), s.d.Now()) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	token, ok := readToken(w, r)
	if !ok {
		return
	}
	if err := s.d.Store.Unsubscribe(r.Context(), token); err != nil {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ── Admin ──────────────────────────────────────────────────────────

// handleListSubscribers serves GET /api/pages/{id}/subscribers?limit=&offset=.
func (s *server) handleListSubscribers(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	if _, err := s.d.Store.GetPage(r.Context(), id); err != nil {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	confirmed, pending, err := s.d.Store.SubscriberCounts(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	limit := clampLimit(queryInt(r, "limit", 50))
	list, err := s.d.Store.ListSubscribers(r.Context(), id, limit, queryInt(r, "offset", 0))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"confirmed": confirmed, "pending": pending, "limit": limit, "subscribers": list})
}

// handleDeleteSubscriber serves DELETE /api/pages/{id}/subscribers/{sid}.
func (s *server) handleDeleteSubscriber(w http.ResponseWriter, r *http.Request) {
	id, err1 := pathID(r, "id")
	sid, err2 := pathID(r, "sid")
	if err1 != nil || err2 != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	if err := s.d.Store.DeleteSubscriber(r.Context(), id, sid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "delete failed", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// notifyIncident queues subscriber notifications for one public update of
// incident id (reloaded, so the title/status/pages are current).
func (s *server) notifyIncident(ctx context.Context, id int64, body string, at time.Time) {
	if s.d.Notify == nil {
		return
	}
	inc, err := s.d.Store.GetIncident(ctx, id)
	if err != nil || len(inc.PageIDs) == 0 {
		return
	}
	s.d.Notify.IncidentUpdate(inc.PageIDs, notify.Incident{
		ID: inc.ID, Title: inc.Title, Status: inc.Status, Impact: inc.Impact, Body: body, At: at,
	})
}
