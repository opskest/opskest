package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func prevPath(exe string) string { return exe + ".prev" }

// canWriteDir proves the process can create (and so rename into) dir.
func canWriteDir(dir string) bool {
	f, err := os.CreateTemp(dir, ".opskest-write-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name) == nil
}

// checkExecutable verifies the file begins with the magic bytes of an
// executable for goos (ELF, PE, Mach-O or a universal binary).
func checkExecutable(path, goos string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return errors.New("downloaded file is too short to be an executable")
	}
	ok := false
	switch goos {
	case "linux", "freebsd", "netbsd", "openbsd", "solaris", "illumos":
		ok = bytes.Equal(magic[:], []byte{0x7f, 'E', 'L', 'F'})
	case "windows":
		ok = magic[0] == 'M' && magic[1] == 'Z'
	case "darwin":
		switch {
		case bytes.Equal(magic[:], []byte{0xfe, 0xed, 0xfa, 0xce}),
			bytes.Equal(magic[:], []byte{0xfe, 0xed, 0xfa, 0xcf}),
			bytes.Equal(magic[:], []byte{0xce, 0xfa, 0xed, 0xfe}),
			bytes.Equal(magic[:], []byte{0xcf, 0xfa, 0xed, 0xfe}),
			bytes.Equal(magic[:], []byte{0xca, 0xfe, 0xba, 0xbe}):
			ok = true
		}
	default:
		ok = true // unknown platform: rely on the version probe
	}
	if !ok {
		return fmt.Errorf("downloaded file is not a %s executable", goos)
	}
	return nil
}

type limitedBuf struct {
	buf bytes.Buffer
	max int
}

func (l *limitedBuf) Write(p []byte) (int, error) {
	if room := l.max - l.buf.Len(); room > 0 {
		if len(p) > room {
			l.buf.Write(p[:room])
		} else {
			l.buf.Write(p)
		}
	}
	return len(p), nil
}

// probeVersion runs `<path> version` (10 s limit) and returns its trimmed
// output.
func probeVersion(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version")
	out := &limitedBuf{max: 512}
	cmd.Stdout = out
	cmd.Env = os.Environ()
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", errors.New("the new binary did not answer `version` within 10 seconds")
		}
		return "", fmt.Errorf("the new binary could not be run: %v", unwrapExec(err))
	}
	return strings.TrimSpace(out.buf.String()), nil
}

func unwrapExec(err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return fmt.Errorf("exit status %d", ee.ExitCode())
	}
	return err
}

// verifyBinary checks magic bytes and, when want is non-empty, that
// `version` prints it.
func verifyBinary(ctx context.Context, path, want string) error {
	if err := checkExecutable(path, runtime.GOOS); err != nil {
		return err
	}
	got, err := probeVersion(ctx, path)
	if err != nil {
		return err
	}
	if want != "" && Normalize(got) != Normalize(want) {
		return fmt.Errorf("the new binary reports version %q, expected %q", truncate(got, 40), want)
	}
	return nil
}

type progressWriter struct {
	total int64
	n     int64
	last  int
	cb    func(pct int)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.n += int64(len(b))
	if p.cb != nil && p.total > 0 {
		pct := int(p.n * 100 / p.total)
		if pct > 100 {
			pct = 100
		}
		if pct != p.last {
			p.last = pct
			p.cb(pct)
		}
	}
	return len(b), nil
}

// download streams the asset into a temp file in dir. The file is removed
// on any failure. It enforces the declared size and the sha256.
func download(ctx context.Context, c *http.Client, ua string, a *Asset, dir string, timeout time.Duration, report func(int)) (string, error) {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	f, err := os.CreateTemp(dir, ".opskest-update-*"+ext)
	if err != nil {
		return "", errors.New("cannot create a temporary file next to the executable")
	}
	path := f.Name()
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", errors.New("download URL is not valid")
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("Accept-Encoding", "identity") // exact bytes, no transparent gunzip
	resp, err := c.Do(req)
	if err != nil {
		return "", sanitizeErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download server answered HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != a.Size {
		return "", errors.New("download size does not match the manifest")
	}

	h := sha256.New()
	pw := &progressWriter{total: a.Size, cb: report, last: -1}
	n, err := io.Copy(io.MultiWriter(f, h, pw), io.LimitReader(resp.Body, a.Size+1))
	if err != nil {
		return "", fmt.Errorf("download failed: %v", sanitizeErr(err))
	}
	if n > a.Size {
		return "", errors.New("download is larger than the size in the manifest")
	}
	if n < a.Size {
		return "", errors.New("download is shorter than the size in the manifest")
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), a.SHA256) {
		return "", errors.New("download checksum does not match the manifest")
	}
	if err := f.Sync(); err != nil {
		return "", fmt.Errorf("could not write the download: %v", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("could not write the download: %v", err)
	}
	ok = true
	return path, nil
}

// makeExecutable gives path the permissions of the current executable (plus
// the execute bits). os.CreateTemp creates 0600 files, which cannot run.
func makeExecutable(path, like string) {
	mode := os.FileMode(0o755)
	if fi, err := os.Stat(like); err == nil {
		mode = fi.Mode().Perm() | 0o111
	}
	_ = os.Chmod(path, mode)
}

// swapIn makes newPath the executable at exe and keeps the old one as
// exe.prev. On failure exe is unchanged.
func swapIn(exe, newPath string) error {
	makeExecutable(newPath, exe)
	if runtime.GOOS == "windows" {
		return swapInWindows(exe, newPath)
	}
	return swapInUnix(exe, newPath)
}

// swapInUnix never leaves exe missing: the old file is hard-linked (or
// copied) aside, then the new file atomically replaces exe by rename. The
// running process keeps its old inode.
func swapInUnix(exe, newPath string) error {
	prev := prevPath(exe)
	tmpPrev := prev + ".tmp"
	_ = os.Remove(tmpPrev)
	if err := os.Link(exe, tmpPrev); err != nil {
		if err := copyFile(exe, tmpPrev); err != nil {
			_ = os.Remove(tmpPrev)
			return fmt.Errorf("could not keep the current binary as .prev: %v", err)
		}
	}
	if err := os.Rename(newPath, exe); err != nil {
		_ = os.Remove(tmpPrev)
		return fmt.Errorf("could not replace the executable: %v", err)
	}
	if err := os.Rename(tmpPrev, prev); err != nil {
		// The new binary is in place; only the .prev bookkeeping failed.
		return fmt.Errorf("installed, but could not record .prev: %v", err)
	}
	return nil
}

// swapInWindows: a running exe cannot be overwritten but can be renamed.
func swapInWindows(exe, newPath string) error {
	prev := prevPath(exe)
	if err := os.Remove(prev); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("could not remove the old .prev: %v", err)
	}
	if err := os.Rename(exe, prev); err != nil {
		return fmt.Errorf("could not move the running executable aside: %v", err)
	}
	if err := os.Rename(newPath, exe); err != nil {
		if rerr := os.Rename(prev, exe); rerr != nil {
			return fmt.Errorf("could not install the new binary (%v) and could not restore the old one (%v); rename %s back manually", err, rerr, filepath.Base(prev))
		}
		return fmt.Errorf("could not install the new binary: %v", err)
	}
	return nil
}

// swapWithPrev exchanges exe and exe.prev, so a rollback is itself
// reversible.
func swapWithPrev(exe string) error {
	if runtime.GOOS == "windows" {
		return swapWithPrevWindows(exe)
	}
	return swapWithPrevUnix(exe)
}

func swapWithPrevUnix(exe string) error {
	prev := prevPath(exe)
	if _, err := os.Stat(prev); err != nil {
		return errors.New("there is no previous version to roll back to")
	}
	tmp := exe + ".swap"
	_ = os.Remove(tmp)
	if err := os.Link(exe, tmp); err != nil {
		if err := copyFile(exe, tmp); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("could not stage the swap: %v", err)
		}
	}
	if err := os.Rename(prev, exe); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("could not restore the previous binary: %v", err)
	}
	if err := os.Rename(tmp, prev); err != nil {
		return fmt.Errorf("rolled back, but could not keep the newer binary as .prev: %v", err)
	}
	return nil
}

func swapWithPrevWindows(exe string) error {
	prev := prevPath(exe)
	if _, err := os.Stat(prev); err != nil {
		return errors.New("there is no previous version to roll back to")
	}
	tmp := exe + ".swap"
	_ = os.Remove(tmp)
	if err := os.Rename(exe, tmp); err != nil {
		return fmt.Errorf("could not move the running executable aside: %v", err)
	}
	if err := os.Rename(prev, exe); err != nil {
		_ = os.Rename(tmp, exe)
		return fmt.Errorf("could not restore the previous binary: %v", err)
	}
	if err := os.Rename(tmp, prev); err != nil {
		return fmt.Errorf("rolled back, but could not keep the newer binary as .prev: %v", err)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
