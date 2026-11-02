package check

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"

	"opskest/internal/model"
)

// EvalJSON evaluates a JSON-path assertion against a response body using
// gjson path syntax. It returns (true, "") on success and (false, reason)
// on failure, including when the body is not valid JSON or the path is
// missing.
func EvalJSON(body []byte, path, op, expected string) (bool, string) {
	if !gjson.ValidBytes(body) {
		return false, model.TrimReason("response is not valid JSON")
	}

	res := gjson.GetBytes(body, path)
	if !res.Exists() {
		return false, model.TrimReason(fmt.Sprintf("%s not found", path))
	}

	if op == "exists" {
		return true, ""
	}

	switch op {
	case "==":
		if res.String() == expected {
			return true, ""
		}
		return false, model.TrimReason(fmt.Sprintf("%s is %q, expected == %q", path, res.String(), expected))
	case "!=":
		if res.String() != expected {
			return true, ""
		}
		return false, model.TrimReason(fmt.Sprintf("%s is %q, expected != %q", path, res.String(), expected))
	case ">", "<":
		fv := res.Float()
		ev, err := strconv.ParseFloat(expected, 64)
		if err != nil {
			return false, model.TrimReason(fmt.Sprintf("invalid comparison value %q", expected))
		}
		ok := fv > ev
		if op == "<" {
			ok = fv < ev
		}
		if ok {
			return true, ""
		}
		return false, model.TrimReason(fmt.Sprintf("%s is %v, expected %s %v", path, fv, op, ev))
	case "contains":
		ok := false
		if res.IsArray() {
			res.ForEach(func(_, v gjson.Result) bool {
				if v.String() == expected {
					ok = true
					return false
				}
				return true
			})
		} else {
			ok = strings.Contains(res.String(), expected)
		}
		if ok {
			return true, ""
		}
		return false, model.TrimReason(fmt.Sprintf("%s does not contain %q", path, expected))
	default:
		return false, model.TrimReason(fmt.Sprintf("unknown json op %q", op))
	}
}

// EvalKeyword evaluates a keyword assertion against a response body.
// mode is one of "contains", "not_contains" or "regex". For regex mode,
// caseSensitive false prepends a (?i) flag.
func EvalKeyword(body []byte, keyword, mode string, caseSensitive bool) (bool, string) {
	switch mode {
	case "regex":
		pattern := keyword
		if !caseSensitive {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return false, model.TrimReason(fmt.Sprintf("invalid regex: %s", err))
		}
		if re.Match(body) {
			return true, ""
		}
		return false, model.TrimReason(fmt.Sprintf("regex %q did not match", keyword))
	case "not_contains":
		if !bodyContains(body, keyword, caseSensitive) {
			return true, ""
		}
		return false, model.TrimReason(fmt.Sprintf("keyword %q found", keyword))
	case "contains", "":
		if bodyContains(body, keyword, caseSensitive) {
			return true, ""
		}
		return false, model.TrimReason(fmt.Sprintf("keyword %q not found", keyword))
	default:
		return false, model.TrimReason(fmt.Sprintf("unknown keyword mode %q", mode))
	}
}

func bodyContains(body []byte, keyword string, caseSensitive bool) bool {
	text := string(body)
	k := keyword
	if !caseSensitive {
		text = strings.ToLower(text)
		k = strings.ToLower(keyword)
	}
	return strings.Contains(text, k)
}

// EvalAssertions runs every assertion against one HTTP response and
// returns the first failure's reason.
func EvalAssertions(list []model.Assertion, code, latencyMs int, header http.Header, body []byte) (bool, string) {
	for _, a := range list {
		if ok, reason := evalAssertion(a, code, latencyMs, header, body); !ok {
			return false, model.TrimReason(reason)
		}
	}
	return true, ""
}

func evalAssertion(a model.Assertion, code, latencyMs int, header http.Header, body []byte) (bool, string) {
	switch a.Source {
	case "status":
		return compareNumber("status code", float64(code), a.Op, a.Value)
	case "latency":
		return compareNumber("response time (ms)", float64(latencyMs), a.Op, a.Value)
	case "header":
		vals, present := header[http.CanonicalHeaderKey(a.Property)]
		v := strings.Join(vals, ", ")
		if a.Op == "exists" {
			if present {
				return true, ""
			}
			return false, fmt.Sprintf("header %s is missing", a.Property)
		}
		return compareString("header "+a.Property, v, present, a.Op, a.Value)
	case "body":
		return compareString("body", string(body), true, a.Op, a.Value)
	case "json":
		if !gjson.ValidBytes(body) {
			return false, "response is not valid JSON"
		}
		r := gjson.GetBytes(body, a.Property)
		if a.Op == "exists" {
			if r.Exists() {
				return true, ""
			}
			return false, fmt.Sprintf("%s not found", a.Property)
		}
		if !r.Exists() && a.Op != "empty" {
			return false, fmt.Sprintf("%s not found", a.Property)
		}
		if model.IsNumericOp(a.Op) {
			return compareNumber(a.Property, r.Float(), a.Op, a.Value)
		}
		return compareString(a.Property, r.String(), r.Exists(), a.Op, a.Value)
	}
	return false, "unknown assertion source " + a.Source
}

func compareNumber(what string, got float64, op, want string) (bool, string) {
	w, err := strconv.ParseFloat(want, 64)
	if err != nil {
		return false, fmt.Sprintf("invalid number %q", want)
	}
	ok := false
	switch op {
	case "eq":
		ok = got == w
	case "ne":
		ok = got != w
	case "gt":
		ok = got > w
	case "gte":
		ok = got >= w
	case "lt":
		ok = got < w
	case "lte":
		ok = got <= w
	}
	if ok {
		return true, ""
	}
	return false, fmt.Sprintf("%s is %s, expected %s %s", what, strconv.FormatFloat(got, 'f', -1, 64), opWord(op), want)
}

func compareString(what, got string, present bool, op, want string) (bool, string) {
	ok := false
	switch op {
	case "eq":
		ok = present && got == want
	case "ne":
		ok = got != want
	case "contains":
		ok = strings.Contains(got, want)
	case "not_contains":
		ok = !strings.Contains(got, want)
	case "regex":
		re, err := regexp.Compile(want)
		ok = err == nil && re.MatchString(got)
	case "empty":
		ok = got == ""
	case "not_empty":
		ok = got != ""
	case "gt", "gte", "lt", "lte":
		f, err := strconv.ParseFloat(strings.TrimSpace(got), 64)
		if err != nil {
			return false, fmt.Sprintf("%s is %q, not a number", what, clip(got))
		}
		return compareNumber(what, f, op, want)
	}
	if ok {
		return true, ""
	}
	if op == "empty" || op == "not_empty" {
		return false, fmt.Sprintf("%s is expected to be %s", what, opWord(op))
	}
	return false, fmt.Sprintf("%s is %q, expected %s %q", what, clip(got), opWord(op), want)
}

func opWord(op string) string {
	switch op {
	case "eq":
		return "=="
	case "ne":
		return "!="
	case "gt":
		return ">"
	case "gte":
		return ">="
	case "lt":
		return "<"
	case "lte":
		return "<="
	case "not_contains":
		return "not to contain"
	case "not_empty":
		return "not empty"
	case "regex":
		return "to match"
	}
	return op
}

func clip(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}
