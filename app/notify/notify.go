// Package notify sends a message to a notification provider: Slack,
// Discord, Microsoft Teams, ntfy, SMTP or Resend (ADR 0040). Each kind only
// formats a Message. Which messages are sent, and when, is decided
// elsewhere.
package notify

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"goravel/app/models"
)

// Timeout is how long one attempt may take.
const Timeout = 10 * time.Second

type Severity string

const (
	Info     Severity = "info"
	Warning  Severity = "warning"
	Critical Severity = "critical"
	Resolved Severity = "resolved"
)

// Message is everything a notification says. Every kind shows all of it,
// in its own format.
type Message struct {
	Severity Severity
	Title    string
	Summary  string
	Fields   []Field
	// URL is the page in Echowa it's about. Empty leaves the link out.
	URL string
}

type Field struct {
	Label string
	Value string
}

type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Open returns the provider's sender. Tests replace it.
var Open = open

func open(p *models.NotificationProvider) (Sender, error) {
	secrets, err := p.SecretValues()
	if err != nil {
		return nil, err
	}
	switch p.Kind {
	case models.NotifySlack:
		return &slack{url: secrets.WebhookURL}, nil
	case models.NotifyDiscord:
		return &discord{url: secrets.WebhookURL}, nil
	case models.NotifyTeams:
		return &teams{url: secrets.WebhookURL}, nil
	case models.NotifyNtfy:
		return &ntfy{server: p.Config.Server, topic: secrets.Topic, token: secrets.Token}, nil
	case models.NotifySMTP:
		return &smtpSender{config: p.Config, password: secrets.Password}, nil
	case models.NotifyResend:
		return &resend{apiKey: secrets.APIKey, from: p.Config.From, to: p.Config.To}, nil
	}

	return nil, fmt.Errorf("notification provider %d: unknown kind %q", p.ID, p.Kind)
}

// Sample is what Test sends to a provider called name. url is Echowa's
// own, from APP_URL.
func Sample(name, url string) Message {
	host, _ := os.Hostname()
	m := Message{
		Severity: Info,
		Title:    "Test notification",
		Summary:  "If you can read this, Echowa can send to " + name + ".",
		Fields:   []Field{{Label: "Server", Value: host}, {Label: "Sent", Value: time.Now().UTC().Format("2 Jan 2006 15:04 UTC")}},
	}
	if url != "" {
		m.URL = strings.TrimSuffix(url, "/") + "/settings"
	}

	return m
}

// emoji marks a severity where a kind has no colour.
func (s Severity) emoji() string {
	switch s {
	case Critical:
		return "🔴"
	case Warning:
		return "🟠"
	case Resolved:
		return "🟢"
	}

	return "🔵"
}

// truncate cuts s to at most n bytes at a whole character, with an
// ellipsis when it cut.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}

	return s[:cut] + "…"
}
