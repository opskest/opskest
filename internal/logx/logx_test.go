package logx

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"
)

func newLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, nil))
}

func TestErrLogsMessageAndError(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf)

	Err(l, "check failed", errors.New("boom"), "monitorID", "abc123")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	if rec["msg"] != "check failed" {
		t.Fatalf("msg = %v", rec["msg"])
	}
	if rec["err"] != "boom" {
		t.Fatalf("err = %v", rec["err"])
	}
	if rec["monitorID"] != "abc123" {
		t.Fatalf("monitorID = %v", rec["monitorID"])
	}
}

func TestErrNilError(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf)

	Err(l, "something happened", nil)

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	if rec["err"] != "" {
		t.Fatalf("err = %v, want empty string", rec["err"])
	}
}

func TestErrTruncatesLongMessages(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf)

	long := strings.Repeat("x", 400)
	Err(l, "check failed", errors.New(long))

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	got, _ := rec["err"].(string)
	// 300 chars + the trailing ellipsis rune.
	wantLen := 300 + len("…")
	if len([]rune(got)) != 301 || len(got) != wantLen {
		t.Fatalf("err len = %d (%q)", len(got), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("err = %q, want ellipsis suffix", got)
	}
}

func TestErrTruncationIsValidUTF8ForMultiByteRunes(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf)

	// 200 copies of "é" (2 bytes each) = 400 bytes, comfortably straddling
	// the 300-byte cutoff. Truncating byte-wise (s[:300]) would split one
	// of these runes and emit invalid UTF-8.
	long := strings.Repeat("é", 200)
	Err(l, "check failed", errors.New(long))

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	got, _ := rec["err"].(string)

	if !utf8.ValidString(got) {
		t.Fatalf("err is not valid UTF-8: %q", got)
	}
	withoutEllipsis := strings.TrimSuffix(got, "…")
	if len(withoutEllipsis) > 300 {
		t.Fatalf("truncated body is %d bytes, want <= 300", len(withoutEllipsis))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("err = %q, want ellipsis suffix", got)
	}
}

func TestTruncateAtRuneBoundaryBacksOffFromContinuationByte(t *testing.T) {
	// An ASCII prefix shifts every subsequent 2-byte "é" rune onto an odd
	// byte offset, so byte 300 (even) lands squarely on a continuation
	// byte and forces the back-off loop to run.
	s := "x" + strings.Repeat("é", 200)
	if s[300]&0xC0 != 0x80 {
		t.Fatalf("test setup: byte 300 is not a UTF-8 continuation byte")
	}

	got := truncateAtRuneBoundary(s, 300)

	if !utf8.ValidString(got) {
		t.Fatalf("result is not valid UTF-8: %q", got)
	}
	if len(got) != 299 {
		t.Fatalf("len(got) = %d, want 299 (backed off by one byte)", len(got))
	}
	if got != s[:299] {
		t.Fatalf("got %q, want the 299-byte prefix", got)
	}
}
