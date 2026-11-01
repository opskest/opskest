package api

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"opskest/internal/auth"
	"opskest/internal/store"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9-]{2,48}$`)
var reservedSlugs = map[string]bool{"new": true, "api": true, "admin": true}

var hexColor = regexp.MustCompile(`^#[0-9a-f]{6}$`)

const (
	maxFooterLen   = 200
	maxItemDescLen = 200
)

// validatePageBranding normalises and checks a page's customer-facing
// fields: https-only URLs (a logo is fetched by every visitor's browser,
// and a javascript: or http: link on a public page is never wanted),
// #rrggbb colour, bounded text, and 30/60/90 history days (0 -> 90).
// It returns per-field messages, empty when valid.
func validatePageBranding(p *store.Page) map[string]string {
	errs := map[string]string{}
	for field, v := range map[string]*string{"logoUrl": &p.LogoURL, "websiteUrl": &p.WebsiteURL, "supportUrl": &p.SupportURL} {
		*v = strings.TrimSpace(*v)
		if *v == "" {
			continue
		}
		u, err := url.Parse(*v)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			errs[field] = "must be an https:// URL"
		}
	}
	p.BrandColor = strings.ToLower(strings.TrimSpace(p.BrandColor))
	if p.BrandColor != "" && !hexColor.MatchString(p.BrandColor) {
		errs["brandColor"] = "must be a #rrggbb colour"
	}
	p.FooterText = strings.TrimSpace(p.FooterText)
	if utf8.RuneCountInString(p.FooterText) > maxFooterLen {
		errs["footerText"] = "at most 200 characters"
	}
	switch p.HistoryDays {
	case 0:
		p.HistoryDays = 90
	case 30, 60, 90:
	default:
		errs["historyDays"] = "must be 30, 60 or 90"
	}
	for i := range p.Items {
		p.Items[i].Description = strings.TrimSpace(p.Items[i].Description)
		if utf8.RuneCountInString(p.Items[i].Description) > maxItemDescLen {
			errs["items"] = "component descriptions are at most 200 characters"
		}
	}
	if p.Settings != nil {
		for k, v := range validatePageSettings(p.Settings) {
			errs[k] = v
		}
	}
	return errs
}

// vendorPageItems rejects a 3rd-party feed (Settings.Vendor) listed as a
// component: feeds are alert-only and never appear on a status page.
func (s *server) vendorPageItems(items []store.PageItem) map[string]string {
	for _, it := range items {
		if it.MonitorID != nil && s.d.Engine.IsVendor(*it.MonitorID) {
			return map[string]string{"items": "3rd-party feeds are alert-only and cannot be components of a status page"}
		}
	}
	return nil
}

func (s *server) handleListPages(w http.ResponseWriter, r *http.Request) {
	pages, err := s.d.Store.ListPages(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pages": pages})
}

func (s *server) handleCreatePage(w http.ResponseWriter, r *http.Request) {
	var in store.Page
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if !slugPattern.MatchString(in.Slug) {
		writeErr(w, http.StatusBadRequest, "invalid slug", nil)
		return
	}
	if reservedSlugs[in.Slug] {
		writeErr(w, http.StatusBadRequest, "reserved slug", nil)
		return
	}
	if errs := validatePageBranding(&in); len(errs) > 0 {
		writeErr(w, http.StatusBadRequest, "invalid page settings", errs)
		return
	}
	if errs := s.vendorPageItems(in.Items); len(errs) > 0 {
		writeErr(w, http.StatusBadRequest, "invalid page settings", errs)
		return
	}
	if in.SubscriberChannelID != nil {
		if ch, err := s.d.Store.GetChannel(r.Context(), *in.SubscriberChannelID); err != nil || ch.Type != "email" {
			writeErr(w, http.StatusBadRequest, "invalid page settings", map[string]string{"subscriberChannelId": "pick an email channel"})
			return
		}
	}
	now := s.d.Now().UnixMilli()
	in.CreatedAt = now
	in.UpdatedAt = now

	if err := s.d.Store.CreatePage(r.Context(), &in); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeErr(w, http.StatusConflict, "slug already taken", map[string]string{"slug": "already taken"})
			return
		}
		if errors.Is(err, store.ErrInvalid) {
			writeErr(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "create failed", nil)
		return
	}
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "create", "page", &in.ID, in.Title)
	writeJSON(w, http.StatusCreated, in)
}

func (s *server) handleGetPage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	page, err := s.d.Store.GetPage(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *server) handleUpdatePage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	var in store.Page
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if !slugPattern.MatchString(in.Slug) {
		writeErr(w, http.StatusBadRequest, "invalid slug", nil)
		return
	}
	if reservedSlugs[in.Slug] {
		writeErr(w, http.StatusBadRequest, "reserved slug", nil)
		return
	}
	if errs := validatePageBranding(&in); len(errs) > 0 {
		writeErr(w, http.StatusBadRequest, "invalid page settings", errs)
		return
	}
	if errs := s.vendorPageItems(in.Items); len(errs) > 0 {
		writeErr(w, http.StatusBadRequest, "invalid page settings", errs)
		return
	}
	if in.SubscriberChannelID != nil {
		if ch, err := s.d.Store.GetChannel(r.Context(), *in.SubscriberChannelID); err != nil || ch.Type != "email" {
			writeErr(w, http.StatusBadRequest, "invalid page settings", map[string]string{"subscriberChannelId": "pick an email channel"})
			return
		}
	}
	// The layout (status page or wallboard) is chosen when the page is created
	// and never converted: a client may repeat the current value, or leave it
	// out, but not change it.
	cur, err := s.d.Store.GetPage(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "update failed", nil)
		return
	}
	if in.Layout == "" {
		in.Layout = cur.Layout
	}
	if in.Layout != cur.Layout {
		writeErr(w, http.StatusBadRequest, "a page's layout cannot be changed after it is created", map[string]string{"layout": "fixed when the page is created: this page is a " + cur.Layout + " page"})
		return
	}
	in.ID = id
	in.UpdatedAt = s.d.Now().UnixMilli()

	if err := s.d.Store.UpdatePage(r.Context(), &in); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		if errors.Is(err, store.ErrConflict) {
			writeErr(w, http.StatusConflict, "slug already taken", map[string]string{"slug": "already taken"})
			return
		}
		if errors.Is(err, store.ErrInvalid) {
			writeErr(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "update failed", nil)
		return
	}
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "update", "page", &id, in.Title)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleDeletePage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	if err := s.d.Store.DeletePage(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "delete failed", nil)
		return
	}
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "delete", "page", &id, "")
	w.WriteHeader(http.StatusNoContent)
}
