package feature

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/goravel/framework/support/carbon"
	"github.com/stretchr/testify/suite"

	"goravel/app/alerts"
	"goravel/app/deploy"
	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/notify"
	"goravel/app/release"
	"goravel/app/services"
	"goravel/app/templates"
	"goravel/app/update"
	"goravel/tests"
)

type AlertsTestSuite struct {
	suite.Suite
	tests.TestCase
	docker *docker.Fake
	notify *notify.Fake
	app    *models.App
	ops    models.NotificationProvider
}

func TestAlertsTestSuite(t *testing.T) {
	suite.Run(t, new(AlertsTestSuite))
}

func (s *AlertsTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.docker = s.FakeDocker()
	s.notify = s.FakeNotify()
	retries, dispatch := services.NotifyRetries, deploy.Dispatch
	services.NotifyRetries = []time.Duration{0, 0}
	deploy.Dispatch = func(uint, time.Duration) error { return nil }
	s.T().Cleanup(func() {
		services.WaitForNotifications()
		services.NotifyRetries, deploy.Dispatch = retries, dispatch
	})
	s.app = createTestApp(s.T(), "blog", nil)
	s.ops = s.provider("Slack #ops")
}

func (s *AlertsTestSuite) provider(name string) models.NotificationProvider {
	p := models.NotificationProvider{Name: name, Kind: models.NotifySlack, Enabled: true}
	p.SetSecrets(models.NotificationSecrets{WebhookURL: "https://hooks.slack.com/services/T0/B0/" + strings.ReplaceAll(name, " ", "")})
	s.Require().NoError(facades.Orm().Query().Create(&p))

	return p
}

// rule makes an enabled rule on trigger for app (nil for every app, or
// the server's for a server trigger) that names providers.
func (s *AlertsTestSuite) rule(trigger string, app *models.App, severity notify.Severity, providers ...models.NotificationProvider) models.AlertRule {
	rule := models.AlertRule{Scope: models.AlertAllApps, Trigger: trigger, Severity: string(severity), Enabled: true, NotifyResolved: true}
	if t, _ := alerts.Find(trigger); t.Scope == alerts.ScopeServer {
		rule.Scope = models.AlertServer
	}
	if app != nil {
		rule.Scope, rule.AppID = models.AlertApp, &app.ID
	}
	s.Require().NoError(facades.Orm().Query().Create(&rule))
	s.Require().NoError(facades.Orm().Query().Model(&rule).Association("Providers").Append(providers))

	return rule
}

func (s *AlertsTestSuite) deploy(app *models.App, ref string) *models.Deployment {
	d, err := deploy.Request(context.Background(), app, models.TriggerManual, ref)
	s.Require().NoError(err)
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	services.WaitForNotifications()

	return d
}

func (s *AlertsTestSuite) failPulls() {
	s.docker.Pull = func(ref string, _ *docker.RegistryAuth) (string, error) {
		return "", errors.New("pull " + ref + ": manifest unknown")
	}
}

func (s *AlertsTestSuite) notifications() []models.Notification {
	var rows []models.Notification
	s.Require().NoError(facades.Orm().Query().Order("id").Get(&rows))

	return rows
}

// sent lists who was sent what, as "provider: title (severity)".
func (s *AlertsTestSuite) sent() []string {
	var lines []string
	for _, sent := range s.notify.Sent() {
		lines = append(lines, fmt.Sprintf("%s: %s (%s)", sent.Provider.Name, sent.Message.Title, sent.Message.Severity))
	}

	return lines
}

func field(m notify.Message, label string) string {
	for _, f := range m.Fields {
		if f.Label == label {
			return f.Value
		}
	}

	return ""
}

func (s *AlertsTestSuite) TestAFailedDeploySendsTheAppsRule() {
	rule := s.rule(alerts.DeployFailed, s.app, notify.Critical, s.ops)
	s.failPulls()

	d := s.deploy(s.app, "v2")

	s.Equal([]string{"Slack #ops: Deploy of blog failed (critical)"}, s.sent())
	m := s.notify.Sent()[0].Message
	s.Equal("pull ghcr.io/owner/blog:v2: manifest unknown", m.Summary)
	s.Equal(fmt.Sprintf("#%d", d.ID), field(m, "Deployment"))
	s.Equal("ghcr.io/owner/blog:v2", field(m, "Image"))
	s.NotEmpty(field(m, "Server"))
	s.True(strings.HasSuffix(m.URL, fmt.Sprintf("/apps/%d/deployments/%d", s.app.ID, d.ID)), m.URL)

	rows := s.notifications()
	s.Require().Len(rows, 1)
	n := rows[0]
	s.Equal(models.NotificationSent, n.Status)
	s.Equal(1, n.Attempts)
	s.Equal(rule.ID, *n.AlertRuleID)
	s.Equal(s.ops.ID, n.NotificationProviderID)
	s.Equal(s.app.ID, *n.AppID)
	s.Equal(alerts.DeployFailed, n.Trigger)
	s.Equal("critical", n.Severity)
	s.NotNil(n.SentAt)
	s.Nil(n.Error)
}

func (s *AlertsTestSuite) TestARolledBackDeployIsAFailedOne() {
	s.rule(alerts.DeployFailed, nil, notify.Critical, s.ops)
	s.deploy(s.app, "v1")
	s.docker.Wait = func(docker.ServiceSpec) (docker.UpdateResult, error) {
		return docker.UpdateResult{Outcome: docker.OutcomeRolledBack, Message: "task: non-zero exit (1)"}, nil
	}

	s.deploy(s.app, "v2")

	s.Equal([]string{"Slack #ops: Deploy of blog rolled back (critical)"}, s.sent())
	s.Contains(s.notify.Sent()[0].Message.Summary, "Swarm rolled the update back")
}

func (s *AlertsTestSuite) TestEveryAppRulesCoverEveryAppAndAnAppsRulesOnlyIt() {
	other := createTestApp(s.T(), "shop", nil)
	shopOnly := s.provider("Slack #shop")
	s.rule(alerts.DeploySucceeded, nil, notify.Info, s.ops)
	s.rule(alerts.DeploySucceeded, other, notify.Info, shopOnly)

	s.deploy(s.app, "v1")
	s.Equal([]string{"Slack #ops: blog deployed (info)"}, s.sent())

	s.deploy(other, "v1")
	s.ElementsMatch([]string{"Slack #ops: blog deployed (info)", "Slack #ops: shop deployed (info)", "Slack #shop: shop deployed (info)"}, s.sent())
}

func (s *AlertsTestSuite) TestAProviderNamedTwiceGetsOneMessageAtTheLouderSeverity() {
	db := s.provider("Slack #db")
	s.rule(alerts.DeployFailed, nil, notify.Warning, s.ops)
	// As loud for #db, but the app's own rule is the one it's under.
	s.rule(alerts.DeployFailed, nil, notify.Critical, db)
	louder := s.rule(alerts.DeployFailed, s.app, notify.Critical, s.ops, db)
	s.failPulls()

	s.deploy(s.app, "v2")

	s.ElementsMatch([]string{"Slack #ops: Deploy of blog failed (critical)", "Slack #db: Deploy of blog failed (critical)"}, s.sent())
	for _, n := range s.notifications() {
		s.Equal(louder.ID, *n.AlertRuleID)
	}
}

func (s *AlertsTestSuite) TestOffRulesAndProvidersSendNothing() {
	rule := s.rule(alerts.DeployFailed, nil, notify.Critical, s.ops)
	s.failPulls()
	_, err := facades.Orm().Query().Model(&rule).Update("enabled", false)
	s.Require().NoError(err)

	s.deploy(s.app, "v2")

	_, err = facades.Orm().Query().Model(&rule).Update("enabled", true)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Model(&s.ops).Update("enabled", false)
	s.Require().NoError(err)

	s.deploy(s.app, "v3")

	s.Empty(s.sent())
	s.Empty(s.notifications())
}

func (s *AlertsTestSuite) TestAFailedSendIsTriedAgain() {
	s.rule(alerts.DeployFailed, nil, notify.Critical, s.ops)
	s.failPulls()
	s.notify.Err, s.notify.Fails = errors.New("the provider didn't answer within 10s"), 2

	s.deploy(s.app, "v2")

	s.Len(s.sent(), 1)
	n := s.notifications()[0]
	s.Equal(models.NotificationSent, n.Status)
	s.Equal(3, n.Attempts)
	s.Nil(n.Error, "the error that was tried past is cleared")
}

func (s *AlertsTestSuite) TestASendThatKeepsFailingIsRecordedFailed() {
	s.rule(alerts.DeployFailed, nil, notify.Critical, s.ops)
	s.failPulls()
	s.notify.Err = errors.New("the provider didn't answer within 10s")

	s.deploy(s.app, "v2")

	n := s.notifications()[0]
	s.Equal(models.NotificationFailed, n.Status)
	s.Equal(3, n.Attempts)
	s.Equal("the provider didn't answer within 10s", *n.Error)
	s.Nil(n.SentAt)
}

func (s *AlertsTestSuite) TestAProviderThatRefusesTheMessageIsntAskedAgain() {
	s.rule(alerts.DeployFailed, nil, notify.Critical, s.ops)
	s.failPulls()
	s.notify.Err = &notify.StatusError{Code: 404}

	s.deploy(s.app, "v2")

	n := s.notifications()[0]
	s.Equal(models.NotificationFailed, n.Status)
	s.Equal(1, n.Attempts)
	s.Equal("the provider answered 404", *n.Error)
}

func (s *AlertsTestSuite) TestTheStormGuardHoldsBackPastTheBurstThenSaysHowMany() {
	s.rule(alerts.EchowaStarted, nil, notify.Info, s.ops)
	for range services.NotifyBurst + 5 {
		services.NotifyStarted()
	}
	services.WaitForNotifications()

	s.Len(s.sent(), services.NotifyBurst)
	counts := map[models.NotificationStatus]int{}
	for _, n := range s.notifications() {
		counts[n.Status]++
	}
	s.Equal(map[models.NotificationStatus]int{models.NotificationSent: services.NotifyBurst, models.NotificationSuppressed: 5}, counts)

	s.Require().NoError(services.SendHeldBack(context.Background()))
	services.WaitForNotifications()
	s.Len(s.sent(), services.NotifyBurst, "nothing until the window has room")

	// The window passes.
	_, err := facades.Orm().Query().Model(&models.Notification{}).Where("id > ?", 0).
		Update("created_at", carbon.NewDateTime(carbon.FromStdTime(time.Now().Add(-services.NotifyWindow-time.Minute))))
	s.Require().NoError(err)
	s.Require().NoError(services.SendHeldBack(context.Background()))
	s.Require().NoError(services.SendHeldBack(context.Background()))
	services.WaitForNotifications()

	sent := s.notify.Sent()
	s.Require().Len(sent, services.NotifyBurst+1, "said once")
	summary := sent[len(sent)-1].Message
	s.Equal("5 notifications held back", summary.Title)
	s.Equal(notify.Warning, summary.Severity)
	s.Contains(summary.Summary, "at most 20 notifications in 10 minutes")
}

func (s *AlertsTestSuite) killedAt(app *models.App, at time.Time) {
	s.docker.OOMKilled = map[string]time.Time{app.ServiceName(): at}
	s.Require().NoError(services.PollAppStatus(context.Background(), time.Now()))
	services.WaitForNotifications()
}

func (s *AlertsTestSuite) TestAKillForMemoryIsSentOncePerRule() {
	s.deploy(s.app, "v1")
	rule := s.rule(alerts.KilledForMemory, nil, notify.Warning, s.ops)
	// The rule was set up an hour ago.
	_, err := facades.Orm().Query().Model(&rule).Update("updated_at", carbon.NewDateTime(carbon.FromStdTime(time.Now().Add(-time.Hour))))
	s.Require().NoError(err)
	first := time.Now().Add(-5 * time.Minute)

	s.killedAt(s.app, first)
	s.Equal([]string{"Slack #ops: blog was killed for memory (warning)"}, s.sent())
	s.Equal(first.UTC().Format("2 Jan 2006 15:04 UTC"), field(s.notify.Sent()[0].Message, "Killed"))

	s.killedAt(s.app, first)
	s.Len(s.sent(), 1, "the same kill, still in Swarm's history, isn't sent again")

	s.killedAt(s.app, time.Now())
	s.Len(s.sent(), 2, "a newer kill is")

	var state models.AlertState
	s.Require().NoError(facades.Orm().Query().Where("alert_rule_id", rule.ID).FirstOrFail(&state))
	s.Equal(models.AppSubject(s.app.ID), state.Subject)
	s.NotNil(state.EventAt)
}

func (s *AlertsTestSuite) TestAKillFromBeforeTheRuleIsntSent() {
	s.deploy(s.app, "v1")
	s.rule(alerts.KilledForMemory, s.app, notify.Warning, s.ops)

	s.killedAt(s.app, time.Now().Add(-time.Hour))

	s.Empty(s.sent())
}

func (s *AlertsTestSuite) TestAFailedBackupIsSent() {
	tmpl, _ := templates.Find("postgres")
	db, err := services.CreateApp(context.Background(), services.AppInput{
		Name: "shop-db", Repository: tmpl.Repository, DefaultTag: tmpl.DefaultTag, Port: tmpl.Port, Replicas: 1,
	}, &tmpl)
	s.Require().NoError(err)
	s.FakeStorage()
	loc := models.StorageLocation{Name: "R2", Endpoint: "https://abc123.r2.cloudflarestorage.com", Region: "auto", Bucket: "echowa-backups", AccessKeyID: "AKIAEXAMPLE", SecretKey: models.Secret(storageSecret)}
	s.Require().NoError(facades.Orm().Query().Create(&loc))
	s.docker.ExecFunc = func(string, []string) ([]byte, error) { return []byte("postgres (PostgreSQL) 18.6"), nil }
	s.docker.ExecStreamFunc = func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) (int, error) {
		return 1, nil
	}
	s.rule(alerts.BackupFailed, nil, notify.Critical, s.ops)

	_, err = services.BackUpNow(db, &loc)
	s.Require().NoError(err)
	services.WaitForBackups()
	services.WaitForNotifications()

	s.Equal([]string{"Slack #ops: Backup of shop-db failed (critical)"}, s.sent())
	m := s.notify.Sent()[0].Message
	s.Equal("The dump exited with status 1.", m.Summary)
	s.Equal("Back up now", field(m, "Started by"))
	s.True(strings.HasSuffix(m.URL, fmt.Sprintf("/apps/%d?tab=backups", db.ID)), m.URL)
}

func (s *AlertsTestSuite) TestAFailedTaskRunIsSent() {
	s.docker.ExecToFunc = func(context.Context, string, []string, io.Writer) (int, error) {
		return 2, nil
	}
	task := &models.ScheduledTask{AppID: s.app.ID, Name: "Scheduler", Schedule: "* * * * *", Timezone: "UTC", Command: "php artisan schedule:run", TimeoutMinutes: 15, Enabled: true}
	s.Require().NoError(facades.Orm().Query().Create(task))
	s.rule(alerts.TaskFailed, s.app, notify.Warning, s.ops)

	_, err := services.RunTask(s.app, task)
	s.Require().NoError(err)
	services.WaitForTaskRuns()
	services.WaitForNotifications()

	s.Equal([]string{"Slack #ops: Task Scheduler on blog failed (warning)"}, s.sent())
	m := s.notify.Sent()[0].Message
	s.Equal("2", field(m, "Exit code"))
	s.Equal("The command exited with 2.", m.Summary)
}

func (s *AlertsTestSuite) TestAPruneThatRemovedImagesIsSentToTheServersRules() {
	s.rule(alerts.ImagesPruned, nil, notify.Info, s.ops)
	s.rule(alerts.DeployFailed, nil, notify.Critical, s.provider("Slack #apps"))
	s.docker.Prune = func(docker.PruneOptions) (docker.PruneReport, error) {
		return docker.PruneReport{Removed: []string{"ghcr.io/owner/blog@sha256:aa", "ghcr.io/owner/blog@sha256:bb"}, SpaceReclaimed: 300 << 20}, nil
	}

	_, err := services.RunPrune(context.Background(), services.PruneManual)
	s.Require().NoError(err)
	services.WaitForNotifications()

	s.Equal([]string{"Slack #ops: Image prune removed 2 images (info)"}, s.sent())
	s.Equal("It freed 300 MiB.", s.notify.Sent()[0].Message.Summary)
	s.Nil(s.notifications()[0].AppID)
}

func (s *AlertsTestSuite) TestEchowaStartedSaysWhenAnUpdateBroughtIt() {
	version, dir := release.Version, update.Dir
	s.T().Cleanup(func() { release.Version, update.Dir = version, dir })
	release.Version, update.Dir = "v1.2.0", s.T().TempDir()
	s.rule(alerts.EchowaStarted, nil, notify.Info, s.ops)

	services.NotifyStarted()
	services.WaitForNotifications()
	s.Require().NoError(update.WriteStatus(update.Status{State: update.Running, From: "v1.1.0", To: "v1.2.0", StartedAt: time.Now()}))
	services.NotifyStarted()
	services.WaitForNotifications()

	s.Equal([]string{"Slack #ops: Echowa started (info)", "Slack #ops: Echowa updated to v1.2.0 (info)"}, s.sent())
	s.Equal("Echowa updated from v1.1.0 and is running.", s.notify.Sent()[1].Message.Summary)
}

func (s *AlertsTestSuite) TestAnEventFromARuleThisReleaseDoesntKnowIsSkipped() {
	rule := models.AlertRule{Scope: models.AlertAllApps, Trigger: "deploy.slow", Severity: "warning", Enabled: true}
	s.Require().NoError(facades.Orm().Query().Create(&rule))
	s.Require().NoError(facades.Orm().Query().Model(&rule).Association("Providers").Append([]models.NotificationProvider{s.ops}))
	s.failPulls()

	s.deploy(s.app, "v2")

	s.Empty(s.sent())
}

func (s *AlertsTestSuite) TestRecoveryFailsWhatAnEarlierEchowaLeftPending() {
	rule := s.rule(alerts.DeployFailed, nil, notify.Critical, s.ops)
	old := models.Notification{AlertRuleID: &rule.ID, NotificationProviderID: s.ops.ID, Trigger: alerts.DeployFailed, Severity: "critical", Title: "Deploy of blog failed", Status: models.NotificationPending}
	s.Require().NoError(facades.Orm().Query().Create(&old))
	_, err := facades.Orm().Query().Model(&old).Update("created_at", carbon.NewDateTime(carbon.FromStdTime(time.Now().Add(-time.Hour))))
	s.Require().NoError(err)
	current := models.Notification{AlertRuleID: &rule.ID, NotificationProviderID: s.ops.ID, Trigger: alerts.DeployFailed, Severity: "critical", Title: "Deploy of blog failed", Status: models.NotificationPending}
	s.Require().NoError(facades.Orm().Query().Create(&current))

	s.Require().NoError(services.RecoverNotifications(context.Background()))

	rows := s.notifications()
	s.Equal(models.NotificationFailed, rows[0].Status)
	s.Equal("Echowa restarted before it was sent.", *rows[0].Error)
	s.Equal(models.NotificationPending, rows[1].Status, "this process is still sending it")
}

func (s *AlertsTestSuite) TestPruneForgetsNotificationsAfter30Days() {
	var kept []uint
	for _, age := range []time.Duration{40 * 24 * time.Hour, 31 * 24 * time.Hour, 29 * 24 * time.Hour, time.Hour} {
		n := models.Notification{NotificationProviderID: s.ops.ID, Trigger: alerts.EchowaStarted, Severity: "info", Title: "Echowa started", Status: models.NotificationSent}
		s.Require().NoError(facades.Orm().Query().Create(&n))
		_, err := facades.Orm().Query().Model(&n).Update("created_at", carbon.NewDateTime(carbon.FromStdTime(time.Now().Add(-age))))
		s.Require().NoError(err)
		if age < services.NotificationsKept {
			kept = append(kept, n.ID)
		}
	}

	s.Require().NoError(services.PruneNotifications(context.Background(), time.Now()))

	var ids []uint
	for _, n := range s.notifications() {
		ids = append(ids, n.ID)
	}
	s.Equal(kept, ids)
}

func (s *AlertsTestSuite) TestDeletingAnAppForgetsItsRulesAndNotifications() {
	s.rule(alerts.DeployFailed, s.app, notify.Critical, s.ops)
	s.failPulls()
	s.deploy(s.app, "v2")
	s.Require().Len(s.notifications(), 1)

	_, err := facades.Orm().Query().Delete(s.app)
	s.Require().NoError(err)

	s.Empty(s.notifications())
	count, err := facades.Orm().Query().Model(&models.AlertRule{}).Count()
	s.Require().NoError(err)
	s.Zero(count)
}
