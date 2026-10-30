package feature

import (
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/goravel/framework/support/carbon"
	"github.com/stretchr/testify/suite"

	"goravel/app/alerts"
	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/metrics"
	"goravel/app/models"
	"goravel/app/notify"
	"goravel/app/services"
	"goravel/app/templates"
	"goravel/tests"
)

type ConditionsTestSuite struct {
	suite.Suite
	tests.TestCase
	docker *docker.Fake
	notify *notify.Fake
	app    *models.App
	ops    models.NotificationProvider
	// start is the first check's time.
	start time.Time
}

func TestConditionsTestSuite(t *testing.T) {
	suite.Run(t, new(ConditionsTestSuite))
}

func (s *ConditionsTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.docker = s.FakeDocker()
	s.notify = s.FakeNotify()
	retries := services.NotifyRetries
	services.NotifyRetries = []time.Duration{0, 0}
	s.T().Cleanup(func() {
		services.WaitForNotifications()
		services.NotifyRetries = retries
		// The last poll's statuses are process-wide: leave them empty.
		_, err := facades.Orm().Query().Exec("DELETE FROM apps")
		s.Require().NoError(err)
		s.Require().NoError(services.PollAppStatus(context.Background(), time.Now()))
	})
	s.app = createTestApp(s.T(), "blog", nil)
	s.ops = createTestProvider(s.T(), "Slack #ops")
	s.start = time.Now().Add(-time.Hour).Truncate(time.Minute)
}

// condition makes an enabled condition rule on trigger for app (nil for
// every app, or the server's), sending to ops.
func (s *ConditionsTestSuite) condition(trigger string, app *models.App, threshold *float64, forMinutes, repeatMinutes int) models.AlertRule {
	t, _ := alerts.Find(trigger)
	rule := createTestAlertRule(s.T(), trigger, app, t.Severity, s.ops)
	_, err := facades.Orm().Query().Model(&rule).Update(map[string]any{
		"threshold": threshold, "for_minutes": forMinutes, "repeat_minutes": repeatMinutes,
	})
	s.Require().NoError(err)

	return rule
}

func (s *ConditionsTestSuite) replicas(running, desired uint64) {
	s.docker.Status = func(name string) (docker.ServiceStatus, error) {
		return docker.ServiceStatus{Name: name, Running: running, Desired: desired}, nil
	}
}

// poll runs the status poller minutes after start.
func (s *ConditionsTestSuite) poll(minutes float64) {
	s.Require().NoError(services.PollAppStatus(context.Background(), s.start.Add(time.Duration(minutes*float64(time.Minute)))))
	services.WaitForNotifications()
}

// sent lists what was sent since the last call, as "title (severity):
// summary".
func (s *ConditionsTestSuite) sent() []string {
	lines := []string{}
	for _, sent := range s.notify.Sent() {
		lines = append(lines, fmt.Sprintf("%s (%s): %s", sent.Message.Title, sent.Message.Severity, sent.Message.Summary))
	}
	s.notify = s.FakeNotify()

	return lines
}

func (s *ConditionsTestSuite) state(rule models.AlertRule, subject string) models.AlertState {
	var state models.AlertState
	s.Require().NoError(facades.Orm().Query().Where("alert_rule_id", rule.ID).Where("subject", subject).First(&state))

	return state
}

func (s *ConditionsTestSuite) TestUnhealthyFiresOnceAndResolves() {
	rule := s.condition(alerts.Unhealthy, s.app, nil, 2, 0)
	s.replicas(0, 1)

	s.poll(0)
	s.poll(0.5)
	s.poll(1.5)
	s.Empty(s.sent(), "short of replicas for less than 2 minutes")
	s.Equal("pending", s.state(rule, models.AppSubject(s.app.ID)).State)

	s.poll(2)
	s.Equal([]string{"blog: Unhealthy for 2 minutes (critical): It runs fewer replicas than it wants."}, s.sent())
	s.poll(2.5)
	s.poll(10)
	s.Empty(s.sent(), "firing, and no repeat")

	s.replicas(1, 1)
	s.poll(10.5)
	s.poll(11)
	s.Empty(s.sent(), "clear for less than 2 minutes")
	s.poll(12.5)
	s.Equal([]string{"Resolved: blog: Unhealthy for 2 minutes (resolved): Clear after firing for 11 minutes."}, s.sent())
	s.Equal("ok", s.state(rule, models.AppSubject(s.app.ID)).State)

	var n models.Notification
	s.Require().NoError(facades.Orm().Query().Order("id desc").First(&n))
	s.Equal("resolved", n.Severity, "recorded as the resolve it was")
}

func (s *ConditionsTestSuite) TestAStoppedAppResolvesWithoutAMessage() {
	rule := s.condition(alerts.Unhealthy, nil, nil, 0, 0)
	s.replicas(0, 1)
	s.poll(0)
	s.Len(s.sent(), 1)

	s.replicas(0, 0)
	s.poll(1)
	s.poll(10)
	s.Empty(s.sent())
	s.Equal("ok", s.state(rule, models.AppSubject(s.app.ID)).State)

	// Never deployed is the same: nothing to be unhealthy about.
	s.docker.Status = func(string) (docker.ServiceStatus, error) { return docker.ServiceStatus{}, docker.ErrServiceNotFound }
	s.poll(11)
	s.Empty(s.sent())
}

func (s *ConditionsTestSuite) TestARestartMidFiringSendsNothingNew() {
	rule := s.condition(alerts.Unhealthy, s.app, nil, 2, 0)
	// What an earlier Echowa left: firing since minute 0.
	fired := carbon.NewDateTime(carbon.FromStdTime(s.start))
	s.Require().NoError(facades.Orm().Query().Create(&models.AlertState{
		AlertRuleID: rule.ID, Subject: models.AppSubject(s.app.ID), State: "firing", Since: fired, NotifiedAt: fired,
	}))

	s.replicas(0, 1)
	s.poll(20)
	s.Empty(s.sent(), "still firing: no second message")

	// Docker not answering is neither breach nor clear.
	s.docker.Status = func(string) (docker.ServiceStatus, error) { return docker.ServiceStatus{}, errors.New("daemon gone") }
	s.poll(21)
	s.Equal("firing", s.state(rule, models.AppSubject(s.app.ID)).State)

	s.replicas(1, 1)
	s.poll(22)
	s.poll(24)
	s.Equal([]string{"Resolved: blog: Unhealthy for 2 minutes (resolved): Clear after firing for 24 minutes."}, s.sent())
}

func (s *ConditionsTestSuite) TestTurningAnAlertOffForgetsItsState() {
	rule := s.condition(alerts.Unhealthy, s.app, nil, 0, 0)
	s.replicas(0, 1)
	s.poll(0)
	s.Len(s.sent(), 1)

	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d?tab=alerts", s.app.ID)
	b.visit(url)
	b.submit(nethttp.MethodPut, fmt.Sprintf("/apps/%d/alerts/%d/enabled", s.app.ID, rule.ID), url, map[string]any{"enabled": false})
	count, err := facades.Orm().Query().Model(&models.AlertState{}).Where("alert_rule_id", rule.ID).Count()
	s.Require().NoError(err)
	s.Zero(count)
	s.poll(1)
	s.Empty(s.sent(), "off sends nothing, the resolve included")

	b.submit(nethttp.MethodPut, fmt.Sprintf("/apps/%d/alerts/%d/enabled", s.app.ID, rule.ID), url, map[string]any{"enabled": true})
	s.poll(2)
	s.Len(s.sent(), 1, "on again, it starts from OK")
}

func (s *ConditionsTestSuite) TestDeletingAnAppForgetsItsStates() {
	rule := s.condition(alerts.Unhealthy, nil, nil, 0, 0)
	s.replicas(0, 1)
	s.poll(0)
	s.Require().NoError(services.DeleteApp(context.Background(), s.app))

	count, err := facades.Orm().Query().Model(&models.AlertState{}).Where("alert_rule_id", rule.ID).Count()
	s.Require().NoError(err)
	s.Zero(count, "a new app with its ID would inherit a firing alert")
}

// minute is a closed minute of one reading at the start of minute m.
func (s *ConditionsTestSuite) minute(m int, r metrics.Reading) metrics.Sample {
	var minute metrics.Minute
	r.At = s.start.Add(time.Duration(m) * time.Minute)
	minute.Add(r)
	sample, closed := minute.Add(metrics.Reading{At: r.At.Add(time.Minute)})
	s.Require().True(closed)

	return sample
}

func (s *ConditionsTestSuite) TestMetricConditions() {
	threshold := func(v float64) *float64 { return &v }
	s.condition(alerts.AppMemory, nil, threshold(90), 0, 0)
	s.condition(alerts.AppCPU, s.app, threshold(90), 0, 0)
	s.condition(alerts.DiskUsed, nil, threshold(80), 0, 0)
	s.condition(alerts.DockerUnreachable, nil, nil, 0, 0)
	s.condition(alerts.ServerMemory, nil, threshold(90), 0, 0)
	// The app runs with 2 CPUs, so 190% of one core is 95% of its limit.
	s.docker.Status = func(name string) (docker.ServiceStatus, error) {
		return docker.ServiceStatus{Name: name, Running: 1, Desired: 1, Limits: docker.Limits{NanoCPUs: 2e9}}, nil
	}
	s.poll(0)

	containers, cpu := 3, 190.0
	total, available := uint64(1000), uint64(500)
	reading := metrics.Reading{
		Host:  metrics.Host{Containers: &containers, MemoryTotal: &total, MemoryUsed: &available},
		Disks: []metrics.Disk{{Mount: "/", UsedBytes: 50, AvailableBytes: 50}, {Mount: "/data", UsedBytes: 90, AvailableBytes: 10}},
		Apps:  []metrics.App{{Name: "blog", Tasks: 1, CPUPercent: &cpu, MemoryUsed: 95, MemoryLimit: 100}},
	}
	s.Require().NoError(services.CheckMetricAlerts(context.Background(), s.minute(0, reading), s.start.Add(time.Minute)))
	services.WaitForNotifications()
	s.ElementsMatch([]string{
		"blog: Memory use above 90% of its limit (warning): Now 95% of its limit.",
		"blog: CPU use above 90% of its limit (warning): Now 95% of its limit.",
		"Server, /data: Disk use above 80% (critical): Now 90%.",
	}, s.sent())

	// Docker stops answering, and the app's figures go with it.
	reading.Host.Containers, reading.Apps = nil, nil
	s.Require().NoError(services.CheckMetricAlerts(context.Background(), s.minute(1, reading), s.start.Add(2*time.Minute)))
	services.WaitForNotifications()
	s.Equal([]string{"Server: Docker unreachable (critical): Echowa can't reach Docker, so it can't deploy, check apps or read their metrics."}, s.sent())

	// A stopped app's alerts resolve without a message.
	s.replicas(0, 0)
	s.poll(3)
	s.Require().NoError(services.CheckMetricAlerts(context.Background(), s.minute(3, reading), s.start.Add(4*time.Minute)))
	services.WaitForNotifications()
	s.Empty(s.sent())
	var firing []models.AlertState
	s.Require().NoError(facades.Orm().Query().Where("state", "firing").Order("subject").Get(&firing))
	s.Len(firing, 2, "the disk and Docker, not the stopped app")
}

func (s *ConditionsTestSuite) TestDatabaseConditions() {
	tmpl, _ := templates.Find("postgres")
	db, err := services.CreateApp(context.Background(), services.AppInput{
		Name: "shop-db", Repository: tmpl.Repository, DefaultTag: tmpl.DefaultTag, Port: tmpl.Port, Replicas: 1,
	}, &tmpl)
	s.Require().NoError(err)
	threshold := func(v float64) *float64 { return &v }
	s.condition(alerts.DBConnections, db, threshold(80), 0, 0)
	s.condition(alerts.LongestTransaction, nil, threshold(10), 0, 30)
	s.docker.ExecFunc = func(string, []string) ([]byte, error) {
		return []byte(`{"size_bytes": 4096, "connections": 90, "max_connections": 100, "longest_transaction_seconds": 1500,
			"idle_in_transaction": 0, "transaction_id_age": 25, "cache_hits": 10, "cache_misses": 0, "commits": 1,
			"rollbacks": 0, "temp_bytes": 0, "deadlocks": 0}`), nil
	}
	services.CheckDatabases(context.Background(), s.start)
	services.WaitForNotifications()
	s.ElementsMatch([]string{
		"shop-db: Connections above 80% of the maximum (warning): Now 90% of the maximum.",
		"shop-db: Longest open transaction above 10 minutes (warning): Now 25 minutes.",
	}, s.sent())

	services.CheckDatabases(context.Background(), s.start.Add(30*time.Minute))
	services.WaitForNotifications()
	s.Equal([]string{"Still firing: shop-db: Longest open transaction above 10 minutes (warning): Now 25 minutes. Firing for 30 minutes."}, s.sent())
	services.SetLatestDatabases(nil)
}

func (s *ConditionsTestSuite) TestTheAlertsSectionShowsWhatsFiring() {
	firingRule := s.condition(alerts.Unhealthy, s.app, nil, 0, 0)
	everyApp := s.condition(alerts.Unhealthy, nil, nil, 5, 0)
	s.replicas(0, 1)
	s.poll(0)

	b := signedIn(s.T(), &s.TestCase)
	_, page := b.visit(fmt.Sprintf("/apps/%d", s.app.ID))
	section := page.Props["alerts"].(map[string]any)
	firing := section["firing"].([]any)
	s.Require().Len(firing, 1, "on Overview too, from the same prop")
	s.Equal("blog", firing[0].(map[string]any)["subject"])
	s.Equal("Unhealthy", firing[0].(map[string]any)["description"])
	s.EqualValues(firingRule.ID, firing[0].(map[string]any)["rule_id"])
	s.Equal("firing", section["rules"].([]any)[0].(map[string]any)["state"])
	s.Equal("pending", section["every_app"].([]any)[0].(map[string]any)["state"])
	s.Equal("Unhealthy for 5 minutes", section["every_app"].([]any)[0].(map[string]any)["description"])

	_, page = b.visit("/settings")
	settings := page.Props["alerts"].(map[string]any)
	s.Empty(settings["firing"], "an app's own alert fires on its page")
	s.Equal("pending", settings["rules"].([]any)[0].(map[string]any)["state"])
	s.EqualValues(everyApp.ID, settings["rules"].([]any)[0].(map[string]any)["id"])
}
