package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvFileLayeredUnderEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "\xef\xbb\xbf# comment\nAPP_URL=\"https://status.example.com\"\nexport PORT=9000 # inline\nEMPTY=\nQUOTED='a b'\nNOEQUALS\nFROM_ENV=file\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"OPSKEST_ENV_FILE": path, "FROM_ENV": "process"}
	get := withEnvFile(func(k string) string { return env[k] })
	for k, want := range map[string]string{
		"APP_URL":  "https://status.example.com",
		"PORT":     "9000",
		"QUOTED":   "a b",
		"FROM_ENV": "process",
		"MISSING":  "",
	} {
		if got := get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestEnvFileMissingIsIgnored(t *testing.T) {
	get := withEnvFile(func(k string) string {
		if k == "OPSKEST_ENV_FILE" {
			return filepath.Join(t.TempDir(), "nope")
		}
		return ""
	})
	if get("APP_URL") != "" {
		t.Fatal("no file, no value")
	}
}

func TestHealthPort(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want string
	}{
		{nil, "8080"},
		{map[string]string{"PORT": "9100"}, "9100"},
		{map[string]string{"LISTEN_ADDR": "127.0.0.1:7000", "PORT": "9100"}, "7000"},
	}
	for _, c := range cases {
		if got := healthPort(func(k string) string { return c.env[k] }); got != c.want {
			t.Errorf("%v -> %s, want %s", c.env, got, c.want)
		}
	}
}

func TestEnvFileQuotedValueWithComment(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	long := strings.Repeat("x", 100_000)
	content := "A=\"two words\" # note\nB='it''s' # c\nC=plain # c\nD=\"pa\"ss\"\nE=\nLONG=" + long + "\nAFTER=1\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got := loadEnvFile(path)
	for k, want := range map[string]string{"A": "two words", "C": "plain", "D": `pa"ss`, "E": "", "AFTER": "1"} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	if len(got["LONG"]) != len(long) {
		t.Errorf("a 100 kB line was cut to %d bytes", len(got["LONG"]))
	}
}
