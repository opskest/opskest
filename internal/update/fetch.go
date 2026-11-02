package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const maxRedirects = 3

// newHTTPClient builds the client used for the feed and the download. Every
// hop, including redirects, must satisfy the https rule, and at most
// maxRedirects are followed. No cookies, no credentials.
func newHTTPClient() *http.Client {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		MaxIdleConns:          2,
		IdleConnTimeout:       30 * time.Second,
	}
	return &http.Client{Transport: tr, CheckRedirect: checkRedirect}
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > maxRedirects {
		return errors.New("too many redirects")
	}
	if !allowedScheme(req.URL) {
		return errors.New("redirect to a non-https location refused")
	}
	return nil
}

// sanitizeErr strips the request URL (which may carry a token in its query
// string) from net/http errors so it never reaches logs or the UI.
func sanitizeErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		inner := ue.Err
		if errors.Is(inner, context.DeadlineExceeded) {
			return errors.New("request timed out")
		}
		if errors.Is(inner, context.Canceled) {
			return context.Canceled
		}
		return errors.New(inner.Error())
	}
	return err
}

// fetchLimited GETs rawURL and returns at most limit bytes. A body longer
// than limit is an error, not a truncation.
func fetchLimited(ctx context.Context, c *http.Client, rawURL, userAgent string, limit int64, timeout time.Duration) ([]byte, error) {
	if _, err := parseFetchURL(rawURL); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, errors.New("URL is not valid")
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	resp, err := c.Do(req)
	if err != nil {
		return nil, sanitizeErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server answered HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return nil, fmt.Errorf("response is larger than %d bytes", limit)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, sanitizeErr(err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("response is larger than %d bytes", limit)
	}
	return b, nil
}

// withSigSuffix returns the signature URL for a feed URL: ".sig" appended
// to the path, query string preserved.
func withSigSuffix(feed string) (string, error) {
	u, err := url.Parse(feed)
	if err != nil {
		return "", err
	}
	u.Path += ".sig"
	if u.RawPath != "" {
		u.RawPath += ".sig"
	}
	return u.String(), nil
}
