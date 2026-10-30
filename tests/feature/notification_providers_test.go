package feature

import (
	"errors"
	"fmt"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/notify"
	"goravel/tests"
)

const (
	slackWebhook = "https://hooks.slack.com/services/T000/B000/slack-secret-0123"
	ntfyTopic    = "echowa-topic-secret-x7"
	resendKey    = "re_resend-secret-0123"
	smtpPassword = "smtp-secret-0123"
)

type NotificationProvidersTestSuite struct {
	suite.Suite
	tests.TestCase
	notify *notify.Fake
}

func TestNotificationProvidersTestSuite(t *testing.T) {
	suite.Run(t, new(NotificationProvidersTestSuite))
}

func (s *NotificationProvidersTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.FakeDocker()
	s.notify = s.FakeNotify()
}

func (s *NotificationProvidersTestSuite) createSlack() models.NotificationProvider {
	p := models.NotificationProvider{Name: "Slack #ops", Kind: models.NotifySlack, Enabled: true}
	p.SetSecrets(models.NotificationSecrets{WebhookURL: slackWebhook})
	s.Require().NoError(facades.Orm().Query().Create(&p))

	return p
}

func (s *NotificationProvidersTestSuite) createNtfy() models.NotificationProvider {
	p := models.NotificationProvider{Name: "Phone", Kind: models.NotifyNtfy, Enabled: true, Config: models.NotificationConfig{Server: "https://ntfy.sh"}}
	p.SetSecrets(models.NotificationSecrets{Topic: ntfyTopic, Token: "tk_token"})
	s.Require().NoError(facades.Orm().Query().Create(&p))

	return p
}

func (s *NotificationProvidersTestSuite) load(id uint) (models.NotificationProvider, models.NotificationSecrets) {
	var p models.NotificationProvider
	s.Require().NoError(facades.Orm().Query().Where("id", id).FirstOrFail(&p))
	secrets, err := p.SecretValues()
	s.Require().NoError(err)

	return p, secrets
}

func (s *NotificationProvidersTestSuite) named(name string) (models.NotificationProvider, models.NotificationSecrets) {
	var p models.NotificationProvider
	s.Require().NoError(facades.Orm().Query().Where("name", name).FirstOrFail(&p))

	return s.load(p.ID)
}

func (s *NotificationProvidersTestSuite) errors(b *browser) map[string]any {
	_, page := b.visit("/settings")
	errs, _ := page.Props["errors"].(map[string]any)

	return errs
}

func (s *NotificationProvidersTestSuite) TestSettingsListsProvidersWithoutTheirSecrets() {
	slack := s.createSlack()
	s.createNtfy()
	b := signedIn(s.T(), &s.TestCase)
	b.forbid(slackWebhook, "slack-secret", ntfyTopic, "tk_token")

	_, page := b.visit("/settings")

	rows := page.Props["notificationProviders"].([]any)
	s.Require().Len(rows, 2)
	phone, ops := rows[0].(map[string]any), rows[1].(map[string]any)
	s.Equal(float64(slack.ID), ops["id"])
	s.Equal("slack", ops["kind"])
	s.Equal("hooks.slack.com", ops["target"])
	s.Equal([]any{"webhook_url"}, ops["stored"])
	s.Equal(true, ops["enabled"])
	s.NotContains(ops, "secrets")
	s.Equal("ntfy.sh", phone["target"])
	s.Equal([]any{"token", "topic"}, phone["stored"])
	s.Equal("https://ntfy.sh", phone["config"].(map[string]any)["server"])
}

func (s *NotificationProvidersTestSuite) TestStoreKeepsEachKindsFieldsAndEncryptsItsSecrets() {
	b := signedIn(s.T(), &s.TestCase)
	b.forbid(slackWebhook, ntfyTopic, resendKey, smtpPassword)
	b.visit("/settings")

	for _, form := range []map[string]any{
		{"name": " Slack ", "kind": "slack", "webhook_url": slackWebhook, "host": "ignored.example.com"},
		{"name": "Phone", "kind": "ntfy", "server": "https://ntfy.example.com/", "topic": ntfyTopic, "token": nil},
		{"name": "Mail", "kind": "smtp", "host": "SMTP.Example.com", "port": 587, "security": "starttls", "username": "echowa",
			"password": smtpPassword, "from": "alerts@example.com", "to": "a@example.com, b@example.com"},
		{"name": "Resend", "kind": "resend", "api_key": resendKey, "from": "alerts@example.com", "to": "a@example.com"},
	} {
		b.submit(nethttp.MethodPost, "/settings/notifications", "/settings", form).AssertHeader("Location", "/settings")
		s.Empty(s.errors(b), form["kind"])
	}

	slack, secrets := s.named("Slack")
	s.Equal(models.NotifySlack, slack.Kind)
	s.True(slack.Enabled)
	s.Equal(models.NotificationConfig{}, slack.Config, "fields of other kinds are dropped")
	s.Equal(models.NotificationSecrets{WebhookURL: slackWebhook}, secrets)
	var stored string
	s.Require().NoError(facades.DB().Table("notification_providers").Where("id", slack.ID).Value("secrets", &stored))
	s.NotContains(stored, "slack-secret")

	phone, secrets := s.named("Phone")
	s.Equal(models.NotificationConfig{Server: "https://ntfy.example.com"}, phone.Config)
	s.Equal(models.NotificationSecrets{Topic: ntfyTopic}, secrets)

	mail, secrets := s.named("Mail")
	s.Equal(models.NotificationConfig{Host: "smtp.example.com", Port: 587, Security: "starttls", Username: "echowa",
		From: "alerts@example.com", To: []string{"a@example.com", "b@example.com"}}, mail.Config)
	s.Equal(smtpPassword, secrets.Password)

	resend, secrets := s.named("Resend")
	s.Equal(models.NotificationConfig{From: "alerts@example.com", To: []string{"a@example.com"}}, resend.Config)
	s.Equal(resendKey, secrets.APIKey)
}

func (s *NotificationProvidersTestSuite) TestStoreValidatesByKind() {
	s.createSlack()
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/settings")

	for _, tc := range []struct {
		form   map[string]any
		fields []string
	}{
		{map[string]any{"name": "", "kind": "telegram"}, []string{"name", "kind"}},
		{map[string]any{"name": "Slack #ops", "kind": "slack", "webhook_url": "http://hooks.slack.com/services/x"}, []string{"name", "webhook_url"}},
		{map[string]any{"name": "Discord", "kind": "discord"}, []string{"webhook_url"}},
		{map[string]any{"name": "Phone", "kind": "ntfy", "server": "ntfy.sh", "topic": "has spaces"}, []string{"server", "topic"}},
		{map[string]any{"name": "Mail", "kind": "smtp", "host": "smtp.example.com:587", "port": 0, "security": "ssl",
			"from": "Echowa <a@example.com>", "to": "a@example.com, nope"}, []string{"host", "port", "security", "from", "to"}},
		{map[string]any{"name": "Resend", "kind": "resend", "from": "a@example.com", "to": ""}, []string{"api_key", "to"}},
	} {
		b.submit(nethttp.MethodPost, "/settings/notifications", "/settings", tc.form).AssertHeader("Location", "/settings")
		errs := s.errors(b)
		for _, field := range tc.fields {
			s.Contains(errs, field, tc.form)
		}
		s.Len(errs, len(tc.fields), errs)
	}

	count, err := facades.Orm().Query().Model(&models.NotificationProvider{}).Count()
	s.Require().NoError(err)
	s.EqualValues(1, count)
}

func (s *NotificationProvidersTestSuite) TestPlainSMTPIsOnlyForLoopback() {
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/settings")
	form := map[string]any{"name": "Mail", "kind": "smtp", "host": "smtp.example.com", "port": 25, "security": "none",
		"from": "alerts@example.com", "to": "a@example.com"}

	b.submit(nethttp.MethodPost, "/settings/notifications", "/settings", form)
	s.Contains(s.errors(b)["security"], "only allowed for localhost")

	form["host"] = "localhost"
	b.submit(nethttp.MethodPost, "/settings/notifications", "/settings", form)
	s.Empty(s.errors(b))
	mail, _ := s.named("Mail")
	s.Equal("none", mail.Config.Security)
}

func (s *NotificationProvidersTestSuite) TestUpdateKeepsSecretsSentAsNull() {
	phone := s.createNtfy()
	b := signedIn(s.T(), &s.TestCase)
	b.forbid(ntfyTopic, "tk_token", "tk_new")
	b.visit("/settings")
	url := fmt.Sprintf("/settings/notifications/%d", phone.ID)

	b.submit(nethttp.MethodPut, url, "/settings", map[string]any{"name": "Pixel", "server": "https://ntfy.example.com", "topic": nil, "token": nil}).
		AssertHeader("Location", "/settings")
	s.Empty(s.errors(b))
	updated, secrets := s.load(phone.ID)
	s.Equal("Pixel", updated.Name)
	s.Equal("https://ntfy.example.com", updated.Config.Server)
	s.Equal(models.NotificationSecrets{Topic: ntfyTopic, Token: "tk_token"}, secrets)

	b.submit(nethttp.MethodPut, url, "/settings", map[string]any{"name": "Pixel", "server": "https://ntfy.sh", "topic": nil, "token": "tk_new"})
	_, secrets = s.load(phone.ID)
	s.Equal("tk_new", secrets.Token)

	b.submit(nethttp.MethodPut, url, "/settings", map[string]any{"name": "Pixel", "server": "https://ntfy.sh", "topic": nil, "token": ""})
	_, secrets = s.load(phone.ID)
	s.Empty(secrets.Token, "an optional secret sent empty is removed")
	s.Equal(ntfyTopic, secrets.Topic)

	b.submit(nethttp.MethodPut, url, "/settings", map[string]any{"name": "Pixel", "server": "https://ntfy.sh", "topic": ""})
	s.Contains(s.errors(b), "topic", "a required secret can't be removed")
}

func (s *NotificationProvidersTestSuite) TestUpdateNeverChangesTheKind() {
	slack := s.createSlack()
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/settings")

	b.submit(nethttp.MethodPut, fmt.Sprintf("/settings/notifications/%d", slack.ID), "/settings",
		map[string]any{"name": "Slack #ops", "kind": "ntfy", "server": "https://ntfy.sh", "topic": "x", "webhook_url": nil})

	s.Empty(s.errors(b))
	updated, secrets := s.load(slack.ID)
	s.Equal(models.NotifySlack, updated.Kind)
	s.Equal(slackWebhook, secrets.WebhookURL)
}

func (s *NotificationProvidersTestSuite) TestEnabledAndDestroy() {
	slack := s.createSlack()
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/settings")
	url := fmt.Sprintf("/settings/notifications/%d", slack.ID)

	b.submit(nethttp.MethodPut, url+"/enabled", "/settings", map[string]any{"enabled": false}).AssertHeader("Location", "/settings")
	updated, _ := s.load(slack.ID)
	s.False(updated.Enabled)

	b.submit(nethttp.MethodDelete, url, "/settings", nil).AssertHeader("Location", "/settings")
	count, err := facades.Orm().Query().Model(&models.NotificationProvider{}).Count()
	s.Require().NoError(err)
	s.Zero(count)
}

// test is the dialog's Test button: it starts a send and polls for how
// it went, as plain JSON outside Inertia.
func (s *NotificationProvidersTestSuite) test(b *browser, form map[string]any, status int) map[string]any {
	resp := b.request(nethttp.MethodPost, "/settings/notifications/test", form, false, map[string]string{
		"X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie),
		"Accept":       "application/json",
	})
	result, err := resp.Json()
	s.Require().NoError(err)
	resp.AssertStatus(status)
	s.Require().False(s.T().Failed(), result)
	if status != nethttp.StatusAccepted {
		return result
	}

	var state map[string]any
	s.Eventually(func() bool {
		resp := b.request(nethttp.MethodGet, "/settings/notifications/test/"+result["id"].(string), nil, false, map[string]string{"Accept": "application/json"})
		resp.AssertStatus(nethttp.StatusOK)
		state, err = resp.Json()
		s.Require().NoError(err)
		return state["state"] != "sending"
	}, 2*time.Second, 20*time.Millisecond)

	return state
}

func (s *NotificationProvidersTestSuite) TestTestSendsASampleWithTheFormsValues() {
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/settings")

	result := s.test(b, map[string]any{"kind": "slack", "webhook_url": slackWebhook}, nethttp.StatusAccepted)

	s.Equal("sent", result["state"])
	sent := s.notify.Sent()
	s.Require().Len(sent, 1)
	s.Equal(models.NotifySlack, sent[0].Provider.Kind)
	secrets, err := sent[0].Provider.SecretValues()
	s.Require().NoError(err)
	s.Equal(slackWebhook, secrets.WebhookURL)
	s.Equal("Test notification", sent[0].Message.Title)
	s.Equal(notify.Info, sent[0].Message.Severity)
	s.Contains(sent[0].Message.URL, "/settings")
}

func (s *NotificationProvidersTestSuite) TestTestReportsTheProvidersAnswer() {
	s.notify.Err = errors.New("the provider answered 404 Not Found: no_service")
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/settings")

	result := s.test(b, map[string]any{"kind": "slack", "webhook_url": slackWebhook}, nethttp.StatusAccepted)

	s.Equal("failed", result["state"])
	s.Equal("the provider answered 404 Not Found: no_service", result["error"])
}

func (s *NotificationProvidersTestSuite) TestTestOfASavedProviderUsesItsStoredSecrets() {
	phone := s.createNtfy()
	b := signedIn(s.T(), &s.TestCase)
	b.forbid(ntfyTopic)
	b.visit("/settings")

	result := s.test(b, map[string]any{"id": phone.ID, "kind": "slack", "server": "https://ntfy.example.com", "topic": nil, "token": nil}, nethttp.StatusAccepted)

	s.Equal("sent", result["state"])
	sent := s.notify.Sent()
	s.Require().Len(sent, 1)
	s.Equal(models.NotifyNtfy, sent[0].Provider.Kind, "a saved provider keeps its kind")
	s.Equal("https://ntfy.example.com", sent[0].Provider.Config.Server)
	secrets, err := sent[0].Provider.SecretValues()
	s.Require().NoError(err)
	s.Equal(models.NotificationSecrets{Topic: ntfyTopic, Token: "tk_token"}, secrets)
	s.Contains(sent[0].Message.Summary, "Phone")
}

func (s *NotificationProvidersTestSuite) TestTestValidatesBeforeSending() {
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/settings")

	result := s.test(b, map[string]any{"kind": "smtp", "host": "smtp.example.com", "port": 25, "security": "none",
		"from": "alerts@example.com", "to": "a@example.com"}, nethttp.StatusUnprocessableEntity)
	s.Contains(result["errors"], "security")

	result = s.test(b, map[string]any{"kind": "discord", "webhook_url": ""}, nethttp.StatusUnprocessableEntity)
	errs := result["errors"].(map[string]any)
	s.Contains(errs, "webhook_url")
	s.NotContains(errs, "name", "Test doesn't need a name")
	s.Empty(s.notify.Sent())
}

func (s *NotificationProvidersTestSuite) TestAnExpiredTestSaysSo() {
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/settings")

	b.request(nethttp.MethodGet, "/settings/notifications/test/nope", nil, false, map[string]string{"Accept": "application/json"}).
		AssertStatus(nethttp.StatusNotFound)
}
