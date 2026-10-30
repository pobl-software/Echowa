package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/notify"
	"goravel/app/rules"
)

// NotificationProviderInput is a validated provider form
// (rules.NotificationProvider). Each secret is nil to keep the stored one.
type NotificationProviderInput struct {
	Name       string                  `form:"name" json:"name"`
	Kind       models.NotificationKind `form:"kind" json:"kind"`
	WebhookURL *string                 `form:"webhook_url" json:"webhook_url"`
	Server     string                  `form:"server" json:"server"`
	Topic      *string                 `form:"topic" json:"topic"`
	Token      *string                 `form:"token" json:"token"`
	Host       string                  `form:"host" json:"host"`
	Port       int                     `form:"port" json:"port"`
	Security   string                  `form:"security" json:"security"`
	Username   string                  `form:"username" json:"username"`
	Password   *string                 `form:"password" json:"password"`
	From       string                  `form:"from" json:"from"`
	To         string                  `form:"to" json:"to"`
	APIKey     *string                 `form:"api_key" json:"api_key"`
}

// Apply copies the form onto p, keeping only the fields p's kind uses. A
// nil secret keeps p's. A new provider takes the form's kind; a saved one
// keeps its own.
func (in NotificationProviderInput) Apply(p *models.NotificationProvider) error {
	if p.Kind == "" {
		p.Kind = in.Kind
	}
	stored, err := p.SecretValues()
	if err != nil {
		return err
	}
	pick := func(sent *string, kept string) string {
		if sent != nil {
			return strings.TrimSpace(*sent)
		}
		return kept
	}

	var cfg models.NotificationConfig
	var secrets models.NotificationSecrets
	switch p.Kind {
	case models.NotifySlack, models.NotifyDiscord, models.NotifyTeams:
		secrets.WebhookURL = pick(in.WebhookURL, stored.WebhookURL)
	case models.NotifyNtfy:
		cfg.Server = strings.TrimSuffix(in.Server, "/")
		secrets.Topic = pick(in.Topic, stored.Topic)
		secrets.Token = pick(in.Token, stored.Token)
	case models.NotifySMTP:
		cfg.Host, cfg.Port, cfg.Security, cfg.Username = in.Host, in.Port, in.Security, in.Username
		cfg.From, cfg.To = in.From, rules.EmailList(in.To)
		secrets.Password = pick(in.Password, stored.Password)
	case models.NotifyResend:
		cfg.From, cfg.To = in.From, rules.EmailList(in.To)
		secrets.APIKey = pick(in.APIKey, stored.APIKey)
	}
	if in.Name != "" {
		p.Name = in.Name
	}
	p.Config = cfg
	p.SetSecrets(secrets)

	return nil
}

// Problem is what's wrong with the form that its rules can't see, keyed
// by field: plain SMTP anywhere but loopback, which would send the
// password and the messages in the clear, and a required secret emptied
// on update.
func (in NotificationProviderInput) Problem(p *models.NotificationProvider) map[string]string {
	kind := p.Kind
	if kind == "" {
		kind = in.Kind
	}
	if kind == models.NotifySMTP && in.Security == "none" && !rules.IsLoopbackHost(in.Host) {
		return map[string]string{"security": "Choose STARTTLS or TLS. Without encryption, the password and messages would cross the network in the clear, so it's only allowed for localhost."}
	}
	emptied := func(sent *string) bool { return sent != nil && strings.TrimSpace(*sent) == "" }
	switch {
	case (kind == models.NotifySlack || kind == models.NotifyDiscord || kind == models.NotifyTeams) && emptied(in.WebhookURL):
		return map[string]string{"webhook_url": "Enter the webhook URL."}
	case kind == models.NotifyNtfy && emptied(in.Topic):
		return map[string]string{"topic": "Enter the topic."}
	case kind == models.NotifyResend && emptied(in.APIKey):
		return map[string]string{"api_key": "Enter the API key."}
	}

	return nil
}

// NotificationProviderSummary is a row of Settings' Notifications list.
type NotificationProviderSummary struct {
	models.NotificationProvider
	// Target says where it sends without giving a secret away: a
	// webhook's host, ntfy's server, an email's recipients.
	Target string `json:"target"`
	// Stored lists the secrets it has, so the form can say which are kept.
	Stored []string `json:"stored"`
}

func NotificationProviders(ctx context.Context) ([]NotificationProviderSummary, error) {
	var providers []models.NotificationProvider
	if err := facades.Orm().WithContext(ctx).Query().Order("name").Get(&providers); err != nil {
		return nil, err
	}
	rows := make([]NotificationProviderSummary, 0, len(providers))
	for _, p := range providers {
		secrets, err := p.SecretValues()
		if err != nil {
			return nil, err
		}
		rows = append(rows, NotificationProviderSummary{NotificationProvider: p, Target: providerTarget(&p, secrets), Stored: storedSecrets(secrets)})
	}

	return rows, nil
}

func providerTarget(p *models.NotificationProvider, secrets models.NotificationSecrets) string {
	switch p.Kind {
	case models.NotifyNtfy:
		return urlHost(p.Config.Server)
	case models.NotifySMTP, models.NotifyResend:
		return strings.Join(p.Config.To, ", ")
	}

	return urlHost(secrets.WebhookURL)
}

func urlHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}

	return u.Host
}

func storedSecrets(s models.NotificationSecrets) []string {
	stored := []string{}
	for name, value := range map[string]string{"webhook_url": s.WebhookURL, "topic": s.Topic, "token": s.Token, "password": s.Password, "api_key": s.APIKey} {
		if value != "" {
			stored = append(stored, name)
		}
	}
	slices.Sort(stored)

	return stored
}

func SaveNotificationProvider(ctx context.Context, p *models.NotificationProvider, in NotificationProviderInput) error {
	if err := in.Apply(p); err != nil {
		return err
	}
	if p.ID == 0 {
		p.Enabled = true
		return facades.Orm().WithContext(ctx).Query().Create(p)
	}

	return facades.Orm().WithContext(ctx).Query().Save(p)
}

func DeleteNotificationProvider(ctx context.Context, p *models.NotificationProvider) error {
	_, err := facades.Orm().WithContext(ctx).Query().Delete(p)

	return err
}

// NotificationTest is how a Test send is going.
type NotificationTest struct {
	// State is sending, sent or failed.
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	at    time.Time
}

// notificationTests are the Test sends of the last NotificationTestKept,
// by ID. A send can take longer than the request timeout, so the request
// starts it and the dialog polls.
var notificationTests = struct {
	sync.Mutex
	byID map[string]NotificationTest
}{byID: map[string]NotificationTest{}}

const NotificationTestKept = 10 * time.Minute

// StartNotificationTest sends notify.Sample to p, which needn't be saved,
// in the background, and returns the ID NotificationTestResult knows it
// by.
func StartNotificationTest(p models.NotificationProvider) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	setNotificationTest(id, NotificationTest{State: "sending"})

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), notify.Timeout)
		defer cancel()
		name := p.Name
		if name == "" {
			name = "this provider"
		}
		err := sendTo(ctx, &p, notify.Sample(name, facades.Config().GetString("http.url")))
		if err != nil {
			setNotificationTest(id, NotificationTest{State: "failed", Error: err.Error()})
			return
		}
		setNotificationTest(id, NotificationTest{State: "sent"})
	}()

	return id
}

func sendTo(ctx context.Context, p *models.NotificationProvider, m notify.Message) error {
	sender, err := notify.Open(p)
	if err != nil {
		return err
	}

	return sender.Send(ctx, m)
}

func setNotificationTest(id string, t NotificationTest) {
	notificationTests.Lock()
	defer notificationTests.Unlock()
	now := time.Now()
	for old, kept := range notificationTests.byID {
		if now.Sub(kept.at) > NotificationTestKept {
			delete(notificationTests.byID, old)
		}
	}
	t.at = now
	notificationTests.byID[id] = t
}

func NotificationTestResult(id string) (NotificationTest, bool) {
	notificationTests.Lock()
	defer notificationTests.Unlock()
	t, ok := notificationTests.byID[id]

	return t, ok
}
