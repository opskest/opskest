package model

import (
	"fmt"
	"strconv"
	"strings"
)

// codeRange is an inclusive [lo, hi] range of HTTP status codes.
type codeRange struct{ lo, hi int }

// CodeSet is a parsed "accepted codes" spec such as "200-299,301, 404".
type CodeSet struct {
	ranges []codeRange
}

// ParseCodes parses a comma-separated list of HTTP status codes and
// ranges, e.g. "200-299,301, 404". Each bound must be in 100..599 and
// lo <= hi for a range. An empty string defaults to "200-299".
func ParseCodes(s string) (CodeSet, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		s = "200-299"
	}
	parts := strings.Split(s, ",")
	var cs CodeSet
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return CodeSet{}, fmt.Errorf("model: empty code entry in %q", s)
		}
		if strings.Contains(p, "-") {
			bounds := strings.SplitN(p, "-", 2)
			if len(bounds) != 2 {
				return CodeSet{}, fmt.Errorf("model: invalid range %q", p)
			}
			lo, err := parseCode(bounds[0])
			if err != nil {
				return CodeSet{}, err
			}
			hi, err := parseCode(bounds[1])
			if err != nil {
				return CodeSet{}, err
			}
			if lo > hi {
				return CodeSet{}, fmt.Errorf("model: invalid range %q: lo > hi", p)
			}
			cs.ranges = append(cs.ranges, codeRange{lo, hi})
		} else {
			c, err := parseCode(p)
			if err != nil {
				return CodeSet{}, err
			}
			cs.ranges = append(cs.ranges, codeRange{c, c})
		}
	}
	return cs, nil
}

func parseCode(s string) (int, error) {
	s = strings.TrimSpace(s)
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("model: invalid status code %q", s)
	}
	if n < 100 || n > 599 {
		return 0, fmt.Errorf("model: status code %d out of range 100..599", n)
	}
	return n, nil
}

// Match reports whether code falls within any range of the set.
func (c CodeSet) Match(code int) bool {
	for _, r := range c.ranges {
		if code >= r.lo && code <= r.hi {
			return true
		}
	}
	return false
}

// String renders the set back in "lo-hi,code" form.
func (c CodeSet) String() string {
	parts := make([]string, 0, len(c.ranges))
	for _, r := range c.ranges {
		if r.lo == r.hi {
			parts = append(parts, strconv.Itoa(r.lo))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", r.lo, r.hi))
		}
	}
	return strings.Join(parts, ",")
}
