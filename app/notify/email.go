package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"

	"goravel/app/models"
)

// fromName is the display name every email is sent under.
const fromName = "Echowa"

// smtpSender sends through an SMTP server: TLS from the start (usually port
// 465), STARTTLS (usually 587), or plain on loopback only.
type smtpSender struct {
	config   models.NotificationConfig
	password string
}

func (s *smtpSender) Send(ctx context.Context, m Message) error {
	cfg := s.config
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(Timeout)
	}
	_ = conn.SetDeadline(deadline)
	if cfg.Security == "tls" {
		conn = tls.Client(conn, &tls.Config{ServerName: cfg.Host})
	}
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("connect: %w", timedOut(err))
	}
	defer c.Close()

	if host, err := os.Hostname(); err == nil {
		if err := c.Hello(host); err != nil {
			return fmt.Errorf("greet the server: %w", timedOut(err))
		}
	}
	if cfg.Security == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("the server doesn't offer STARTTLS. Choose TLS, or check the port")
		}
		if err := c.StartTLS(&tls.Config{ServerName: cfg.Host}); err != nil {
			return fmt.Errorf("start TLS: %w", timedOut(err))
		}
	}
	if cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", cfg.Username, s.password, cfg.Host)); err != nil {
			return fmt.Errorf("sign in: %w", timedOut(err))
		}
	}
	if err := c.Mail(cfg.From); err != nil {
		return fmt.Errorf("send from %s: %w", cfg.From, timedOut(err))
	}
	for _, to := range cfg.To {
		if err := c.Rcpt(to); err != nil {
			return fmt.Errorf("send to %s: %w", to, timedOut(err))
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("send: %w", timedOut(err))
	}
	if _, err := w.Write(emailMessage(cfg.From, cfg.To, m)); err != nil {
		return fmt.Errorf("send: %w", timedOut(err))
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("send: %w", timedOut(err))
	}

	return c.Quit()
}

func timedOut(err error) error {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return fmt.Errorf("the server didn't answer within %s", Timeout)
	}

	return err
}

// emailMessage is m as a multipart/alternative email, text and HTML.
func emailMessage(from string, to []string, m Message) []byte {
	var b bytes.Buffer
	boundary := randomHex(12)
	header := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	header("From", (&mail.Address{Name: fromName, Address: from}).String())
	header("To", strings.Join(to, ", "))
	header("Subject", mime.QEncoding.Encode("utf-8", emailSubject(m)))
	header("Date", time.Now().Format(time.RFC1123Z))
	header("Message-ID", "<"+randomHex(16)+"@echowa>")
	header("MIME-Version", "1.0")
	header("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n")
	for _, part := range []struct{ kind, body string }{{"text/plain", emailText(m)}, {"text/html", emailHTML(m)}} {
		fmt.Fprintf(&b, "--%s\r\nContent-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", boundary, part.kind)
		w := quotedprintable.NewWriter(&b)
		_, _ = w.Write([]byte(part.body))
		_ = w.Close()
		b.WriteString("\r\n")
	}
	fmt.Fprintf(&b, "--%s--\r\n", boundary)

	return b.Bytes()
}

func emailSubject(m Message) string {
	return "[Echowa] " + m.Title
}

func emailText(m Message) string {
	lines := []string{m.Title, "", m.Summary}
	if len(m.Fields) > 0 {
		lines = append(lines, "")
		for _, f := range m.Fields {
			lines = append(lines, f.Label+": "+f.Value)
		}
	}
	if m.URL != "" {
		lines = append(lines, "", "Open in Echowa: "+m.URL)
	}

	return strings.Join(lines, "\r\n") + "\r\n"
}

var emailTemplate = template.Must(template.New("email").Parse(`<!doctype html>
<html><body style="margin:0;padding:24px;background:#f4f4f5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;color:#18181b">
<table role="presentation" width="100%" style="max-width:560px;margin:0 auto;background:#ffffff;border:1px solid #e4e4e7;border-radius:8px;border-top:4px solid {{.Colour}}">
<tr><td style="padding:24px">
<h1 style="margin:0 0 8px;font-size:18px">{{.Title}}</h1>
<p style="margin:0 0 16px;font-size:14px;line-height:1.5;color:#3f3f46">{{.Summary}}</p>
{{if .Fields}}<table role="presentation" style="font-size:13px;margin:0 0 16px">{{range .Fields}}<tr><td style="padding:2px 16px 2px 0;color:#71717a">{{.Label}}</td><td style="padding:2px 0">{{.Value}}</td></tr>{{end}}</table>{{end}}
{{if .URL}}<a href="{{.URL}}" style="display:inline-block;padding:8px 14px;background:#18181b;color:#ffffff;border-radius:6px;font-size:13px;text-decoration:none">Open in Echowa</a>{{end}}
</td></tr></table>
</body></html>
`))

func emailHTML(m Message) string {
	var b bytes.Buffer
	_ = emailTemplate.Execute(&b, struct {
		Message
		Colour string
	}{m, fmt.Sprintf("#%06X", discordColour(m.Severity))})

	return b.String()
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}

// resendURL is Resend's send endpoint. Tests replace it.
var resendURL = "https://api.resend.com/emails"

// resend sends through Resend's API, from an address on a domain verified
// there.
type resend struct {
	apiKey string
	from   string
	to     []string
}

func (r *resend) Send(ctx context.Context, m Message) error {
	return postJSON(ctx, resendURL, map[string]any{
		"from":    (&mail.Address{Name: fromName, Address: r.from}).String(),
		"to":      r.to,
		"subject": emailSubject(m),
		"text":    emailText(m),
		"html":    emailHTML(m),
	}, http.Header{"Authorization": {"Bearer " + r.apiKey}})
}
