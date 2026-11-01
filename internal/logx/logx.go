// Package logx provides logging helpers that keep secrets, request
// payloads and settings out of the logs.
package logx

import (
	"log/slog"
	"unicode/utf8"
)

const maxErrLen = 300

// Err logs an error without ever logging request payloads, settings or
// secrets: only err.Error(), trimmed. Callers pass safe attrs (ids, kinds).
func Err(l *slog.Logger, msg string, err error, attrs ...any) {
	s := ""
	if err != nil {
		s = err.Error()
	}
	if len(s) > maxErrLen {
		s = truncateAtRuneBoundary(s, maxErrLen) + "…"
	}
	l.Error(msg, append(attrs, "err", s)...)
}

// truncateAtRuneBoundary returns the first n bytes of s, backed off to the
// nearest earlier rune boundary if n would otherwise split a multi-byte
// UTF-8 rune. This keeps the result valid UTF-8.
func truncateAtRuneBoundary(s string, n int) string {
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
