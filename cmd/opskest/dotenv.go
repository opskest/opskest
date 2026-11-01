package main

import (
	"bufio"
	"os"
	"strings"
)

// envFileName is read from the working directory when present, so a plain
// binary (or a cPanel cron job) can be configured by dropping a .env file
// beside it. OPSKEST_ENV_FILE points at a different file.
const envFileName = ".env"

// loadEnvFile parses simple KEY=VALUE lines: blank lines and # comments are
// skipped, an optional "export " prefix is ignored and matching single or
// double quotes around a value are removed. A missing file is not an error.
func loadEnvFile(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	out := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20) // a long line must not silently end the file
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\xef\xbb\xbf"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, val, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			continue
		}
		val = strings.TrimSpace(val)
		quoted := len(val) >= 1 && (val[0] == '"' || val[0] == '\'')
		if n := len(val); quoted && n >= 2 && val[n-1] == val[0] {
			val = val[1 : n-1]
		} else if i := closingQuoteBeforeComment(val); quoted && i >= 0 {
			val = val[1:i] // KEY="value" # comment
		} else if i := strings.Index(val, " #"); i >= 0 {
			val = strings.TrimSpace(val[:i])
		}
		out[key] = val
	}
	return out
}

// withEnvFile layers the .env file under the real environment: a variable
// already set in the environment always wins.
func withEnvFile(getenv func(string) string) func(string) string {
	path := getenv("OPSKEST_ENV_FILE")
	if path == "" {
		path = envFileName
	}
	file := loadEnvFile(path)
	if len(file) == 0 {
		return getenv
	}
	return func(key string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return file[key]
	}
}

// closingQuoteBeforeComment returns the index of the quote that closes a quoted
// value followed by an inline comment (`"a b" # note`), or -1.
func closingQuoteBeforeComment(val string) int {
	if len(val) < 2 || (val[0] != '"' && val[0] != '\'') {
		return -1
	}
	i := strings.Index(val[1:], string(val[0])+" #")
	if i < 0 {
		return -1
	}
	return i + 1
}
