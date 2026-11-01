package check

import (
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAppTimeMs(t *testing.T) {
	cases := []struct {
		h    map[string]string
		want int
	}{
		{map[string]string{"Server-Timing": "db;dur=40.2, app;dur=312.7"}, 313},
		{map[string]string{"Server-Timing": "cache;desc=hit, db;dur=12, render;dur=80"}, 80},
		{map[string]string{"X-Envoy-Upstream-Service-Time": "245"}, 245},
		{map[string]string{"X-Response-Time": "123.4ms"}, 123},
		{map[string]string{"X-Response-Time": "0.5s"}, 500},
		{map[string]string{"X-Runtime": "0.087"}, 87},
		{map[string]string{"X-Response-Time": "garbage"}, -1},
		{map[string]string{}, -1},
	}
	for _, c := range cases {
		h := http.Header{}
		for k, v := range c.h {
			h.Set(k, v)
		}
		if got := AppTimeMs(h); got != c.want {
			t.Errorf("%v: got %d, want %d", c.h, got, c.want)
		}
	}
}

func TestEdgeOf(t *testing.T) {
	cases := []struct {
		h    map[string]string
		want string
	}{
		{map[string]string{"Cf-Ray": "8c1a2b3c4d5e6f70-BOM", "Cf-Cache-Status": "DYNAMIC", "Server": "cloudflare"}, "Cloudflare BOM · cache MISS · Cloudflare"},
		{map[string]string{"Server": "awselb/2.0"}, "AWS ALB"},
		{map[string]string{"Server": "nginx/1.25.3"}, "nginx"},
		{map[string]string{"X-Amz-Cf-Pop": "BOM78-P1", "X-Cache": "Hit from cloudfront", "Server": "nginx"}, "CloudFront BOM · cache HIT · nginx"},
		{map[string]string{}, ""},
	}
	for _, c := range cases {
		h := http.Header{}
		for k, v := range c.h {
			h.Set(k, v)
		}
		if got := EdgeOf(h); got != c.want {
			t.Errorf("%v: got %q, want %q", c.h, got, c.want)
		}
	}
}

func TestBodySnippet(t *testing.T) {
	if got := bodySnippet([]byte("<html>\r\n\r\n\r\n<body>502</body>\n</html>\n"), "text/html"); got != "<html>\n\n<body>502</body>\n</html>" {
		t.Errorf("html = %q", got)
	}
	if got := bodySnippet([]byte{0x89, 'P', 'N', 'G', 0, 1}, "image/png"); got != "" {
		t.Errorf("binary = %q", got)
	}
	long := strings.Repeat("é", 400) // 800 bytes
	got := bodySnippet([]byte(long), "application/json")
	if len(got) > 505 || !strings.HasSuffix(got, "…") || !utf8.ValidString(got) {
		t.Errorf("long snippet len=%d valid=%v", len(got), utf8.ValidString(got))
	}
}
