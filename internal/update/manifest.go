package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Limits applied to everything that comes from the feed. They are
// deliberately small: a release manifest is a few KiB.
const (
	MaxManifestBytes = 1 << 20          // 1 MiB on the wire
	MaxSigBytes      = 4 << 10          // a base64 signature is 88 bytes
	MaxNotesBytes    = 20 << 10         // release notes
	MaxAssets        = 64               // entries in assets[]
	MaxAssetBytes    = 200 << 20        // 200 MiB per binary
	maxFieldLen      = 2048             // url and similar strings
	feedTimeout      = 15 * time.Second // manifest and signature fetches
)

// Asset is one downloadable binary in a manifest.
type Asset struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest is the signed release description published by the owner.
type Manifest struct {
	Version     string  `json:"version"`
	PublishedAt string  `json:"publishedAt,omitempty"`
	Notes       string  `json:"notes,omitempty"`
	MinVersion  string  `json:"minVersion,omitempty"`
	Assets      []Asset `json:"assets"`

	// Parsed form of the fields above, filled in by ParseManifest.
	ver       Version
	published time.Time
}

// ParsedVersion returns the manifest version as parsed by ParseManifest.
func (m *Manifest) ParsedVersion() Version { return m.ver }

// Published returns the parsed publishedAt, or the zero time.
func (m *Manifest) Published() time.Time { return m.published }

// ParseManifest decodes and validates manifest bytes. It enforces the size
// limits above; it does not check the signature (see VerifySignature) and
// does not judge asset URLs (Install does, for the asset it picks).
func ParseManifest(data []byte) (*Manifest, error) {
	if len(data) == 0 {
		return nil, errors.New("manifest is empty")
	}
	if len(data) > MaxManifestBytes {
		return nil, errors.New("manifest is larger than 1 MiB")
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest is not valid JSON: %s", jsonErrText(err))
	}
	if dec.More() {
		return nil, errors.New("manifest has trailing content")
	}
	v, err := ParseVersion(m.Version)
	if err != nil {
		return nil, fmt.Errorf("manifest version is invalid: %w", err)
	}
	m.ver = v
	if m.MinVersion != "" {
		if _, err := ParseVersion(m.MinVersion); err != nil {
			return nil, fmt.Errorf("manifest minVersion is invalid: %w", err)
		}
	}
	if len(m.Notes) > MaxNotesBytes {
		return nil, errors.New("manifest notes are longer than 20 KiB")
	}
	if m.PublishedAt != "" {
		t, err := time.Parse(time.RFC3339, m.PublishedAt)
		if err != nil {
			return nil, errors.New("manifest publishedAt is not an RFC 3339 time")
		}
		m.published = t
	}
	if len(m.Assets) > MaxAssets {
		return nil, fmt.Errorf("manifest lists more than %d assets", MaxAssets)
	}
	for i, a := range m.Assets {
		if a.OS == "" || a.Arch == "" || len(a.OS) > 32 || len(a.Arch) > 32 {
			return nil, fmt.Errorf("manifest asset %d has no os/arch", i)
		}
		if len(a.URL) > maxFieldLen {
			return nil, fmt.Errorf("manifest asset %d url is too long", i)
		}
		if a.Size < 0 {
			return nil, fmt.Errorf("manifest asset %d has a negative size", i)
		}
		if len(a.SHA256) != 64 {
			return nil, fmt.Errorf("manifest asset %d sha256 must be 64 hex characters", i)
		}
		if _, err := hex.DecodeString(a.SHA256); err != nil {
			return nil, fmt.Errorf("manifest asset %d sha256 is not hex", i)
		}
	}
	return &m, nil
}

func jsonErrText(err error) string {
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return "syntax error"
	}
	var ue *json.UnmarshalTypeError
	if errors.As(err, &ue) {
		return "wrong type for field " + ue.Field
	}
	return "decode error"
}

// SelectAsset returns the asset for goos/goarch, or nil.
func (m *Manifest) SelectAsset(goos, goarch string) *Asset {
	for i := range m.Assets {
		a := &m.Assets[i]
		if strings.EqualFold(a.OS, goos) && strings.EqualFold(a.Arch, goarch) {
			return a
		}
	}
	return nil
}

// ParsePublicKey decodes a base64 ed25519 public key (32 bytes).
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, errors.New("public key is not valid base64")
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key must be %d bytes (got %d)", ed25519.PublicKeySize, len(b))
	}
	return ed25519.PublicKey(b), nil
}

// KeyFingerprint is a short, non-secret identifier of a public key (first
// 8 hex characters of its SHA-256) for display, never an authenticator.
func KeyFingerprint(key string) string {
	k, err := ParsePublicKey(key)
	if err != nil {
		return ""
	}
	return fingerprint(k)
}

// VerifySignature checks sigB64 (base64 of the 64-byte signature) against
// the exact manifest bytes.
func VerifySignature(pub ed25519.PublicKey, manifest []byte, sigB64 string) error {
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("no valid public key")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sigB64))
	if err != nil {
		return errors.New("signature is not valid base64")
	}
	if len(sig) != ed25519.SignatureSize {
		return errors.New("signature has the wrong length")
	}
	if !ed25519.Verify(pub, manifest, sig) {
		return errors.New("signature does not match the manifest")
	}
	return nil
}

// ValidateFeedURL reports whether u is an acceptable release feed
// location: https, or http only for loopback (127.0.0.1, ::1, localhost).
func ValidateFeedURL(u string) error {
	_, err := parseFetchURL(u)
	return err
}

// ValidateSettingsFeedURL is the rule for the saved setting: empty means "no
// source configured"; otherwise https, or plain http only for a loopback host
// (127.0.0.1, ::1, localhost), which is what a local test feed needs. A
// signed manifest is still required to install, so loopback http opens
// nothing up.
func ValidateSettingsFeedURL(u string) error {
	if u == "" {
		return nil
	}
	_, err := parseFetchURL(u)
	return err
}

func parseFetchURL(raw string) (*url.URL, error) {
	if raw == "" || len(raw) > maxFieldLen {
		return nil, errors.New("URL is empty or too long")
	}
	p, err := url.Parse(raw)
	if err != nil || p.Host == "" {
		return nil, errors.New("URL is not valid")
	}
	if p.User != nil {
		return nil, errors.New("URL must not contain credentials")
	}
	if !allowedScheme(p) {
		return nil, errors.New("URL must use https")
	}
	return p, nil
}

// allowedScheme: https always; http only for a loopback host.
func allowedScheme(p *url.URL) bool {
	switch strings.ToLower(p.Scheme) {
	case "https":
		return true
	case "http":
		switch strings.ToLower(p.Hostname()) {
		case "127.0.0.1", "localhost", "::1":
			return true
		}
	}
	return false
}

func fingerprint(k ed25519.PublicKey) string {
	h := sha256.Sum256(k)
	return hex.EncodeToString(h[:4])
}
