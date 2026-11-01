// Domain expiry, via RDAP.
//
// RDAP resolves which registry server is authoritative for a TLD using the
// IANA bootstrap registry (https://data.iana.org/rdap/dns.json), then asks
// that server for the domain's expiration event. The bootstrap document is
// cached for 24h since it rarely changes and every http monitor with
// check_domain_expiry consults it once a day.
package check

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ErrNoRDAP means the domain's TLD has no RDAP server in the bootstrap
// registry. Per spec, this is shown as "unknown" and never alerts.
var ErrNoRDAP = errors.New("tld has no rdap server")

// defaultRDAPBootstrapURL is the IANA RDAP bootstrap registry.
const defaultRDAPBootstrapURL = "https://data.iana.org/rdap/dns.json"

// bootstrapCacheTTL is how long the bootstrap document is cached before a
// call to Expiry refetches it.
const bootstrapCacheTTL = 24 * time.Hour

// registrableDomainExceptions are second-level labels that, together with
// a two-letter TLD, mean the registrable domain is the last three labels
// rather than the last two (e.g. "example.com.in").
var registrableDomainExceptions = map[string]bool{
	"co": true, "com": true, "net": true, "org": true, "gov": true, "ac": true, "edu": true,
}

// RDAP resolves domain expiry dates via the IANA RDAP bootstrap registry.
// A single RDAP caches its bootstrap document in memory for 24h, guarded
// by a mutex, so concurrent callers share one fetch.
type RDAP struct {
	client       *http.Client
	bootstrapURL string

	mu        sync.Mutex
	services  map[string]string // tld (lowercase) -> rdap server base URL
	fetchedAt time.Time
}

// NewRDAP returns an RDAP that fetches its bootstrap document with client
// (http.DefaultClient when nil) from bootstrapURL (defaultRDAPBootstrapURL
// when empty).
func NewRDAP(client *http.Client, bootstrapURL string) *RDAP {
	if client == nil {
		client = http.DefaultClient
	}
	if bootstrapURL == "" {
		bootstrapURL = defaultRDAPBootstrapURL
	}
	return &RDAP{client: client, bootstrapURL: bootstrapURL}
}

// Expiry returns the domain expiration date for host's registrable domain.
// It returns ErrNoRDAP when host's TLD has no RDAP server in the bootstrap
// registry.
func (r *RDAP) Expiry(ctx context.Context, host string) (time.Time, error) {
	rd := registrableDomain(host)
	tld := rd[strings.LastIndexByte(rd, '.')+1:]

	services, err := r.servicesMap(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("fetching rdap bootstrap: %w", err)
	}

	server, ok := services[strings.ToLower(tld)]
	if !ok {
		return time.Time{}, ErrNoRDAP
	}
	server = strings.TrimSuffix(server, "/")

	body, err := r.get(ctx, server+"/domain/"+rd)
	if err != nil {
		return time.Time{}, fmt.Errorf("querying rdap server: %w", err)
	}

	var doc struct {
		Events []struct {
			EventAction string `json:"eventAction"`
			EventDate   string `json:"eventDate"`
		} `json:"events"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return time.Time{}, fmt.Errorf("parsing rdap response: %w", err)
	}

	for _, ev := range doc.Events {
		if ev.EventAction == "expiration" {
			t, err := time.Parse(time.RFC3339, ev.EventDate)
			if err != nil {
				return time.Time{}, fmt.Errorf("parsing rdap expiration date: %w", err)
			}
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("rdap response for %s has no expiration event", rd)
}

// servicesMap returns the cached tld -> server map, refetching it when
// missing or older than bootstrapCacheTTL.
func (r *RDAP) servicesMap(ctx context.Context) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.services != nil && time.Since(r.fetchedAt) < bootstrapCacheTTL {
		return r.services, nil
	}

	body, err := r.get(ctx, r.bootstrapURL)
	if err != nil {
		return nil, err
	}

	var doc struct {
		Services [][][]string `json:"services"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parsing rdap bootstrap: %w", err)
	}

	services := make(map[string]string, len(doc.Services))
	for _, entry := range doc.Services {
		if len(entry) < 2 || len(entry[1]) == 0 {
			continue
		}
		server := entry[1][0]
		for _, tld := range entry[0] {
			services[strings.ToLower(tld)] = server
		}
	}

	r.services = services
	r.fetchedAt = time.Now()
	return services, nil
}

// get performs a GET, capped at the same 1 MiB body read limit used for
// checks, and returns the body on a 200 response.
func (r *RDAP) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return body, nil
}

// registrableDomain returns host's registrable domain: the last two
// labels, or the last three when the second-level label is one of
// registrableDomainExceptions and the TLD is two letters (e.g.
// example.com.in).
func registrableDomain(host string) string {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	labels := strings.Split(host, ".")
	if len(labels) <= 2 {
		return host
	}

	tld := labels[len(labels)-1]
	sld := labels[len(labels)-2]
	if len(tld) == 2 && registrableDomainExceptions[sld] {
		return strings.Join(labels[len(labels)-3:], ".")
	}
	return strings.Join(labels[len(labels)-2:], ".")
}
