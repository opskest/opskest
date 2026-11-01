package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const goodSHA = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func validManifest() map[string]any {
	return map[string]any{
		"version":     "1.4.0",
		"publishedAt": "2026-11-02T10:00:00Z",
		"notes":       "## Fixes\n- things",
		"minVersion":  "1.0.0",
		"assets": []map[string]any{
			{"os": "linux", "arch": "amd64", "url": "https://example.com/opskest-linux-amd64", "sha256": goodSHA, "size": 123},
			{"os": "windows", "arch": "amd64", "url": "https://example.com/opskest-windows-amd64.exe", "sha256": goodSHA, "size": 456},
		},
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseManifestGood(t *testing.T) {
	m, err := ParseManifest(mustJSON(t, validManifest()))
	if err != nil {
		t.Fatal(err)
	}
	if m.ParsedVersion().String() != "1.4.0" || m.Published().IsZero() || len(m.Assets) != 2 {
		t.Fatalf("parsed wrong: %+v", m)
	}
	// "v" prefix and optional fields.
	if _, err := ParseManifest([]byte(`{"version":"v2.0.0-rc.1","assets":[]}`)); err != nil {
		t.Fatalf("minimal manifest: %v", err)
	}
}

func TestParseManifestRejects(t *testing.T) {
	mut := func(f func(m map[string]any)) []byte {
		m := validManifest()
		f(m)
		return mustJSON(t, m)
	}
	assets := func(m map[string]any) []map[string]any { return m["assets"].([]map[string]any) }
	cases := map[string][]byte{
		"empty":            {},
		"not json":         []byte("<html>"),
		"trailing":         append(mustJSON(t, validManifest()), []byte(` {}`)...),
		"wrong type":       []byte(`{"version":1,"assets":[]}`),
		"no version":       mut(func(m map[string]any) { delete(m, "version") }),
		"dev version":      mut(func(m map[string]any) { m["version"] = "dev" }),
		"bad minVersion":   mut(func(m map[string]any) { m["minVersion"] = "old" }),
		"bad publishedAt":  mut(func(m map[string]any) { m["publishedAt"] = "yesterday" }),
		"notes too long":   mut(func(m map[string]any) { m["notes"] = strings.Repeat("a", MaxNotesBytes+1) }),
		"bad sha length":   mut(func(m map[string]any) { assets(m)[0]["sha256"] = "abc" }),
		"sha not hex":      mut(func(m map[string]any) { assets(m)[0]["sha256"] = strings.Repeat("z", 64) }),
		"negative size":    mut(func(m map[string]any) { assets(m)[0]["size"] = -1 }),
		"no os":            mut(func(m map[string]any) { assets(m)[0]["os"] = "" }),
		"no arch":          mut(func(m map[string]any) { assets(m)[0]["arch"] = "" }),
		"url too long":     mut(func(m map[string]any) { assets(m)[0]["url"] = "https://x/" + strings.Repeat("a", 3000) }),
		"too many assets":  manyAssets(t, MaxAssets+1),
		"bigger than 1MiB": append(mustJSON(t, validManifest()), []byte(strings.Repeat(" ", MaxManifestBytes))...),
	}
	for name, data := range cases {
		if _, err := ParseManifest(data); err == nil {
			t.Errorf("%s: ParseManifest succeeded, want error", name)
		}
	}
}

func manyAssets(t *testing.T, n int) []byte {
	m := validManifest()
	var list []map[string]any
	for i := 0; i < n; i++ {
		list = append(list, map[string]any{"os": "linux", "arch": fmt.Sprintf("a%d", i), "url": "https://x/y", "sha256": goodSHA, "size": 1})
	}
	m["assets"] = list
	return mustJSON(t, m)
}

func TestParseManifestAtLimits(t *testing.T) {
	m := validManifest()
	m["notes"] = strings.Repeat("n", MaxNotesBytes)
	if _, err := ParseManifest(mustJSON(t, m)); err != nil {
		t.Fatalf("notes at the limit rejected: %v", err)
	}
	if _, err := ParseManifest(manyAssets(t, MaxAssets)); err != nil {
		t.Fatalf("assets at the limit rejected: %v", err)
	}
}

func TestSelectAsset(t *testing.T) {
	m, err := ParseManifest(mustJSON(t, validManifest()))
	if err != nil {
		t.Fatal(err)
	}
	if a := m.SelectAsset("linux", "amd64"); a == nil || !strings.HasSuffix(a.URL, "opskest-linux-amd64") {
		t.Fatalf("linux/amd64 = %+v", a)
	}
	if a := m.SelectAsset("windows", "amd64"); a == nil || a.Size != 456 {
		t.Fatalf("windows/amd64 = %+v", a)
	}
	if a := m.SelectAsset("Linux", "AMD64"); a == nil {
		t.Fatal("selection should be case-insensitive")
	}
	for _, p := range [][2]string{{"linux", "arm64"}, {"darwin", "arm64"}, {"windows", "386"}, {"", ""}} {
		if a := m.SelectAsset(p[0], p[1]); a != nil {
			t.Errorf("%v: got %+v, want nil", p, a)
		}
	}
}

func newKeys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func sign(priv ed25519.PrivateKey, data []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data))
}

func TestVerifySignature(t *testing.T) {
	pub, priv := newKeys(t)
	otherPub, otherPriv := newKeys(t)
	data := mustJSON(t, validManifest())
	sig := sign(priv, data)

	if err := VerifySignature(pub, data, sig); err != nil {
		t.Fatalf("good signature rejected: %v", err)
	}
	if err := VerifySignature(pub, data, sig+"\n"); err != nil {
		t.Fatalf("trailing newline in .sig file must be accepted: %v", err)
	}
	// Tampered manifest: one changed byte.
	tampered := append([]byte(nil), data...)
	tampered[len(tampered)/2] ^= 1
	if VerifySignature(pub, tampered, sig) == nil {
		t.Error("tampered manifest accepted")
	}
	// Appended byte.
	if VerifySignature(pub, append(append([]byte(nil), data...), '\n'), sig) == nil {
		t.Error("manifest with an appended byte accepted: signature must cover exact bytes")
	}
	// Signed by someone else.
	if VerifySignature(pub, data, sign(otherPriv, data)) == nil {
		t.Error("signature from another key accepted")
	}
	if VerifySignature(otherPub, data, sig) == nil {
		t.Error("wrong public key accepted")
	}
	for name, bad := range map[string]string{"empty": "", "not base64": "!!!", "short": base64.StdEncoding.EncodeToString([]byte("short"))} {
		if VerifySignature(pub, data, bad) == nil {
			t.Errorf("%s signature accepted", name)
		}
	}
	if VerifySignature(nil, data, sig) == nil {
		t.Error("nil key accepted")
	}
}

func TestParsePublicKey(t *testing.T) {
	pub, _ := newKeys(t)
	b64 := base64.StdEncoding.EncodeToString(pub)
	if _, err := ParsePublicKey(b64); err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePublicKey("  " + b64 + "\n"); err != nil {
		t.Fatalf("whitespace should be trimmed: %v", err)
	}
	for _, bad := range []string{"", "not base64!", base64.StdEncoding.EncodeToString(make([]byte, 31)), base64.StdEncoding.EncodeToString(make([]byte, 64))} {
		if _, err := ParsePublicKey(bad); err == nil {
			t.Errorf("ParsePublicKey(%q) succeeded", bad)
		}
	}
	if fp := KeyFingerprint(b64); len(fp) != 8 {
		t.Errorf("fingerprint = %q", fp)
	}
	if KeyFingerprint("junk") != "" {
		t.Error("fingerprint of junk should be empty")
	}
}

func TestFeedURLRules(t *testing.T) {
	for _, ok := range []string{
		"https://example.com/m.json", "https://github.com/o/r/releases/latest/download/manifest.json?x=1",
		"http://127.0.0.1:8080/m.json", "http://localhost/m.json", "http://[::1]:9/m.json",
	} {
		if err := ValidateFeedURL(ok); err != nil {
			t.Errorf("ValidateFeedURL(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "http://example.com/m.json", "ftp://example.com/m", "file:///etc/passwd", "javascript:alert(1)",
		"https://user:pw@example.com/m.json", "//example.com/m.json", "https:///nohost", "example.com/m.json",
		"http://127.0.0.1.evil.com/m.json", "http://localhost.evil.com/m.json",
	} {
		if err := ValidateFeedURL(bad); err == nil {
			t.Errorf("ValidateFeedURL(%q) accepted", bad)
		}
	}
	// The saved setting follows the same rule; empty is allowed.
	for _, ok := range []string{"", "https://example.com/m.json", "http://127.0.0.1:18990/m.json", "http://localhost/m.json", "http://[::1]:8000/m.json"} {
		if err := ValidateSettingsFeedURL(ok); err != nil {
			t.Errorf("settings must accept %q: %v", ok, err)
		}
	}
	if ValidateSettingsFeedURL("http://example.com/m.json") == nil || ValidateSettingsFeedURL("http://127.0.0.1.evil.com/m.json") == nil {
		t.Error("non-loopback http must be rejected in settings")
	}
}

func TestSigURL(t *testing.T) {
	got, err := withSigSuffix("https://h.example/a/manifest.json?token=abc")
	if err != nil || got != "https://h.example/a/manifest.json.sig?token=abc" {
		t.Fatalf("got %q, %v", got, err)
	}
}
