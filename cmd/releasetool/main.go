// Command releasetool prepares and signs an Opskest release.
//
//	go run ./cmd/releasetool keygen   [-out release]
//	go run ./cmd/releasetool manifest -version 1.4.0 -base-url https://... [-dir dist] [-notes notes.md] [-min-version 1.0.0]
//	go run ./cmd/releasetool sign     -key release.key -file dist/manifest.json
//	go run ./cmd/releasetool verify   -pub release.pub -file dist/manifest.json
//
// It uses only the standard library and the same parsing and verification
// code the server uses, so a manifest it accepts is one the server accepts.
// The private key is never printed.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"opskest/internal/update"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, time.Now))
}

const usage = `usage: releasetool <command> [flags]

commands:
  keygen    create a release signing key pair (release.key + release.pub)
  manifest  scan dist/ for opskest-<os>-<arch>[.exe] and write manifest.json
  sign      sign a manifest, writing <file>.sig
  verify    check a manifest and its signature against a public key
`

func run(args []string, stdout, stderr io.Writer, now func() time.Time) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "keygen":
		err = cmdKeygen(args[1:], stdout)
	case "manifest":
		err = cmdManifest(args[1:], stdout, now)
	case "sign":
		err = cmdSign(args[1:], stdout, stderr)
	case "verify":
		err = cmdVerify(args[1:], stdout)
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// ── keygen ────────────────────────────────────────────────────────────

func cmdKeygen(args []string, out io.Writer) error {
	fs := newFlags("keygen")
	base := fs.String("out", "release", "base name: writes <out>.key (private) and <out>.pub (public)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	keyPath, pubPath := *base+".key", *base+".pub"

	// Refuse to overwrite either file: replacing a key that signed past
	// releases would orphan every install that trusts it.
	for _, p := range []string{keyPath, pubPath} {
		if _, err := os.Lstat(p); err == nil {
			return fmt.Errorf("%s already exists; refusing to overwrite an existing key (move it away first)", p)
		}
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	// O_EXCL: also safe against a race between the check and the create.
	kf, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", keyPath, err)
	}
	if _, err := kf.WriteString(base64.StdEncoding.EncodeToString(priv) + "\n"); err != nil {
		kf.Close()
		os.Remove(keyPath)
		return err
	}
	if err := kf.Close(); err != nil {
		os.Remove(keyPath)
		return err
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	pf, err := os.OpenFile(pubPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		os.Remove(keyPath)
		return fmt.Errorf("create %s: %w", pubPath, err)
	}
	if _, err := pf.WriteString(pubB64 + "\n"); err != nil {
		pf.Close()
		os.Remove(keyPath)
		os.Remove(pubPath)
		return err
	}
	if err := pf.Close(); err != nil {
		os.Remove(keyPath)
		os.Remove(pubPath)
		return err
	}

	fmt.Fprintf(out, "wrote %s (PRIVATE: keep it secret, back it up, never commit it)\n", keyPath)
	fmt.Fprintf(out, "wrote %s (public)\n\n", pubPath)
	fmt.Fprintf(out, "public key (paste into Settings > Updates, or build with -ldflags \"-X main.updatePublicKey=...\"):\n%s\n", pubB64)
	return nil
}

// ── manifest ──────────────────────────────────────────────────────────

var assetName = regexp.MustCompile(`^opskest-([a-z]+)-([a-z0-9]+)(\.exe)?$`)

func cmdManifest(args []string, out io.Writer, now func() time.Time) error {
	fs := newFlags("manifest")
	version := fs.String("version", "", "release version, e.g. 1.4.0 (required)")
	notes := fs.String("notes", "", "markdown release notes file (max 20 KiB)")
	dir := fs.String("dir", "dist", "directory holding opskest-<os>-<arch>[.exe]")
	baseURL := fs.String("base-url", "", "https URL the files will be uploaded to (required; http only for 127.0.0.1/localhost test feeds)")
	minVersion := fs.String("min-version", "", "oldest version allowed to update directly (optional)")
	outFile := fs.String("out", "", "output file (default <dir>/manifest.json)")
	published := fs.String("published", "", "publishedAt as RFC 3339 (default: now, UTC)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *version == "" || *baseURL == "" {
		return errors.New("-version and -base-url are required")
	}
	v, err := update.ParseVersion(*version)
	if err != nil {
		return fmt.Errorf("-version: %w", err)
	}
	if *minVersion != "" {
		mv, err := update.ParseVersion(*minVersion)
		if err != nil {
			return fmt.Errorf("-min-version: %w", err)
		}
		*minVersion = mv.String()
	}
	bu, err := url.Parse(strings.TrimRight(*baseURL, "/"))
	if err != nil || bu.Host == "" || update.ValidateFeedURL(bu.String()) != nil {
		return errors.New("-base-url must be an https URL (http is allowed only for localhost / 127.0.0.1, for local test feeds)")
	}
	pub := now().UTC().Truncate(time.Second)
	if *published != "" {
		pub, err = time.Parse(time.RFC3339, *published)
		if err != nil {
			return errors.New("-published must be an RFC 3339 time")
		}
		pub = pub.UTC()
	}
	noteText := ""
	if *notes != "" {
		b, err := os.ReadFile(*notes)
		if err != nil {
			return err
		}
		if len(b) > update.MaxNotesBytes {
			return fmt.Errorf("notes are %d bytes; the limit is %d", len(b), update.MaxNotesBytes)
		}
		noteText = string(b)
	}

	entries, err := os.ReadDir(*dir)
	if err != nil {
		return err
	}
	var assets []update.Asset
	for _, e := range entries {
		m := assetName.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		osName, arch, exe := m[1], m[2], m[3] != ""
		if (osName == "windows") != exe {
			return fmt.Errorf("%s: windows binaries must end in .exe and others must not", e.Name())
		}
		sum, size, err := hashFile(filepath.Join(*dir, e.Name()))
		if err != nil {
			return err
		}
		if size == 0 || size > update.MaxAssetBytes {
			return fmt.Errorf("%s is %d bytes; assets must be 1 byte to 200 MiB", e.Name(), size)
		}
		assets = append(assets, update.Asset{
			OS: osName, Arch: arch, SHA256: sum, Size: size,
			URL: strings.TrimRight(*baseURL, "/") + "/" + url.PathEscape(e.Name()),
		})
	}
	if len(assets) == 0 {
		return fmt.Errorf("no opskest-<os>-<arch>[.exe] files in %s (run scripts/build-release first)", *dir)
	}
	sort.Slice(assets, func(i, j int) bool {
		if assets[i].OS != assets[j].OS {
			return assets[i].OS < assets[j].OS
		}
		return assets[i].Arch < assets[j].Arch
	})

	man := update.Manifest{
		Version:     v.String(),
		PublishedAt: pub.Format(time.RFC3339),
		Notes:       noteText,
		MinVersion:  *minVersion,
		Assets:      assets,
	}
	data, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	// The server must accept exactly what we are about to publish.
	if _, err := update.ParseManifest(data); err != nil {
		return fmt.Errorf("generated manifest is invalid: %w", err)
	}
	dest := *outFile
	if dest == "" {
		dest = filepath.Join(*dir, "manifest.json")
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s (version %s, %d assets)\n", dest, v.String(), len(assets))
	for _, a := range assets {
		fmt.Fprintf(out, "  %-8s %-6s %10d bytes  sha256 %s...\n", a.OS, a.Arch, a.Size, a.SHA256[:12])
	}
	return nil
}

func hashFile(path string) (sum string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// ── sign ──────────────────────────────────────────────────────────────

func cmdSign(args []string, out, errOut io.Writer) error {
	fs := newFlags("sign")
	keyFile := fs.String("key", "release.key", "private key file")
	file := fs.String("file", "", "manifest to sign (required)")
	sigFile := fs.String("out", "", "signature file (default <file>.sig)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return errors.New("-file is required")
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(*keyFile); err == nil && fi.Mode().Perm()&0o077 != 0 {
			fmt.Fprintf(errOut, "warning: %s is readable by other users; run chmod 600 %s\n", *keyFile, *keyFile)
		}
	}
	priv, err := readPrivateKey(*keyFile)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	if _, err := update.ParseManifest(data); err != nil {
		return fmt.Errorf("refusing to sign an invalid manifest: %w", err)
	}
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data))
	dest := *sigFile
	if dest == "" {
		dest = *file + ".sig"
	}
	if err := os.WriteFile(dest, []byte(sig+"\n"), 0o644); err != nil {
		return err
	}
	// Prove the result verifies with the matching public key.
	pub := priv.Public().(ed25519.PublicKey)
	if err := update.VerifySignature(pub, data, sig); err != nil {
		os.Remove(dest)
		return fmt.Errorf("self-check failed: %w", err)
	}
	fmt.Fprintf(out, "wrote %s (signed with key %s)\n", dest, update.KeyFingerprint(base64.StdEncoding.EncodeToString(pub)))
	return nil
}

func readPrivateKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, errors.New("private key file is not base64")
	}
	switch len(raw) {
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(raw), nil
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	}
	return nil, errors.New("private key file has the wrong length")
}

// ── verify ────────────────────────────────────────────────────────────

func cmdVerify(args []string, out io.Writer) error {
	fs := newFlags("verify")
	pubArg := fs.String("pub", "release.pub", "public key file (or the base64 key itself)")
	file := fs.String("file", "", "manifest to check (required)")
	sigFile := fs.String("sig", "", "signature file (default <file>.sig)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return errors.New("-file is required")
	}
	keyText := *pubArg
	if b, err := os.ReadFile(*pubArg); err == nil {
		keyText = string(b)
	}
	pub, err := update.ParsePublicKey(keyText)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	sp := *sigFile
	if sp == "" {
		sp = *file + ".sig"
	}
	sig, err := os.ReadFile(sp)
	if err != nil {
		return fmt.Errorf("read signature: %w", err)
	}
	if err := update.VerifySignature(pub, data, string(sig)); err != nil {
		return err
	}
	man, err := update.ParseManifest(data)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "OK: signature valid, manifest version %s, %d assets\n", man.ParsedVersion().String(), len(man.Assets))
	return nil
}
