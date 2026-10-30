package feature

import (
	"context"
	"fmt"
	"maps"
	nethttp "net/http"
	"slices"
	"testing"

	"github.com/stretchr/testify/suite"

	"goravel/app/alerts"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/notify"
	"goravel/app/services"
	"goravel/app/templates"
	"goravel/tests"
)

type AlertRulesTestSuite struct {
	suite.Suite
	tests.TestCase
	app *models.App
	ops models.NotificationProvider
	db  models.NotificationProvider
}

func TestAlertRulesTestSuite(t *testing.T) {
	suite.Run(t, new(AlertRulesTestSuite))
}

func (s *AlertRulesTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.FakeDocker()
	s.FakeNotify()
	s.app = createTestApp(s.T(), "blog", nil)
	s.ops = createTestProvider(s.T(), "Slack #ops")
	s.db = createTestProvider(s.T(), "Slack #db")
}

func (s *AlertRulesTestSuite) alertsURL(app *models.App) string {
	return fmt.Sprintf("/apps/%d?tab=alerts", app.ID)
}

// rules lists every rule as "scope trigger severity [providers]".
func (s *AlertRulesTestSuite) rules() []string {
	var rules []models.AlertRule
	s.Require().NoError(facades.Orm().Query().With("Providers").Order("id").Get(&rules))
	lines := []string{}
	for _, r := range rules {
		names := []string{}
		for _, p := range r.Providers {
			names = append(names, p.Name)
		}
		slices.Sort(names)
		line := fmt.Sprintf("%s %s %s %v", r.Scope, r.Trigger, r.Severity, names)
		if r.AppID != nil {
			line = fmt.Sprintf("%s:%d %s %s %v", r.Scope, *r.AppID, r.Trigger, r.Severity, names)
		}
		if !r.Enabled {
			line += " off"
		}
		lines = append(lines, line)
	}

	return lines
}

func (s *AlertRulesTestSuite) errors(b *browser, uri string) map[string]any {
	_, page := b.visit(uri)
	errs, _ := page.Props["errors"].(map[string]any)

	return errs
}

func (s *AlertRulesTestSuite) templateApp(name, slug string) *models.App {
	tmpl, _ := templates.Find(slug)
	app, err := services.CreateApp(context.Background(), services.AppInput{
		Name: name, Repository: tmpl.Repository, DefaultTag: tmpl.DefaultTag, Port: tmpl.Port, Replicas: 1,
	}, &tmpl)
	s.Require().NoError(err)

	return app
}

func (s *AlertRulesTestSuite) TestAnAppsAlertIsAddedForThatApp() {
	b := signedIn(s.T(), &s.TestCase)
	b.visit(s.alertsURL(s.app))

	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/alerts", s.app.ID), s.alertsURL(s.app), map[string]any{
		"trigger": alerts.DeployFailed, "severity": "critical", "providers": []uint{s.ops.ID, s.db.ID},
	}).AssertHeader("Location", s.alertsURL(s.app))

	s.Equal([]string{fmt.Sprintf("app:%d deploy.failed critical [Slack #db Slack #ops]", s.app.ID)}, s.rules())
}

func (s *AlertRulesTestSuite) TestTheFormIsChecked() {
	db := s.templateApp("shop-db", "postgres")
	b := signedIn(s.T(), &s.TestCase)
	b.visit(s.alertsURL(s.app))

	for _, tc := range []struct {
		app    *models.App
		form   map[string]any
		errors map[string]string
	}{
		{s.app, map[string]any{"trigger": alerts.BackupFailed, "severity": "loud", "providers": []uint{}},
			map[string]string{"trigger": "Choose one of the listed alerts.", "providers": "Choose where to send it.", "severity": ""}},
		{s.app, map[string]any{"trigger": alerts.EchowaStarted, "severity": "info", "providers": []uint{s.ops.ID}},
			map[string]string{"trigger": "Choose one of the listed alerts."}},
		{db, map[string]any{"trigger": alerts.TaskFailed, "severity": "info", "providers": []uint{s.ops.ID}},
			map[string]string{"trigger": "Choose one of the listed alerts."}},
		{s.app, map[string]any{"trigger": alerts.DeployFailed, "severity": "info", "providers": []uint{s.ops.ID, 999}},
			map[string]string{"providers": "That provider doesn't exist any more."}},
	} {
		b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/alerts", tc.app.ID), s.alertsURL(tc.app), tc.form)
		errs := s.errors(b, s.alertsURL(tc.app))
		s.Len(errs, len(tc.errors), errs)
		for field, message := range tc.errors {
			s.Contains(errs, field, tc.form)
			if message != "" {
				s.Equal(message, errs[field], tc.form)
			}
		}
	}
	s.Empty(s.rules())
}

func (s *AlertRulesTestSuite) TestEditChangesTheSeverityAndProvidersButNeverTheTrigger() {
	rule := createTestAlertRule(s.T(), alerts.DeployFailed, s.app, notify.Critical, s.ops)
	b := signedIn(s.T(), &s.TestCase)
	b.visit(s.alertsURL(s.app))

	b.submit(nethttp.MethodPut, fmt.Sprintf("/apps/%d/alerts/%d", s.app.ID, rule.ID), s.alertsURL(s.app), map[string]any{
		"trigger": alerts.DeploySucceeded, "severity": "warning", "providers": []uint{s.db.ID},
	}).AssertHeader("Location", s.alertsURL(s.app))

	s.Equal([]string{fmt.Sprintf("app:%d deploy.failed warning [Slack #db]", s.app.ID)}, s.rules())
}

func (s *AlertRulesTestSuite) TestAConditionsFieldsAreCheckedAndSaved() {
	b := signedIn(s.T(), &s.TestCase)
	b.visit(s.alertsURL(s.app))
	memory := func(fields map[string]any) map[string]any {
		form := map[string]any{"trigger": alerts.AppMemory, "severity": "warning", "providers": []uint{s.ops.ID},
			"threshold": 85.5, "for_minutes": 5, "repeat_minutes": 30, "notify_resolved": false}
		maps.Copy(form, fields)
		return form
	}

	for _, tc := range []struct {
		form   map[string]any
		errors map[string]string
	}{
		{memory(map[string]any{"threshold": nil}), map[string]string{"threshold": "Enter a threshold."}},
		{memory(map[string]any{"threshold": 150}), map[string]string{"threshold": "At most 100%."}},
		{memory(map[string]any{"threshold": -1, "for_minutes": 2000, "repeat_minutes": nil}), map[string]string{
			"threshold": "At least 0.", "for_minutes": "At most 1440 minutes, a day.", "repeat_minutes": ""}},
	} {
		b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/alerts", s.app.ID), s.alertsURL(s.app), tc.form)
		errs := s.errors(b, s.alertsURL(s.app))
		s.Len(errs, len(tc.errors), errs)
		for field, message := range tc.errors {
			s.Contains(errs, field, tc.form)
			if message != "" {
				s.Equal(message, errs[field], tc.form)
			}
		}
	}
	s.Empty(s.rules())

	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/alerts", s.app.ID), s.alertsURL(s.app), memory(nil)).
		AssertHeader("Location", s.alertsURL(s.app))
	// A yes/no condition keeps no threshold, whatever the form sends.
	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/alerts", s.app.ID), s.alertsURL(s.app), memory(map[string]any{
		"trigger": alerts.Unhealthy, "threshold": 50, "notify_resolved": true,
	})).AssertHeader("Location", s.alertsURL(s.app))

	var saved []models.AlertRule
	s.Require().NoError(facades.Orm().Query().Order("id").Get(&saved))
	s.Require().Len(saved, 2)
	s.Require().NotNil(saved[0].Threshold)
	s.Equal(85.5, *saved[0].Threshold)
	s.Equal([]any{5, 30, false}, []any{saved[0].ForMinutes, saved[0].RepeatMinutes, saved[0].NotifyResolved})
	s.Nil(saved[1].Threshold)
	s.True(saved[1].NotifyResolved)

	b.submit(nethttp.MethodPut, fmt.Sprintf("/apps/%d/alerts/%d", s.app.ID, saved[0].ID), s.alertsURL(s.app), memory(map[string]any{
		"threshold": 70, "for_minutes": 0, "notify_resolved": true,
	})).AssertHeader("Location", s.alertsURL(s.app))
	var edited models.AlertRule
	s.Require().NoError(facades.Orm().Query().Find(&edited, saved[0].ID))
	s.Equal(70.0, *edited.Threshold)
	s.Equal(0, edited.ForMinutes)
	s.True(edited.NotifyResolved)

	_, page := b.visit(s.alertsURL(s.app))
	rule := page.Props["alerts"].(map[string]any)["rules"].([]any)[0].(map[string]any)
	s.Equal("Memory use above 70% of its limit", rule["description"])
	s.Equal("ok", rule["state"])
}

func (s *AlertRulesTestSuite) TestAnAlertIsOnlyReachedFromWhereItLives() {
	other := createTestApp(s.T(), "shop", nil)
	theirs := createTestAlertRule(s.T(), alerts.DeployFailed, other, notify.Critical, s.ops)
	everyApp := createTestAlertRule(s.T(), alerts.DeployFailed, nil, notify.Critical, s.ops)
	b := signedIn(s.T(), &s.TestCase)
	b.visit(s.alertsURL(s.app))

	for _, id := range []uint{theirs.ID, everyApp.ID} {
		b.submit(nethttp.MethodDelete, fmt.Sprintf("/apps/%d/alerts/%d", s.app.ID, id), s.alertsURL(s.app), nil).AssertNotFound()
	}
	b.submit(nethttp.MethodDelete, fmt.Sprintf("/settings/alerts/%d", theirs.ID), "/settings", nil).AssertNotFound()
	s.Len(s.rules(), 2)
}

func (s *AlertRulesTestSuite) TestTheSwitchAndDelete() {
	rule := createTestAlertRule(s.T(), alerts.DeployFailed, s.app, notify.Critical, s.ops)
	url := fmt.Sprintf("/apps/%d/alerts/%d", s.app.ID, rule.ID)
	b := signedIn(s.T(), &s.TestCase)
	b.visit(s.alertsURL(s.app))

	b.submit(nethttp.MethodPut, url+"/enabled", s.alertsURL(s.app), map[string]any{"enabled": false}).
		AssertHeader("Location", s.alertsURL(s.app))
	s.Equal([]string{fmt.Sprintf("app:%d deploy.failed critical [Slack #ops] off", s.app.ID)}, s.rules())

	b.submit(nethttp.MethodDelete, url, s.alertsURL(s.app), nil).AssertHeader("Location", s.alertsURL(s.app))
	s.Empty(s.rules())
	var p models.NotificationProvider
	s.Require().NoError(facades.Orm().Query().Find(&p, s.ops.ID))
	s.Equal("Slack #ops", p.Name, "the provider stays")
}

func (s *AlertRulesTestSuite) TestSettingsAlertsCoverTheServerOrEveryAppByTrigger() {
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/settings")

	for _, trigger := range []string{alerts.EchowaStarted, alerts.BackupFailed} {
		b.submit(nethttp.MethodPost, "/settings/alerts", "/settings", map[string]any{
			"trigger": trigger, "severity": "info", "providers": []uint{s.ops.ID},
		}).AssertHeader("Location", "/settings")
	}

	s.Equal([]string{"server echowa.started info [Slack #ops]", "all_apps backup.failed info [Slack #ops]"}, s.rules())
}

func (s *AlertRulesTestSuite) TestAnAppsSectionListsItsAlertsTheEveryAppOnesAndWhatsSuggested() {
	db := s.templateApp("shop-db", "postgres")
	own := createTestAlertRule(s.T(), alerts.BackupFailed, db, notify.Critical, s.ops)
	everyApp := createTestAlertRule(s.T(), alerts.DeployFailed, nil, notify.Critical, s.db)
	createTestAlertRule(s.T(), alerts.TaskFailed, nil, notify.Warning, s.db)
	createTestAlertRule(s.T(), alerts.EchowaStarted, nil, notify.Info, s.db)
	createTestAlertRule(s.T(), alerts.DeployFailed, s.app, notify.Critical, s.ops)
	sent := models.Notification{AlertRuleID: &own.ID, NotificationProviderID: s.ops.ID, AppID: &db.ID, Trigger: alerts.BackupFailed,
		Severity: "critical", Title: "Backup of shop-db failed", Summary: "The dump exited with status 1.", Status: models.NotificationSent, Attempts: 1}
	s.Require().NoError(facades.Orm().Query().Create(&sent))
	b := signedIn(s.T(), &s.TestCase)
	b.forbid("hooks.slack.com/services")

	_, page := b.visit(s.alertsURL(db))

	section := page.Props["alerts"].(map[string]any)
	rules := section["rules"].([]any)
	s.Require().Len(rules, 1)
	rule := rules[0].(map[string]any)
	s.Equal(float64(own.ID), rule["id"])
	s.Equal("Backup failed or skipped", rule["label"])
	s.Equal([]any{map[string]any{"id": float64(s.ops.ID), "name": "Slack #ops", "kind": "slack", "enabled": true}}, rule["providers"])

	everyApps := section["every_app"].([]any)
	s.Require().Len(everyApps, 1, "a custom app's task alert doesn't cover a database")
	s.Equal(float64(everyApp.ID), everyApps[0].(map[string]any)["id"])

	var suggested []string
	for _, t := range section["suggestions"].([]any) {
		suggested = append(suggested, t.(map[string]any)["id"].(string))
	}
	s.Equal([]string{alerts.Unhealthy, alerts.KilledForMemory, alerts.AppCPU, alerts.AppMemory, alerts.DBConnections,
		alerts.LongestTransaction, alerts.TransactionIDAge}, suggested, "the deploy and backup ones are covered")

	var triggers []string
	for _, t := range section["triggers"].([]any) {
		triggers = append(triggers, t.(map[string]any)["id"].(string))
	}
	s.Contains(triggers, alerts.RestoreSucceeded)
	s.NotContains(triggers, alerts.TaskFailed)
	s.NotContains(triggers, alerts.EchowaStarted)

	recent := section["recent"].([]any)
	s.Require().Len(recent, 1)
	s.Equal("Backup of shop-db failed", recent[0].(map[string]any)["title"])
	s.Equal("Slack #ops", recent[0].(map[string]any)["provider"].(map[string]any)["name"])
	s.Len(section["providers"].([]any), 2)
}

func (s *AlertRulesTestSuite) TestATaskAlertIsSuggestedOnlyForAnAppWithTasks() {
	suggestions := func() []string {
		section, err := services.AppAlertsSection(context.Background(), s.app)
		s.Require().NoError(err)
		var ids []string
		for _, t := range section.Suggestions {
			ids = append(ids, t.ID)
		}
		return ids
	}
	s.NotContains(suggestions(), alerts.TaskFailed)

	task := models.ScheduledTask{AppID: s.app.ID, Name: "Scheduler", Schedule: "* * * * *", Timezone: "UTC", Command: "true", TimeoutMinutes: 1, Enabled: true}
	s.Require().NoError(facades.Orm().Query().Create(&task))

	s.Equal([]string{alerts.DeployFailed, alerts.Unhealthy, alerts.KilledForMemory, alerts.AppCPU, alerts.AppMemory, alerts.TaskFailed}, suggestions())
}

func (s *AlertRulesTestSuite) TestSuggestionsAreAddedAtTheirSeverity() {
	b := signedIn(s.T(), &s.TestCase)
	b.visit(s.alertsURL(s.app))

	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/alerts/suggested", s.app.ID), s.alertsURL(s.app), map[string]any{
		"triggers": []string{alerts.DeployFailed, alerts.DeploySucceeded}, "providers": []uint{s.ops.ID},
	})
	s.Contains(s.errors(b, s.alertsURL(s.app)), "triggers.1", "deploy succeeded isn't suggested")
	s.Empty(s.rules())

	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/alerts/suggested", s.app.ID), s.alertsURL(s.app), map[string]any{
		"triggers": []string{alerts.DeployFailed, alerts.KilledForMemory}, "providers": []uint{s.ops.ID, s.db.ID},
	}).AssertHeader("Location", s.alertsURL(s.app))

	s.Equal([]string{
		fmt.Sprintf("app:%d deploy.failed critical [Slack #db Slack #ops]", s.app.ID),
		fmt.Sprintf("app:%d app.oom warning [Slack #db Slack #ops]", s.app.ID),
	}, s.rules())
}

func (s *AlertRulesTestSuite) TestSettingsSuggestsTheServersAndEveryAppsAlerts() {
	b := signedIn(s.T(), &s.TestCase)
	_, page := b.visit("/settings")
	var suggested []string
	for _, t := range page.Props["alerts"].(map[string]any)["suggestions"].([]any) {
		suggested = append(suggested, t.(map[string]any)["id"].(string))
	}
	s.Equal([]string{
		alerts.EchowaStarted, alerts.ServerCPU, alerts.ServerMemory, alerts.DiskUsed, alerts.DockerUnreachable,
		alerts.DeployFailed, alerts.Unhealthy, alerts.KilledForMemory, alerts.AppCPU, alerts.AppMemory,
		alerts.DBConnections, alerts.LongestTransaction, alerts.TransactionIDAge, alerts.BackupFailed, alerts.TaskFailed,
	}, suggested)

	b.submit(nethttp.MethodPost, "/settings/alerts/suggested", "/settings", map[string]any{
		"triggers": []string{alerts.EchowaStarted, alerts.DiskUsed, alerts.DeployFailed, alerts.Unhealthy}, "providers": []uint{s.ops.ID},
	}).AssertHeader("Location", "/settings")

	s.Equal([]string{
		"server echowa.started info [Slack #ops]",
		"server server.disk critical [Slack #ops]",
		"all_apps deploy.failed critical [Slack #ops]",
		"all_apps app.unhealthy critical [Slack #ops]",
	}, s.rules())
	var disk models.AlertRule
	s.Require().NoError(facades.Orm().Query().Where("trigger", alerts.DiskUsed).First(&disk))
	s.Require().NotNil(disk.Threshold)
	s.Equal(90.0, *disk.Threshold, "a condition starts at its usual threshold and minutes")
	s.Equal(5, disk.ForMinutes)
	s.True(disk.NotifyResolved)

	b.submit(nethttp.MethodPost, "/settings/alerts/suggested", "/settings", map[string]any{
		"triggers": suggested[1:], "providers": []uint{s.ops.ID},
	})
	s.NotEmpty(s.errors(b, "/settings"), "the ones just added aren't suggested any more")
	remaining := slices.DeleteFunc(slices.Clone(suggested), func(id string) bool {
		return slices.Contains([]string{alerts.EchowaStarted, alerts.DiskUsed, alerts.DeployFailed, alerts.Unhealthy}, id)
	})
	b.submit(nethttp.MethodPost, "/settings/alerts/suggested", "/settings", map[string]any{
		"triggers": remaining, "providers": []uint{s.ops.ID},
	}).AssertHeader("Location", "/settings")
	_, page = b.visit("/settings")
	s.Empty(page.Props["alerts"].(map[string]any)["suggestions"])
	section, err := services.AppAlertsSection(context.Background(), s.app)
	s.Require().NoError(err)
	s.Empty(section.Suggestions, "a new app needs nothing")
}

func (s *AlertRulesTestSuite) TestAProviderSaysWhichAlertsUseItAndHowItsLastSendWent() {
	createTestAlertRule(s.T(), alerts.DeployFailed, nil, notify.Critical, s.ops)
	createTestAlertRule(s.T(), alerts.DeployFailed, s.app, notify.Critical, s.ops, s.db)
	createTestAlertRule(s.T(), alerts.EchowaStarted, nil, notify.Info, s.ops)
	failure := "the provider answered 404 Not Found: no_service"
	s.Require().NoError(facades.Orm().Query().Create(&models.Notification{NotificationProviderID: s.ops.ID, Trigger: alerts.DeployFailed,
		Severity: "critical", Title: "Deploy of blog failed", Status: models.NotificationFailed, Attempts: 1, Error: &failure}))
	b := signedIn(s.T(), &s.TestCase)

	_, page := b.visit("/settings")

	rows := page.Props["notificationProviders"].([]any)
	db, ops := rows[0].(map[string]any), rows[1].(map[string]any)
	s.Equal([]any{"Deploy failed or rolled back, every app", "Deploy failed or rolled back, blog", "Echowa started, the server"}, ops["used_by"])
	s.Equal([]any{"Deploy failed or rolled back, blog"}, db["used_by"])
	last := ops["last"].(map[string]any)
	s.Equal("failed", last["status"])
	s.Equal(failure, last["error"])
	s.Nil(db["last"])

	b.submit(nethttp.MethodDelete, fmt.Sprintf("/settings/notifications/%d", s.db.ID), "/settings", nil)
	s.Contains(s.rules(), fmt.Sprintf("app:%d deploy.failed critical [Slack #ops]", s.app.ID), "the alert keeps its other provider")
}
