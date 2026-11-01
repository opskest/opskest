package notify

import (
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
)

func TestBuildMailNoHeaderInjection(t *testing.T) {
	msg := string(buildMail("status@example.com", "a@example.com",
		"Outage\r\nBcc: victim@example.com", "body", "<p>body</p>", "https://x/unsub?token=t"))
	head := msg[:strings.Index(msg, "\r\n\r\n")]
	for _, line := range strings.Split(head, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "bcc:") {
			t.Fatalf("injected header: %q\n%s", line, head)
		}
	}
	if !strings.Contains(head, "List-Unsubscribe: <https://x/unsub?token=t>") {
		t.Errorf("missing List-Unsubscribe:\n%s", head)
	}
}

func TestSlackEscapesMarkup(t *testing.T) {
	if got := slackEscape("<!channel> & <http://x|y>"); strings.ContainsAny(got, "<>") {
		t.Fatalf("slackEscape left markup: %q", got)
	}
}

// A body line that imitates a MIME boundary can't split the message: the
// boundary is random and the parts are quoted-printable.
func TestBuildMailBoundaryAndEncoding(t *testing.T) {
	body := "--opskest-sub-boundary\r\nContent-Type: text/html\r\n\r\n<script>x</script>\n" + strings.Repeat("x", 2000)
	msg := string(buildMail("status@example.com", "a@example.com", "s", body, "<p>h</p>", ""))
	for _, line := range strings.Split(msg, "\r\n") {
		if len(line) > 998 {
			t.Fatalf("line of %d chars", len(line))
		}
	}
	if !strings.Contains(msg, "Content-Transfer-Encoding: quoted-printable") || !strings.Contains(msg, "Message-ID: <") || !strings.Contains(msg, "Date: ") {
		t.Fatalf("missing headers:\n%s", msg[:400])
	}
	m, err := mail.ReadMessage(strings.NewReader(msg))
	if err != nil {
		t.Fatal(err)
	}
	_, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil || params["boundary"] == "opskest-sub-boundary" {
		t.Fatalf("boundary = %q (%v)", params["boundary"], err)
	}
	mr := multipart.NewReader(m.Body, params["boundary"])
	parts := 0
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		parts++
		if parts == 1 {
			b, _ := io.ReadAll(p) // multipart decodes quoted-printable
			if !strings.Contains(string(b), "<script>x</script>") || !strings.HasPrefix(string(b), "--opskest-sub-boundary") {
				t.Fatalf("text part not preserved verbatim: %.80q", b)
			}
		}
	}
	if parts != 2 {
		t.Fatalf("message has %d parts, want 2 (body injected a part?)", parts)
	}
}
