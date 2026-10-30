package notify

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/models"
)

var message = Message{
	Severity: Critical,
	Title:    "shop: deploy failed",
	Summary:  "Swarm rolled the update back <fast>.",
	Fields:   []Field{{Label: "Image", Value: "ghcr.io/acme/shop:v2"}, {Label: "Took", Value: "41s"}},
	URL:      "https://echowa.example.com/apps/3?tab=deployments",
}

// received is a request a provider's test server saw.
type received struct {
	path   string
	header http.Header
	body   map[string]any
}

// server answers every request with status and body, and records it.
func server(t *testing.T, status int, body string) (*httptest.Server, *[]received) {
	t.Helper()
	var got []received
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		require.NoError(t, json.Unmarshal(raw, &parsed), string(raw))
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		got = append(got, received{path: r.URL.Path, header: r.Header, body: parsed})
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return srv, &got
}

func provider(kind models.NotificationKind, config models.NotificationConfig, secrets models.NotificationSecrets) *models.NotificationProvider {
	p := &models.NotificationProvider{Name: "test", Kind: kind, Config: config, Enabled: true}
	p.SetSecrets(secrets)

	return p
}

func send(t *testing.T, p *models.NotificationProvider) error {
	t.Helper()
	sender, err := Open(p)
	require.NoError(t, err)

	return sender.Send(context.Background(), message)
}

func TestSlack(t *testing.T) {
	srv, got := server(t, http.StatusOK, "ok")
	require.NoError(t, send(t, provider(models.NotifySlack, models.NotificationConfig{}, models.NotificationSecrets{WebhookURL: srv.URL + "/services/T0/B0/secret"})))

	require.Len(t, *got, 1)
	body := (*got)[0].body
	assert.Equal(t, "/services/T0/B0/secret", (*got)[0].path)
	assert.Equal(t, "shop: deploy failed: Swarm rolled the update back <fast>.", body["text"])
	encoded := plainJSON(body["blocks"])
	assert.Contains(t, string(encoded), `"text":"🔴 shop: deploy failed"`)
	assert.Contains(t, string(encoded), `Swarm rolled the update back &lt;fast&gt;.`)
	assert.Contains(t, string(encoded), `*Image*\nghcr.io/acme/shop:v2`)
	assert.Contains(t, string(encoded), `<https://echowa.example.com/apps/3?tab=deployments|Open in Echowa>`)
}

// plainJSON encodes v without escaping <, > and &, as the assertions
// write them.
func plainJSON(v any) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)

	return b.String()
}

func TestDiscord(t *testing.T) {
	srv, got := server(t, http.StatusNoContent, "")
	require.NoError(t, send(t, provider(models.NotifyDiscord, models.NotificationConfig{}, models.NotificationSecrets{WebhookURL: srv.URL + "/api/webhooks/1/secret"})))

	require.Len(t, *got, 1)
	body := (*got)[0].body
	assert.Equal(t, "Echowa", body["username"])
	embed := body["embeds"].([]any)[0].(map[string]any)
	assert.Equal(t, "shop: deploy failed", embed["title"])
	assert.Equal(t, message.URL, embed["url"])
	assert.EqualValues(t, 0xE5484D, embed["color"])
	assert.Len(t, embed["fields"], 2)
}

func TestTeams(t *testing.T) {
	srv, got := server(t, http.StatusAccepted, "")
	require.NoError(t, send(t, provider(models.NotifyTeams, models.NotificationConfig{}, models.NotificationSecrets{WebhookURL: srv.URL + "/workflows/secret"})))

	require.Len(t, *got, 1)
	body := (*got)[0].body
	assert.Equal(t, "message", body["type"])
	attachment := body["attachments"].([]any)[0].(map[string]any)
	assert.Equal(t, "application/vnd.microsoft.card.adaptive", attachment["contentType"])
	card := attachment["content"].(map[string]any)
	assert.Equal(t, "AdaptiveCard", card["type"])
	encoded, _ := json.Marshal(card)
	assert.Contains(t, string(encoded), `"color":"Attention"`)
	assert.Contains(t, string(encoded), `"type":"FactSet"`)
	assert.Contains(t, string(encoded), `"type":"Action.OpenUrl"`)
}

func TestNtfy(t *testing.T) {
	srv, got := server(t, http.StatusOK, `{"id":"x"}`)
	require.NoError(t, send(t, provider(models.NotifyNtfy, models.NotificationConfig{Server: srv.URL + "/"},
		models.NotificationSecrets{Topic: "echowa-alerts-x7", Token: "tk_abc"})))

	require.Len(t, *got, 1)
	r := (*got)[0]
	assert.Equal(t, "/", r.path, "the topic goes in the body, not the URL")
	assert.Equal(t, "Bearer tk_abc", r.header.Get("Authorization"))
	assert.Equal(t, "echowa-alerts-x7", r.body["topic"])
	assert.Equal(t, "shop: deploy failed", r.body["title"])
	assert.EqualValues(t, 5, r.body["priority"])
	assert.Equal(t, message.URL, r.body["click"])
	assert.Equal(t, "Swarm rolled the update back <fast>.\n\nImage: ghcr.io/acme/shop:v2\nTook: 41s", r.body["message"])
}

func TestNtfyWithoutAToken(t *testing.T) {
	srv, got := server(t, http.StatusOK, `{}`)
	require.NoError(t, send(t, provider(models.NotifyNtfy, models.NotificationConfig{Server: srv.URL},
		models.NotificationSecrets{Topic: "t"})))

	require.Len(t, *got, 1)
	assert.Empty(t, (*got)[0].header.Get("Authorization"))
}

func TestResend(t *testing.T) {
	srv, got := server(t, http.StatusOK, `{"id":"49a3999c"}`)
	resendURL = srv.URL + "/emails"
	t.Cleanup(func() { resendURL = "https://api.resend.com/emails" })

	require.NoError(t, send(t, provider(models.NotifyResend,
		models.NotificationConfig{From: "alerts@example.com", To: []string{"a@example.com", "b@example.com"}},
		models.NotificationSecrets{APIKey: "re_123"})))

	require.Len(t, *got, 1)
	r := (*got)[0]
	assert.Equal(t, "Bearer re_123", r.header.Get("Authorization"))
	assert.Equal(t, `"Echowa" <alerts@example.com>`, r.body["from"])
	assert.Equal(t, []any{"a@example.com", "b@example.com"}, r.body["to"])
	assert.Equal(t, "[Echowa] shop: deploy failed", r.body["subject"])
	assert.Contains(t, r.body["text"], "Open in Echowa: "+message.URL)
	assert.Contains(t, r.body["html"], "Swarm rolled the update back &lt;fast&gt;.")
}

// A provider's refusal says what it answered, and never the URL, which
// for a webhook is the secret.
func TestErrorsNameTheAnswerAndNeverTheURL(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		want      string
		permanent bool
		after     time.Duration
	}{
		{"slack's plain text", http.StatusNotFound, "no_service", "the provider answered 404 Not Found: no_service", true, 0},
		{"discord's JSON", http.StatusNotFound, `{"message": "Unknown Webhook", "code": 10015}`, "the provider answered 404 Not Found: Unknown Webhook", true, 0},
		{"ntfy's JSON", http.StatusUnauthorized, `{"code":40101,"http":401,"error":"unauthorized"}`, "the provider answered 401 Unauthorized: unauthorized", true, 0},
		{"a rate limit", http.StatusTooManyRequests, `{"message": "You are being rate limited."}`, "the provider answered 429 Too Many Requests: You are being rate limited. (try again in 3s)", false, 3 * time.Second},
		{"a server error", http.StatusBadGateway, "", "the provider answered 502 Bad Gateway", false, 0},
		{"a redirect, not followed", http.StatusFound, "", "the provider answered 302 Found", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := server(t, tc.status, tc.body)
			err := send(t, provider(models.NotifyDiscord, models.NotificationConfig{}, models.NotificationSecrets{WebhookURL: srv.URL + "/api/webhooks/1/hunter2"}))
			require.Error(t, err)
			assert.Equal(t, tc.want, err.Error())
			assert.Equal(t, tc.permanent, Permanent(err), "trying again can't help")
			assert.Equal(t, tc.after, RetryAfter(err))
		})
	}

	t.Run("nobody listening", func(t *testing.T) {
		srv, _ := server(t, http.StatusOK, "")
		srv.Close()
		err := send(t, provider(models.NotifySlack, models.NotificationConfig{}, models.NotificationSecrets{WebhookURL: srv.URL + "/services/hunter2"}))
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "hunter2")
		assert.Contains(t, err.Error(), "connection refused")
	})

	t.Run("a URL that doesn't parse", func(t *testing.T) {
		err := send(t, provider(models.NotifyTeams, models.NotificationConfig{}, models.NotificationSecrets{WebhookURL: "https://bad host/hunter2"}))
		require.Error(t, err)
		assert.Equal(t, "the URL isn't valid", err.Error())
	})
}

func TestTruncateCutsAtAWholeCharacter(t *testing.T) {
	assert.Equal(t, "abc", truncate("abc", 3))
	assert.Equal(t, "ab…", truncate("abcdef", 5))
	assert.Equal(t, "…", truncate("éééé", 4), "é is two bytes; one wouldn't fit beside the ellipsis")
}

// smtpServer speaks just enough SMTP to take one message on loopback, and
// records the commands and the message.
func smtpServer(t *testing.T, auth bool) (port int, commands *[]string, data *string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	var cmds []string
	var msg string
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		reply := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
		reply("220 test ESMTP")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			cmds = append(cmds, line)
			switch verb := strings.ToUpper(strings.Fields(line)[0]); verb {
			case "EHLO":
				if auth {
					reply("250-test\r\n250 AUTH PLAIN")
				} else {
					reply("250 test")
				}
			case "AUTH":
				reply("235 ok")
			case "DATA":
				reply("354 go ahead")
				var b strings.Builder
				for {
					l, err := r.ReadString('\n')
					if err != nil || l == ".\r\n" {
						break
					}
					b.WriteString(l)
				}
				msg = b.String()
				reply("250 queued")
			case "QUIT":
				reply("221 bye")
				return
			default:
				reply("250 ok")
			}
		}
	}()

	return ln.Addr().(*net.TCPAddr).Port, &cmds, &msg
}

func TestSMTP(t *testing.T) {
	port, commands, data := smtpServer(t, true)
	require.NoError(t, send(t, provider(models.NotifySMTP,
		models.NotificationConfig{Host: "127.0.0.1", Port: port, Security: "none", Username: "echowa", From: "alerts@example.com", To: []string{"a@example.com", "b@example.com"}},
		models.NotificationSecrets{Password: "pw"})))

	assert.Contains(t, *commands, "MAIL FROM:<alerts@example.com>")
	assert.Contains(t, *commands, "RCPT TO:<a@example.com>")
	assert.Contains(t, *commands, "RCPT TO:<b@example.com>")
	assert.True(t, strings.HasPrefix((*commands)[1], "AUTH PLAIN "), *commands)
	assert.Contains(t, *data, `From: "Echowa" <alerts@example.com>`)
	assert.Contains(t, *data, "To: a@example.com, b@example.com")
	assert.Contains(t, *data, "Subject: [Echowa] shop: deploy failed")
	assert.Contains(t, *data, "Content-Type: text/plain; charset=utf-8")
	assert.Contains(t, *data, "Content-Type: text/html; charset=utf-8")
}

func TestSMTPWithoutAUsernameDoesntSignIn(t *testing.T) {
	port, commands, _ := smtpServer(t, false)
	require.NoError(t, send(t, provider(models.NotifySMTP,
		models.NotificationConfig{Host: "127.0.0.1", Port: port, Security: "none", From: "alerts@example.com", To: []string{"a@example.com"}},
		models.NotificationSecrets{})))

	for _, c := range *commands {
		assert.False(t, strings.HasPrefix(c, "AUTH"), c)
	}
}

func TestSMTPStartTLSThatIsntOffered(t *testing.T) {
	port, _, _ := smtpServer(t, false)
	err := send(t, provider(models.NotifySMTP,
		models.NotificationConfig{Host: "127.0.0.1", Port: port, Security: "starttls", From: "alerts@example.com", To: []string{"a@example.com"}},
		models.NotificationSecrets{}))

	require.Error(t, err)
	assert.Equal(t, "the server doesn't offer STARTTLS. Choose TLS, or check the port", err.Error())
}

func TestASubjectOutsideASCIIIsEncoded(t *testing.T) {
	m := message
	m.Title = "café: déploiement"
	raw := string(emailMessage("a@example.com", []string{"b@example.com"}, m))
	assert.Contains(t, raw, "Subject: =?utf-8?q?")
	assert.NotContains(t, raw, "Subject: [Echowa] café")
}
