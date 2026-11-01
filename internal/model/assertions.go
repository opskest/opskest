package model

import (
	"fmt"
	"regexp"
	"strconv"
)

// MaxAssertions caps one monitor's assertion list.
const MaxAssertions = 20

var assertionOps = map[string]map[string]bool{
	"status":  {"eq": true, "ne": true, "gt": true, "gte": true, "lt": true, "lte": true},
	"latency": {"gt": true, "gte": true, "lt": true, "lte": true},
	"header":  {"eq": true, "ne": true, "contains": true, "not_contains": true, "regex": true, "exists": true, "empty": true, "not_empty": true},
	"body":    {"eq": true, "contains": true, "not_contains": true, "regex": true, "empty": true, "not_empty": true},
	"json":    {"eq": true, "ne": true, "contains": true, "not_contains": true, "gt": true, "gte": true, "lt": true, "lte": true, "regex": true, "exists": true, "empty": true, "not_empty": true},
}

// AssertionNeedsValue reports whether op compares against Value.
func AssertionNeedsValue(op string) bool {
	return op != "exists" && op != "empty" && op != "not_empty"
}

// IsNumericOp reports whether op is an ordering comparison.
func IsNumericOp(op string) bool {
	return op == "gt" || op == "gte" || op == "lt" || op == "lte"
}

func validateAssertions(list []Assertion, fe FieldErrors) {
	if len(list) > MaxAssertions {
		fe["settings.assertions"] = fmt.Sprintf("at most %d assertions", MaxAssertions)
		return
	}
	for i, a := range list {
		key := fmt.Sprintf("settings.assertions.%d", i)
		ops, ok := assertionOps[a.Source]
		if !ok {
			fe[key+".source"] = "one of status, latency, header, body, json"
			continue
		}
		if !ops[a.Op] {
			fe[key+".op"] = "not valid for " + a.Source
			continue
		}
		if (a.Source == "header" || a.Source == "json") && a.Property == "" {
			fe[key+".property"] = "required"
		}
		if len(a.Property) > 256 {
			fe[key+".property"] = "at most 256 characters"
		}
		if len(a.Value) > 1000 {
			fe[key+".value"] = "at most 1000 characters"
		}
		if AssertionNeedsValue(a.Op) && a.Value == "" && a.Op != "eq" && a.Op != "ne" {
			fe[key+".value"] = "required"
		}
		if (a.Source == "status" || a.Source == "latency" || IsNumericOp(a.Op)) && AssertionNeedsValue(a.Op) {
			if _, err := strconv.ParseFloat(a.Value, 64); err != nil {
				fe[key+".value"] = "must be a number"
			}
		}
		if a.Op == "regex" {
			if _, err := regexp.Compile(a.Value); err != nil {
				fe[key+".value"] = "invalid regular expression"
			}
		}
	}
}
