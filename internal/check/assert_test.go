package check

import "testing"

const assertTestBody = `{"data":{"status":"ok","n":5,"tags":["a","b"]}}`

func TestEvalJSON(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		path     string
		op       string
		expected string
		want     bool
		reason   string
	}{
		{"eq true", assertTestBody, "data.status", "==", "ok", true, ""},
		{"eq false", assertTestBody, "data.status", "!=", "ok", false, ""},
		{"gt true", assertTestBody, "data.n", ">", "3", true, ""},
		{"lt false", assertTestBody, "data.n", "<", "3", false, ""},
		{"contains true", assertTestBody, "data.tags", "contains", "b", true, ""},
		{"exists false", assertTestBody, "data.missing", "exists", "", false, "data.missing not found"},
		{"invalid json", "not json", "data.status", "==", "ok", false, "response is not valid JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := EvalJSON([]byte(tc.body), tc.path, tc.op, tc.expected)
			if got != tc.want {
				t.Errorf("EvalJSON() got = %v, want %v (reason=%q)", got, tc.want, reason)
			}
			if tc.reason != "" && reason != tc.reason {
				t.Errorf("EvalJSON() reason = %q, want %q", reason, tc.reason)
			}
		})
	}
}

func TestEvalJSONNotFoundReasonForNonExistsOp(t *testing.T) {
	got, reason := EvalJSON([]byte(assertTestBody), "data.missing", "==", "x")
	if got {
		t.Fatalf("expected false for missing path")
	}
	if reason == "" {
		t.Fatalf("expected non-empty reason")
	}
}

func TestEvalKeyword(t *testing.T) {
	cases := []struct {
		name          string
		body          string
		keyword       string
		mode          string
		caseSensitive bool
		want          bool
	}{
		{"contains found", "Hello World", "World", "contains", true, true},
		{"contains not found", "Hello World", "Missing", "contains", true, false},
		{"contains case insensitive", "Hello World", "world", "contains", false, true},
		{"contains case sensitive miss", "Hello World", "world", "contains", true, false},
		{"not_contains true", "Hello World", "Missing", "not_contains", true, true},
		{"not_contains false", "Hello World", "World", "not_contains", true, false},
		{"regex match", "OK all good", `^OK\b`, "regex", true, true},
		{"regex no match", "not ok", `^OK\b`, "regex", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := EvalKeyword([]byte(tc.body), tc.keyword, tc.mode, tc.caseSensitive)
			if got != tc.want {
				t.Errorf("EvalKeyword() got = %v, want %v (reason=%q)", got, tc.want, reason)
			}
			if !tc.want && reason == "" {
				t.Errorf("EvalKeyword() expected non-empty reason on failure")
			}
		})
	}
}
