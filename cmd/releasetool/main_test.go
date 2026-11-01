package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"opskest/internal/update"
)

var fixedNow = func() time.Time { return time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC) }

func runTool(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(args, &o, &e, fixedNow)
	return code, o.String(), e.String()
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestKeygenWritesKeysAndRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "release")
	code, out, errOut := runTool(t, "keygen", "-out", base)
	if code != 0 {
		t.Fatalf("keygen = %d %s", code, errOut)
	}
	priv, err := os.ReadFile(base + ".key")
	if err != nil {
		t.Fatal(err)
	}
	pub, err := os.ReadFile(base + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(base + ".key")
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("private key mode = %v, want 0600", fi.Mode().Perm())
		}
	}
	// The private key is never printed, only the public one.
	privText := strings.TrimSpace(string(priv))
	if strings.Contains(out, privText) || strings.Contains(errOut, privText) {
		t.Fatal("keygen printed the private key")
	}
	if !strings.Contains(out, strings.TrimSpace(string(pub))) {
		t.Fatal("keygen should print the public key")
	}
	if _, err := update.ParsePublicKey(string(pub)); err != nil {
		t.Fatalf("release.pub is not a valid public key: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(privText)
	if err != nil || len(raw) != 64 {
		t.Fatalf("release.key is not a base64 64-byte ed25519 key: %v len=%d", err, len(raw))
	}

	// A second run refuses and leaves both files untouched.
	code, _, errOut = runTool(t, "keygen", "-out", base)
	if code == 0 || !strings.Contains(errOut, "already exists") {
		t.Fatalf("second keygen = %d %q", code, errOut)
	}
	again, _ := os.ReadFile(base + ".key")
	if !bytes.Equal(again, priv) {
		t.Fatal("existing private key was modified")
	}
	// Even if only one of the two exists.
	os.Remove(base + ".pub")
	if code, _, _ := runTool(t, "keygen", "-out", base); code == 0 {
		t.Fatal("keygen overwrote an existing private key")
	}
	if again, _ := os.ReadFile(base + ".key"); !bytes.Equal(again, priv) {
		t.Fatal("existing private key was modified")
	}
}

func buildDist(t *testing.T) string {
	t.Helper()
	dist := t.TempDir()
	writeFile(t, filepath.Join(dist, "opskest-linux-amd64"), "ELF-AMD64")
	writeFile(t, filepath.Join(dist, "opskest-linux-arm64"), "ELF-ARM64")
	writeFile(t, filepath.Join(dist, "opskest-windows-amd64.exe"), "MZ-WIN")
	writeFile(t, filepath.Join(dist, "opskest-darwin-arm64"), "MACHO")
	writeFile(t, filepath.Join(dist, "README.txt"), "ignored")
	writeFile(t, filepath.Join(dist, "manifest.json.sig"), "ignored")
	return dist
}

func TestManifestSignVerifyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dist := buildDist(t)
	notes := filepath.Join(dir, "notes.md")
	writeFile(t, notes, "## 1.4.0\n- better things\n")
	base := filepath.Join(dir, "release")
	if code, _, e := runTool(t, "keygen", "-out", base); code != 0 {
		t.Fatal(e)
	}

	code, out, e := runTool(t, "manifest", "-version", "v1.4.0", "-notes", notes, "-dir", dist,
		"-base-url", "https://github.com/o/r/releases/download/v1.4.0/", "-min-version", "1.0.0")
	if code != 0 {
		t.Fatalf("manifest = %d %s", code, e)
	}
	if !strings.Contains(out, "4 assets") {
		t.Fatalf("out = %s", out)
	}
	mpath := filepath.Join(dist, "manifest.json")
	data, err := os.ReadFile(mpath)
	if err != nil {
		t.Fatal(err)
	}
	m, err := update.ParseManifest(data)
	if err != nil {
		t.Fatalf("the server rejects the generated manifest: %v", err)
	}
	if m.Version != "1.4.0" || m.MinVersion != "1.0.0" || m.PublishedAt != "2026-11-02T10:00:00Z" || !strings.Contains(m.Notes, "better things") {
		t.Fatalf("manifest = %+v", m)
	}
	a := m.SelectAsset("windows", "amd64")
	if a == nil || a.URL != "https://github.com/o/r/releases/download/v1.4.0/opskest-windows-amd64.exe" || a.Size != 6 || len(a.SHA256) != 64 {
		t.Fatalf("windows asset = %+v", a)
	}
	// sha256("ELF-AMD64") is checked independently of the tool's code path.
	if l := m.SelectAsset("linux", "amd64"); l == nil || l.SHA256 != sha256Hex("ELF-AMD64") {
		t.Fatalf("linux asset = %+v", l)
	}
	if len(m.Assets) != 4 {
		t.Fatalf("assets = %d (README and .sig must be ignored)", len(m.Assets))
	}
	// Deterministic order.
	var order []string
	for _, x := range m.Assets {
		order = append(order, x.OS+"/"+x.Arch)
	}
	if strings.Join(order, ",") != "darwin/arm64,linux/amd64,linux/arm64,windows/amd64" {
		t.Fatalf("order = %v", order)
	}

	if code, _, e := runTool(t, "sign", "-key", base+".key", "-file", mpath); code != 0 {
		t.Fatalf("sign = %d %s", code, e)
	}
	sig, err := os.ReadFile(mpath + ".sig")
	if err != nil {
		t.Fatal(err)
	}
	code, out, e = runTool(t, "verify", "-pub", base+".pub", "-file", mpath)
	if code != 0 || !strings.Contains(out, "OK") {
		t.Fatalf("verify = %d %q %q", code, out, e)
	}
	// The server-side verifier agrees with the tool.
	pubText, _ := os.ReadFile(base + ".pub")
	pk, _ := update.ParsePublicKey(string(pubText))
	if err := update.VerifySignature(pk, data, string(sig)); err != nil {
		t.Fatalf("server verifier rejects the tool signature: %v", err)
	}
	// The public key can also be passed inline.
	if code, _, _ := runTool(t, "verify", "-pub", strings.TrimSpace(string(pubText)), "-file", mpath); code != 0 {
		t.Fatal("inline public key rejected")
	}

	// Tampering with one byte of the manifest fails verification.
	tampered := bytes.Replace(data, []byte("1.4.0"), []byte("9.9.9"), 1)
	if err := os.WriteFile(mpath, tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, e := runTool(t, "verify", "-pub", base+".pub", "-file", mpath); code == 0 || !strings.Contains(e, "signature") {
		t.Fatalf("tampered manifest verified: %d %q", code, e)
	}
	// A different key fails too.
	os.WriteFile(mpath, data, 0o644)
	other := filepath.Join(dir, "other")
	runTool(t, "keygen", "-out", other)
	if code, _, _ := runTool(t, "verify", "-pub", other+".pub", "-file", mpath); code == 0 {
		t.Fatal("wrong public key verified")
	}
	// Missing signature file.
	os.Remove(mpath + ".sig")
	if code, _, _ := runTool(t, "verify", "-pub", base+".pub", "-file", mpath); code == 0 {
		t.Fatal("missing signature verified")
	}
}

func TestManifestInputValidation(t *testing.T) {
	dist := buildDist(t)
	empty := t.TempDir()
	big := filepath.Join(t.TempDir(), "notes.md")
	writeFile(t, big, strings.Repeat("x", update.MaxNotesBytes+1))
	cases := map[string][]string{
		"no version":     {"manifest", "-dir", dist, "-base-url", "https://x/y"},
		"no base url":    {"manifest", "-version", "1.0.0", "-dir", dist},
		"bad version":    {"manifest", "-version", "dev", "-dir", dist, "-base-url", "https://x/y"},
		"bad min":        {"manifest", "-version", "1.0.0", "-min-version", "old", "-dir", dist, "-base-url", "https://x/y"},
		"http base url":  {"manifest", "-version", "1.0.0", "-dir", dist, "-base-url", "http://x/y"},
		"fake loopback":  {"manifest", "-version", "1.0.0", "-dir", dist, "-base-url", "http://127.0.0.1.evil.com/y"},
		"empty dir":      {"manifest", "-version", "1.0.0", "-dir", empty, "-base-url", "https://x/y"},
		"missing dir":    {"manifest", "-version", "1.0.0", "-dir", filepath.Join(empty, "nope"), "-base-url", "https://x/y"},
		"notes too big":  {"manifest", "-version", "1.0.0", "-dir", dist, "-base-url", "https://x/y", "-notes", big},
		"bad published":  {"manifest", "-version", "1.0.0", "-dir", dist, "-base-url", "https://x/y", "-published", "soon"},
		"sign no file":   {"sign"},
		"verify no file": {"verify"},
		"unknown":        {"frobnicate"},
	}
	for name, args := range cases {
		if code, _, _ := runTool(t, args...); code == 0 {
			t.Errorf("%s: succeeded, want failure", name)
		}
	}
	if code, _, _ := runTool(t); code != 2 {
		t.Errorf("no args = %d, want 2", code)
	}
	// A windows binary without .exe is a mistake worth catching.
	bad := t.TempDir()
	writeFile(t, filepath.Join(bad, "opskest-windows-amd64"), "MZ")
	if code, _, e := runTool(t, "manifest", "-version", "1.0.0", "-dir", bad, "-base-url", "https://x/y"); code == 0 || !strings.Contains(e, ".exe") {
		t.Errorf("windows without .exe: %d %q", code, e)
	}
}

func TestSignRefusesInvalidManifestAndBadKey(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "release")
	runTool(t, "keygen", "-out", base)
	bad := filepath.Join(dir, "manifest.json")
	writeFile(t, bad, `{"version":"nope"}`)
	if code, _, _ := runTool(t, "sign", "-key", base+".key", "-file", bad); code == 0 {
		t.Fatal("signed an invalid manifest")
	}
	if _, err := os.Stat(bad + ".sig"); err == nil {
		t.Fatal("signature written for an invalid manifest")
	}
	good, _ := json.Marshal(map[string]any{"version": "1.0.0", "assets": []any{}})
	writeFile(t, bad, string(good))
	keyBad := filepath.Join(dir, "bad.key")
	writeFile(t, keyBad, "not base64!!")
	if code, _, _ := runTool(t, "sign", "-key", keyBad, "-file", bad); code == 0 {
		t.Fatal("signed with a garbage key")
	}
	writeFile(t, keyBad, base64.StdEncoding.EncodeToString([]byte("short")))
	if code, _, _ := runTool(t, "sign", "-key", keyBad, "-file", bad); code == 0 {
		t.Fatal("signed with a short key")
	}
	if code, _, _ := runTool(t, "sign", "-key", filepath.Join(dir, "missing.key"), "-file", bad); code == 0 {
		t.Fatal("signed with a missing key")
	}
	// A 32-byte seed form is accepted as well.
	seedKey, _ := os.ReadFile(base + ".key")
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(string(seedKey)))
	writeFile(t, keyBad, base64.StdEncoding.EncodeToString(raw[:32]))
	if code, _, e := runTool(t, "sign", "-key", keyBad, "-file", bad); code != 0 {
		t.Fatalf("seed-form key rejected: %s", e)
	}
	if code, _, _ := runTool(t, "verify", "-pub", base+".pub", "-file", bad); code != 0 {
		t.Fatal("signature from the seed-form key does not verify against the public key")
	}
}
