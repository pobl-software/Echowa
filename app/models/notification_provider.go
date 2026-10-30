package models

import (
	"encoding/json"
	"fmt"

	"github.com/goravel/framework/database/orm"
)

// NotificationKind is where a provider sends: one of the six kinds ADR
// 0040 lists.
type NotificationKind string

const (
	NotifySlack   NotificationKind = "slack"
	NotifyDiscord NotificationKind = "discord"
	NotifyTeams   NotificationKind = "teams"
	NotifyNtfy    NotificationKind = "ntfy"
	NotifySMTP    NotificationKind = "smtp"
	NotifyResend  NotificationKind = "resend"
)

// NotificationKinds lists them in the order the page shows them.
var NotificationKinds = []NotificationKind{NotifySlack, NotifyDiscord, NotifyTeams, NotifyNtfy, NotifySMTP, NotifyResend}

// NotificationProvider is where notifications go, and nothing else: which
// notifications go there is the alert rules' business (ADR 0040).
type NotificationProvider struct {
	orm.Model
	Name   string             `json:"name"`
	Kind   NotificationKind   `json:"kind"`
	Config NotificationConfig `gorm:"serializer:json" json:"config"`
	// Secrets holds NotificationSecrets as JSON. Never serialised: props
	// end up in the page HTML and JSON.
	Secrets Secret `json:"-"`
	Enabled bool   `json:"enabled"`
}

// NotificationConfig is a provider's plain settings. Each kind uses some.
type NotificationConfig struct {
	// Server is ntfy's base URL.
	Server string `json:"server,omitempty"`
	// Host, Port, Security and Username are SMTP's. Security is starttls,
	// tls or none, and none is only allowed on loopback.
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Security string `json:"security,omitempty"`
	Username string `json:"username,omitempty"`
	// From and To are the email kinds'.
	From string   `json:"from,omitempty"`
	To   []string `json:"to,omitempty"`
}

// NotificationSecrets is what a provider keeps encrypted. A Slack, Discord
// or Teams webhook URL is a secret, and so is an ntfy topic: on ntfy.sh
// its name is the only protection.
type NotificationSecrets struct {
	WebhookURL string `json:"webhook_url,omitempty"`
	Topic      string `json:"topic,omitempty"`
	Token      string `json:"token,omitempty"`
	Password   string `json:"password,omitempty"`
	APIKey     string `json:"api_key,omitempty"`
}

// SecretValues decodes Secrets.
func (p *NotificationProvider) SecretValues() (NotificationSecrets, error) {
	var s NotificationSecrets
	if p.Secrets == "" {
		return s, nil
	}
	if err := json.Unmarshal([]byte(p.Secrets), &s); err != nil {
		return s, fmt.Errorf("notification provider %d: decode secrets: %w", p.ID, err)
	}

	return s, nil
}

// SetSecrets encodes s into Secrets.
func (p *NotificationProvider) SetSecrets(s NotificationSecrets) {
	b, _ := json.Marshal(s)
	p.Secrets = Secret(b)
}
