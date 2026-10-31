package feature

import (
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"os"
	"testing"
	"time"

	"github.com/goravel/framework/facades"
	"github.com/goravel/framework/support/carbon"
	"github.com/stretchr/testify/suite"

	"goravel/app/docker"
	"goravel/app/metrics"
	"goravel/app/models"
	"goravel/app/release"
	"goravel/app/services"
	"goravel/app/templates"
	"goravel/tests"
)

type DashboardControllerTestSuite struct {
	suite.Suite
	tests.TestCase
	docker *docker.Fake
}

func TestDashboardControllerTestSuite(t *testing.T) {
	suite.Run(t, new(DashboardControllerTestSuite))
}

func (s *DashboardControllerTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.docker = s.FakeDocker()
}

func (s *DashboardControllerTestSuite) TestGuestsAreSentToLogin() {
	b := newBrowser(s.T(), &s.TestCase)
	b.request(nethttp.MethodGet, "/dashboard", nil, true, nil).AssertHeader("Location", "/login")
	b.request(nethttp.MethodGet, "/", nil, false, nil).AssertHeader("Location", "/dashboard")
}

func (s *DashboardControllerTestSuite) TestListsAppsWithLiveStatusAndLastDeployment() {
	ctx := context.Background()
	running := createTestApp(s.T(), "blog", nil)
	notDeployed := createTestApp(s.T(), "shop", nil)
	s.Require().NoError(s.docker.DeployService(ctx, docker.ServiceSpec{
		Name: running.ServiceName(), App: "blog", Replicas: 2,
		Image: "ghcr.io/owner/blog@sha256:3f1c2a9b8d7e0123456789abcdef0123456789abcdef0123456789abcdef0123",
	}))
	_, err := services.CreateDeployment(ctx, running, models.TriggerManual, "v1")
	s.Require().NoError(err)
	latest, err := services.CreateDeployment(ctx, running, models.TriggerWebhook, "v2")
	s.Require().NoError(err)

	b := signedIn(s.T(), &s.TestCase)
	b.forbid(string(running.WebhookSecret), string(notDeployed.WebhookSecret))
	_, page := b.visit("/dashboard")

	page.AssertComponent("Dashboard")
	apps := page.Props["apps"].([]any)
	s.Require().Len(apps, 2)
	blog := apps[0].(map[string]any)
	s.Equal("blog", blog["name"])
	s.Equal("blog.example.com", blog["domain"])
	status := blog["status"].(map[string]any)
	s.Equal(services.StateRunning, status["state"])
	s.EqualValues(2, status["running"])
	s.EqualValues(2, status["desired"])
	s.Equal("3f1c2a9b8d7e", status["digest"])
	last := blog["last_deployment"].(map[string]any)
	s.EqualValues(latest.ID, last["id"])
	s.Equal("queued", last["status"])
	s.NotContains(last, "log", "lists don't carry deploy logs")

	shop := apps[1].(map[string]any)
	s.Equal(services.StateNotDeployed, shop["status"].(map[string]any)["state"])
	s.Nil(shop["last_deployment"])
}

func (s *DashboardControllerTestSuite) TestUnknownStatusWhenDockerDoesNotAnswer() {
	createTestApp(s.T(), "blog", nil)
	s.docker.Status = func(string) (docker.ServiceStatus, error) {
		return docker.ServiceStatus{}, errors.New("connection refused")
	}

	_, page := signedIn(s.T(), &s.TestCase).visit("/dashboard")

	app := page.Props["apps"].([]any)[0].(map[string]any)
	s.Equal(services.StateUnknown, app["status"].(map[string]any)["state"])
}

func (s *DashboardControllerTestSuite) TestDockerBanner() {
	b := signedIn(s.T(), &s.TestCase)

	_, page := b.visit("/dashboard")
	s.Nil(page.Props["dockerProblem"], "no banner when Docker is a Swarm manager")

	s.docker.CheckErr = fmt.Errorf("%w (node state %q)", docker.ErrNotSwarm, "inactive")
	_, page = b.visit("/settings/registries")
	s.Contains(page.Props["dockerProblem"], "docker swarm init")

	s.docker.CheckErr = fmt.Errorf("%w: dial unix /var/run/docker.sock: connect: no such file", docker.ErrDaemonUnreachable)
	_, page = b.visit("/dashboard")
	s.Contains(page.Props["dockerProblem"], "can't reach the Docker daemon")

	s.docker.CheckErr = docker.ErrNotManager
	_, page = b.visit("/dashboard")
	s.Contains(page.Props["dockerProblem"], "isn't a Swarm manager")

	_, page = newBrowser(s.T(), &s.TestCase).visit("/login")
	s.Nil(page.Props["dockerProblem"], "guests don't trigger a Docker check")
}

func (s *DashboardControllerTestSuite) TestEachAppListsTheDatabasesLinkedToIt() {
	ctx := context.Background()
	shop := createTestApp(s.T(), "shop", nil)
	createTestApp(s.T(), "blog", nil)
	tmpl, _ := templates.Find("postgres")
	db, err := services.CreateApp(ctx, services.TemplateInput(tmpl, "db"), &tmpl)
	s.Require().NoError(err)
	s.Require().NoError(services.LinkApps(ctx, shop.ID, db.ID))

	b := signedIn(s.T(), &s.TestCase)
	_, page := b.visit("/dashboard")

	apps := map[string]map[string]any{}
	for _, app := range page.Props["apps"].([]any) {
		apps[app.(map[string]any)["name"].(string)] = app.(map[string]any)
	}
	s.Equal([]any{float64(db.ID)}, apps["shop"]["links"])
	s.Equal([]any{}, apps["blog"]["links"], "an empty list, not null")
	s.Nil(apps["blog"]["template"])
	s.Equal("postgres", apps["db"]["template"])
	s.Equal([]any{}, apps["db"]["links"])
	s.Len(apps, 3, "the database is still listed, for the stats and its row")
}

func (s *DashboardControllerTestSuite) TestServerFactsSayWhatsRunningAndWhatsNewer() {
	version, fetch := release.Version, services.FetchLatestRelease
	s.T().Cleanup(func() {
		release.Version, services.FetchLatestRelease = version, fetch
		services.ResetUpdateCheck()
	})
	release.Version = "v1.2.0"
	services.ResetUpdateCheck()
	b := signedIn(s.T(), &s.TestCase)

	_, page := b.visit("/dashboard")
	server := page.Props["server"].(map[string]any)
	hostname, _ := os.Hostname()
	s.Equal(hostname, server["hostname"])
	s.Equal("v1.2.0", server["version"])
	s.Equal("", server["update"])
	s.Equal(false, server["update_checked"], "no answer from the channel yet")

	services.FetchLatestRelease = func(context.Context) (services.LatestRelease, error) {
		return services.LatestRelease{Version: "v1.3.0"}, nil
	}
	services.CheckForUpdate(context.Background())
	services.WaitForNotifications()
	_, page = b.visit("/dashboard")
	server = page.Props["server"].(map[string]any)
	s.Equal("v1.3.0", server["update"])
	s.Equal(true, server["update_checked"])

	services.FetchLatestRelease = func(context.Context) (services.LatestRelease, error) {
		return services.LatestRelease{}, errors.New("channel down")
	}
	services.CheckForUpdate(context.Background())
	_, page = b.visit("/dashboard")
	server = page.Props["server"].(map[string]any)
	s.Equal("v1.3.0", server["update"], "a release already seen stays")
	s.Equal(false, server["update_checked"], "a failed look isn't up to date")
}

func (s *DashboardControllerTestSuite) TestRecentActivityLeavesOutWhatGoesFineAllTheTime() {
	app := createTestApp(s.T(), "blog", nil)
	loc := models.StorageLocation{Name: "R2", Endpoint: "https://r2.example.com", Region: "auto", Bucket: "b", AccessKeyID: "k", SecretKey: models.Secret("s")}
	s.Require().NoError(facades.Orm().Query().Create(&loc))
	task := models.ScheduledTask{AppID: app.ID, Name: "Scheduler", Schedule: "* * * * *", Timezone: "UTC", Command: "true", TimeoutMinutes: 1, Enabled: true}
	s.Require().NoError(facades.Orm().Query().Create(&task))

	at := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	started := carbon.NewDateTime(carbon.Now())
	created := func(table string, id uint, minutes int) {
		_, err := facades.Orm().Query().Exec("UPDATE "+table+" SET created_at = ? WHERE id = ?", at.Add(time.Duration(minutes)*time.Minute).Format(time.DateTime), id)
		s.Require().NoError(err)
	}
	deploy, err := services.CreateDeployment(context.Background(), app, models.TriggerManual, "v1")
	s.Require().NoError(err)
	created("deployments", deploy.ID, 1)
	for i, status := range []models.BackupStatus{models.BackupSucceeded, models.BackupSkipped} {
		backup := models.Backup{AppID: app.ID, StorageLocationID: loc.ID, Trigger: models.BackupOnSchedule, Status: status, StartedAt: started}
		s.Require().NoError(facades.Orm().Query().Create(&backup))
		created("backups", backup.ID, 2+i)
	}
	for i, run := range []models.ScheduledTaskRun{
		{ScheduledTaskID: task.ID, Trigger: models.RunOnSchedule, Status: models.RunSucceeded, StartedAt: started},
		{ScheduledTaskID: task.ID, Trigger: models.RunOnSchedule, Status: models.RunFailed, StartedAt: started},
		{ScheduledTaskID: task.ID, Trigger: models.RunByHand, Status: models.RunSucceeded, StartedAt: started},
	} {
		s.Require().NoError(facades.Orm().Query().Create(&run))
		created("scheduled_task_runs", run.ID, 4+i)
	}

	_, page := signedIn(s.T(), &s.TestCase).visit("/dashboard")

	var got []string
	for _, row := range page.Props["activity"].([]any) {
		a := row.(map[string]any)
		s.EqualValues(app.ID, a["app_id"])
		got = append(got, fmt.Sprintf("%s %s %s", a["kind"], a["status"], a["trigger"]))
	}
	s.Equal([]string{
		"task succeeded manual",
		"task failed schedule",
		"backup succeeded schedule",
		"deployment queued manual",
	}, got, "newest first, without scheduled runs that succeeded or backups that were skipped")
}

func (s *DashboardControllerTestSuite) TestEachAppsCPUOverTheLastHour() {
	blog := createTestApp(s.T(), "blog", nil)
	shop := createTestApp(s.T(), "shop", nil)
	db, err := services.MetricsDB()
	s.Require().NoError(err)
	for _, table := range metrics.Tables {
		_, err := db.Exec("DELETE FROM " + table.Name)
		s.Require().NoError(err)
	}
	minute := time.Now().Truncate(time.Minute).Add(-time.Minute).Unix()
	_, err = db.Exec(`INSERT INTO app_samples (app_id, ts, readings, tasks_avg, tasks_max, cpu_percent_avg, cpu_percent_max)
		VALUES (?, ?, 4, 1, 1, 3, 9)`, blog.ID, minute)
	s.Require().NoError(err)

	_, page := signedIn(s.T(), &s.TestCase).visit("/dashboard")

	cpu := page.Props["cpu"].(map[string]any)
	s.Equal("1h", cpu["range"])
	apps := cpu["apps"].(map[string]any)
	s.Equal([]any{map[string]any{"t": float64(minute), "avg": 3.0, "max": 9.0}}, apps[fmt.Sprint(blog.ID)])
	s.NotContains(apps, fmt.Sprint(shop.ID), "nothing stored for it")
}
