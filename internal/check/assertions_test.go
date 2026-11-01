package check

import (
	"net/http"
	"strings"
	"testing"

	"opskest/internal/model"
)

func TestEvalAssertions(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("X-Version", "2.4")
	body := []byte(`{"status":"UP","version":2.4,"checks":{"db":"ok"},"items":[1,2,3]}`)
	pass := []model.Assertion{
		{Source: "status", Op: "eq", Value: "200"},
		{Source: "latency", Op: "lt", Value: "500"},
		{Source: "header", Property: "content-type", Op: "contains", Value: "json"},
		{Source: "header", Property: "X-Version", Op: "gte", Value: "2"},
		{Source: "header", Property: "X-Version", Op: "exists"},
		{Source: "body", Op: "regex", Value: `"db":\s*"ok"`},
		{Source: "json", Property: "status", Op: "eq", Value: "UP"},
		{Source: "json", Property: "version", Op: "gte", Value: "2"},
		{Source: "json", Property: "items.#", Op: "eq", Value: "3"},
		{Source: "json", Property: "checks.db", Op: "not_empty"},
	}
	for _, a := range pass {
		if ok, reason := EvalAssertions([]model.Assertion{a}, 200, 120, h, body); !ok {
			t.Errorf("%+v failed: %s", a, reason)
		}
	}
	fail := map[string]model.Assertion{
		"status code is 200, expected == 204":          {Source: "status", Op: "eq", Value: "204"},
		"response time (ms) is 120, expected < 100":    {Source: "latency", Op: "lt", Value: "100"},
		"header X-Missing is missing":                  {Source: "header", Property: "X-Missing", Op: "exists"},
		`status is "UP", expected == "DOWN"`:           {Source: "json", Property: "status", Op: "eq", Value: "DOWN"},
		"version is 2.4, expected > 3":                 {Source: "json", Property: "version", Op: "gt", Value: "3"},
		"nope not found":                               {Source: "json", Property: "nope", Op: "eq", Value: "x"},
		`body is "{\"status\":\"UP\",\"version\":2.4,`: {Source: "body", Op: "not_contains", Value: "UP"},
	}
	for want, a := range fail {
		ok, reason := EvalAssertions([]model.Assertion{a}, 200, 120, h, body)
		if ok || !strings.HasPrefix(reason, want) {
			t.Errorf("%+v: ok=%v reason=%q, want prefix %q", a, ok, reason, want)
		}
	}
}

func TestValidateAssertions(t *testing.T) {
	m := model.NewMonitor(model.TypeHTTP)
	m.Name, m.Target = "x", "https://example.com"
	m.Settings.Assertions = []model.Assertion{
		{Source: "nope", Op: "eq"},
		{Source: "latency", Op: "contains", Value: "1"},
		{Source: "json", Op: "eq", Value: "1"},
		{Source: "status", Op: "lt", Value: "abc"},
		{Source: "body", Op: "regex", Value: "("},
	}
	fe := m.Validate()
	for _, k := range []string{"settings.assertions.0.source", "settings.assertions.1.op", "settings.assertions.2.property", "settings.assertions.3.value", "settings.assertions.4.value"} {
		if _, ok := fe[k]; !ok {
			t.Errorf("missing error %s (got %v)", k, fe)
		}
	}
}
