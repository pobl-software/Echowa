//go:build notify

// Sends real messages: to the dev machine's Mailpit and ntfy
// (deploy/notify), which deploy/dev-vm.sh up starts, and to each provider
// .env.notify-test at the repo root (or the environment) names. Run it on
// the Mac, where OrbStack forwards the dev machine's ports:
//
//	go test -tags notify ./app/notify/ -v
//
// Each provider gets one message per severity, a long one that has to be
// cut to fit, and, where its secret can be spoiled, a message it must
// refuse for good, with an error that doesn't show the secret. What
// reached the dev machine is read back; the rest is for reading by eye,
// each title starting "Echowa live test" and the run's ID.
package notify

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/models"
)

// live is a provider the test sends to. refused is the same with its
// secret spoiled, and secret what its errors must never show. received
// reads back the titles that arrived, where that can be done.
type live struct {
	name     string
	provider models.NotificationProvider
	secret   string
	refused  *models.NotificationProvider
	received func(t *testing.T) []string
}

func TestLiveProviders(t *testing.T) {
	run := randomHex(3)
	messages := liveMessages("Echowa live test " + run)
	for _, l := range liveProviders(t, run) {
		t.Run(l.name, func(t *testing.T) {
			sender, err := Open(&l.provider)
			require.NoError(t, err)
			for _, m := range messages {
				ctx, cancel := context.WithTimeout(context.Background(), Timeout)
				err := sender.Send(ctx, m)
				cancel()
				require.NoError(t, err, "%s: %s", m.Severity, m.Title)
			}

			if l.refused != nil {
				sender, err := Open(l.refused)
				require.NoError(t, err)
				ctx, cancel := context.WithTimeout(context.Background(), Timeout)
				err = sender.Send(ctx, messages[0])
				cancel()
				require.Error(t, err, "a spoiled secret is refused")
				assert.True(t, Permanent(err), "refused for good, so never retried: %v", err)
				spoiled, _ := l.refused.SecretValues()
				for _, s := range []string{l.secret, spoiled.WebhookURL, spoiled.APIKey, spoiled.Password, spoiled.Token} {
					if s != "" {
						assert.NotContains(t, err.Error(), s, "the error shows the secret")
					}
				}
				t.Logf("refused: %v", err)
			}

			if l.received != nil {
				titles := l.received(t)
				for _, m := range messages {
					assert.True(t, containsTitle(titles, m.Title), "%q didn't arrive", m.Title)
				}
			}
		})
	}
}

// liveMessages are what each provider gets, titled with prefix: one per
// severity, as an alert would send them, then one too long for any of them.
func liveMessages(prefix string) []Message {
	link := "http://localhost:3000/apps/1?tab=alerts"
	fields := []Field{{Label: "Server", Value: "echowa-live-test"}, {Label: "Ref", Value: "sha256:8fb11c38b355"}}
	long := strings.Repeat("A line of a deploy's output that goes on. ", 200)

	return []Message{
		{Severity: Critical, Title: prefix + ": shop: Deploy failed", Summary: "The new task exited with status 1, so Swarm rolled back to the digest that ran before.", Fields: fields, URL: link},
		{Severity: Warning, Title: prefix + ": shop: Memory use above 90% of its limit for 5 minutes", Summary: "Now 93% of its limit.", Fields: fields[:1], URL: link},
		{Severity: Info, Title: prefix + ": shop: Deploy succeeded", Summary: "It runs `sha256:8fb11c38b355` now. <b>Not bold</b> & *not italic*.", Fields: fields, URL: link},
		{Severity: Resolved, Title: prefix + ": Resolved: shop: Memory use above 90% of its limit", Summary: "Now 41% of its limit, after firing for 12 minutes.", Fields: fields[:1], URL: link},
		{Severity: Info, Title: prefix + ": a long message", Summary: long, Fields: fields},
	}
}

func containsTitle(titles []string, title string) bool {
	for _, got := range titles {
		if strings.Contains(got, title) {
			return true
		}
	}

	return false
}

func liveProviders(t *testing.T, run string) []live {
	topic := "echowa-live-" + run
	list := []live{
		{
			name: "dev ntfy",
			provider: models.NotificationProvider{Kind: models.NotifyNtfy, Name: "Dev ntfy",
				Config: models.NotificationConfig{Server: "http://localhost:8095"}},
			received: func(t *testing.T) []string { return ntfyTitles(t, "http://localhost:8095", topic) },
		},
		{
			name: "dev Mailpit",
			provider: models.NotificationProvider{Kind: models.NotifySMTP, Name: "Dev Mailpit",
				Config: models.NotificationConfig{Host: "localhost", Port: 2525, Security: "none", From: "alerts@echowa.test", To: []string{"live-" + run + "@echowa.test"}}},
			received: func(t *testing.T) []string { return mailpitSubjects(t, "live-"+run+"@echowa.test") },
		},
	}
	list[0].provider.SetSecrets(models.NotificationSecrets{Topic: topic})

	env := liveEnv(t)
	webhook := func(name string, kind models.NotificationKind, key string) {
		u := env[key]
		if u == "" {
			return
		}
		l := live{name: name, secret: u, provider: models.NotificationProvider{Kind: kind, Name: name}}
		l.provider.SetSecrets(models.NotificationSecrets{WebhookURL: u})
		refused := models.NotificationProvider{Kind: kind, Name: name}
		refused.SetSecrets(models.NotificationSecrets{WebhookURL: spoil(u)})
		l.refused = &refused
		list = append(list, l)
	}
	webhook("Slack", models.NotifySlack, "ECHOWA_TEST_NOTIFY_SLACK_URL")
	webhook("Discord", models.NotifyDiscord, "ECHOWA_TEST_NOTIFY_DISCORD_URL")
	webhook("Teams", models.NotifyTeams, "ECHOWA_TEST_NOTIFY_TEAMS_URL")

	if named := env["ECHOWA_TEST_NOTIFY_NTFY_TOPIC"]; named != "" {
		server := env["ECHOWA_TEST_NOTIFY_NTFY_SERVER"]
		if server == "" {
			server = "https://ntfy.sh"
		}
		token := env["ECHOWA_TEST_NOTIFY_NTFY_TOKEN"]
		l := live{name: "ntfy", secret: named, provider: models.NotificationProvider{Kind: models.NotifyNtfy, Name: "ntfy",
			Config: models.NotificationConfig{Server: server}}}
		l.provider.SetSecrets(models.NotificationSecrets{Topic: named, Token: token})
		if token == "" {
			l.received = func(t *testing.T) []string { return ntfyTitles(t, server, named) }
		} else {
			// Only a server that wants a token refuses a wrong one.
			l.secret = token
			refused := l.provider
			refused.SetSecrets(models.NotificationSecrets{Topic: named, Token: spoil(token)})
			l.refused = &refused
		}
		list = append(list, l)
	}

	if host := env["ECHOWA_TEST_NOTIFY_SMTP_HOST"]; host != "" {
		port, err := strconv.Atoi(env["ECHOWA_TEST_NOTIFY_SMTP_PORT"])
		require.NoError(t, err, "ECHOWA_TEST_NOTIFY_SMTP_PORT")
		cfg := models.NotificationConfig{Host: host, Port: port, Security: env["ECHOWA_TEST_NOTIFY_SMTP_SECURITY"],
			Username: env["ECHOWA_TEST_NOTIFY_SMTP_USERNAME"], From: env["ECHOWA_TEST_NOTIFY_SMTP_FROM"], To: addresses(env["ECHOWA_TEST_NOTIFY_SMTP_TO"])}
		password := env["ECHOWA_TEST_NOTIFY_SMTP_PASSWORD"]
		l := live{name: "SMTP", secret: password, provider: models.NotificationProvider{Kind: models.NotifySMTP, Name: "SMTP", Config: cfg}}
		l.provider.SetSecrets(models.NotificationSecrets{Password: password})
		if password != "" {
			refused := models.NotificationProvider{Kind: models.NotifySMTP, Name: "SMTP", Config: cfg}
			refused.SetSecrets(models.NotificationSecrets{Password: spoil(password)})
			l.refused = &refused
		}
		list = append(list, l)
	}

	if key := env["ECHOWA_TEST_NOTIFY_RESEND_API_KEY"]; key != "" {
		cfg := models.NotificationConfig{From: env["ECHOWA_TEST_NOTIFY_RESEND_FROM"], To: addresses(env["ECHOWA_TEST_NOTIFY_RESEND_TO"])}
		l := live{name: "Resend", secret: key, provider: models.NotificationProvider{Kind: models.NotifyResend, Name: "Resend", Config: cfg}}
		l.provider.SetSecrets(models.NotificationSecrets{APIKey: key})
		refused := models.NotificationProvider{Kind: models.NotifyResend, Name: "Resend", Config: cfg}
		refused.SetSecrets(models.NotificationSecrets{APIKey: spoil(key)})
		l.refused = &refused
		list = append(list, l)
	}

	return list
}

// spoil changes a secret's last characters, so the provider still knows
// where it's meant to go but refuses it.
func spoil(s string) string {
	if len(s) < 6 {
		return s + "x"
	}

	return s[:len(s)-4] + "zzzz"
}

func addresses(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}

	return out
}

// ntfyTitles reads back the titles of what the topic got in the last few
// minutes.
func ntfyTitles(t *testing.T, server, topic string) []string {
	resp, err := http.Get(strings.TrimSuffix(server, "/") + "/" + topic + "/json?poll=1&since=5m")
	require.NoError(t, err)
	defer resp.Body.Close()
	var titles []string
	lines := bufio.NewScanner(resp.Body)
	lines.Buffer(make([]byte, 64<<10), 1<<20)
	for lines.Scan() {
		var m struct {
			Title string `json:"title"`
		}
		require.NoError(t, json.Unmarshal(lines.Bytes(), &m))
		titles = append(titles, m.Title)
	}

	return titles
}

// mailpitSubjects reads back the subjects of the mail Mailpit has for to.
func mailpitSubjects(t *testing.T, to string) []string {
	var subjects []string
	deadline := time.Now().Add(5 * time.Second)
	for len(subjects) == 0 && time.Now().Before(deadline) {
		resp, err := http.Get("http://localhost:8125/api/v1/search?query=" + url.QueryEscape("to:"+to))
		require.NoError(t, err)
		var page struct {
			Messages []struct {
				Subject string `json:"Subject"`
			} `json:"messages"`
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		require.NoError(t, err)
		for _, m := range page.Messages {
			subjects = append(subjects, m.Subject)
		}
		time.Sleep(200 * time.Millisecond)
	}

	return subjects
}

// liveEnv reads ECHOWA_TEST_NOTIFY_* from .env.notify-test at the repo
// root, with the environment winning. The file is read here rather than
// passed in, so no secret goes on a command line.
func liveEnv(t *testing.T) map[string]string {
	env := map[string]string{}
	if f, err := os.Open(filepath.Join(repoRoot(t), ".env.notify-test")); err == nil {
		defer func() { _ = f.Close() }()
		lines := bufio.NewScanner(f)
		for lines.Scan() {
			line := strings.TrimSpace(lines.Text())
			key, value, ok := strings.Cut(line, "=")
			if ok && !strings.HasPrefix(line, "#") {
				env[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
			}
		}
	}
	for _, kv := range os.Environ() {
		if key, value, _ := strings.Cut(kv, "="); strings.HasPrefix(key, "ECHOWA_TEST_NOTIFY_") {
			env[key] = value
		}
	}

	return env
}

// repoRoot is the nearest directory up from here with a go.mod.
func repoRoot(t *testing.T) string {
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}
