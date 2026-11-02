package alert

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"opskest/internal/store"
)

// A channel can carry a text template that replaces the body of a
// single-monitor alert. The template is plain text with a fixed set of
// {placeholders}; it is expanded by string replacement and nothing else, so
// it cannot call functions, read other data or run code, and a value that
// itself looks like a placeholder is not expanded again.

// MaxTemplateLen bounds a channel's template, in characters.
const MaxTemplateLen = 1000

// templateFieldMax bounds each substituted value; templateOutMax the result.
const (
	templateFieldMax = 500
	templateOutMax   = 2000
)

// TemplatePlaceholders lists what a template may use.
var TemplatePlaceholders = []string{"name", "status", "reason", "duration", "url", "time"}

var placeholderRe = regexp.MustCompile(`\{([a-z_]+)\}`)

// TemplateProblem reports why tpl can't be used, or "" when it can: too long,
// or naming a placeholder that does not exist (usually a typo).
func TemplateProblem(tpl string) string {
	if utf8.RuneCountInString(tpl) > MaxTemplateLen {
		return "at most 1000 characters"
	}
	for _, m := range placeholderRe.FindAllStringSubmatch(tpl, -1) {
		known := false
		for _, p := range TemplatePlaceholders {
			if m[1] == p {
				known = true
				break
			}
		}
		if !known {
			return "unknown placeholder {" + m[1] + "}; use " + placeholderList()
		}
	}
	return ""
}

func placeholderList() string {
	var b strings.Builder
	for i, p := range TemplatePlaceholders {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString("{" + p + "}")
	}
	return b.String()
}

// oneLine collapses line breaks in a value and bounds its length, so a
// monitor name or failure reason cannot reshape the message around it.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return truncateRunes(s, templateFieldMax)
}

// RenderTemplate expands tpl for one item of m. It never fails: a placeholder
// without a value expands to nothing.
func RenderTemplate(tpl string, m Message, it Item) string {
	at := m.At
	if m.Loc != nil {
		at = at.In(m.Loc)
	}
	status := it.Status
	if status == "" {
		status = string(m.Kind)
	}
	duration := ""
	if it.Duration > 0 {
		duration = formatDuration(it.Duration)
	}
	r := strings.NewReplacer(
		"{name}", oneLine(it.Name),
		"{status}", oneLine(status),
		"{reason}", oneLine(it.Reason),
		"{duration}", duration,
		"{url}", oneLine(it.URL),
		"{time}", at.Format("2006-01-02 15:04:05 MST"),
	)
	return truncateRunes(strings.TrimSpace(r.Replace(tpl)), templateOutMax)
}

// withChannelTemplate returns m with the channel's template applied when it
// has one and m is a single-monitor alert (a storm's grouped message keeps
// its listing).
func withChannelTemplate(cfg store.ChannelConfig, m Message) Message {
	if cfg.Template == "" || m.Grouped || len(m.Items) != 1 {
		return m
	}
	m.Custom = RenderTemplate(cfg.Template, m, m.Items[0])
	return m
}
