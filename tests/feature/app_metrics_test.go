package feature

import (
	"fmt"
	"testing"
	"time"

	"github.com/goravel/framework/support/carbon"
	"github.com/stretchr/testify/suite"

	"goravel/app/facades"
	"goravel/app/metrics"
	"goravel/app/models"
	"goravel/app/services"
	"goravel/tests"
)

type AppMetricsTestSuite struct {
	suite.Suite
	tests.TestCase
}

func TestAppMetricsTestSuite(t *testing.T) {
	suite.Run(t, new(AppMetricsTestSuite))
}

func (s *AppMetricsTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.FakeDocker()
	services.SetLatestMetrics(nil)
	db, err := services.MetricsDB()
	s.Require().NoError(err)
	for _, table := range metrics.Tables {
		_, err := db.Exec("DELETE FROM " + table.Name)
		s.Require().NoError(err)
	}
}

func (s *AppMetricsTestSuite) TearDownTest() {
	services.SetLatestMetrics(nil)
}

func (s *AppMetricsTestSuite) TestANewAppHasNoMetricsYet() {
	app := createTestApp(s.T(), "blog", nil)
	b := signedIn(s.T(), &s.TestCase)

	_, page := b.visit(fmt.Sprintf("/apps/%d", app.ID) + "?tab=metrics")
	page.AssertComponent("Apps/Show")
	s.Nil(page.Props["metrics"], "nothing read yet")
	s.Equal([]any{"1h", "6h", "24h", "7d", "30d", "90d"}, page.Props["ranges"])
	series := page.Props["series"].(map[string]any)
	s.Equal("24h", series["range"])
	for _, key := range []string{"cpu", "tasks", "memory"} {
		s.Equal([]any{}, series[key], "%s: empty lists, never null", key)
	}
	s.Equal(map[string]any{"rx": []any{}, "tx": []any{}}, series["network"])
	s.Equal(map[string]any{"read": []any{}, "write": []any{}}, series["block"])

	// Read, but the app isn't running.
	r := reading()
	r.Apps = nil
	services.SetLatestMetrics(r)
	_, page = b.visit(fmt.Sprintf("/apps/%d", app.ID))
	s.Nil(page.Props["metrics"], "no tasks running")
}

func (s *AppMetricsTestSuite) TestShowsTheAppsReadingAndCharts() {
	app := createTestApp(s.T(), "blog", nil)
	other := createTestApp(s.T(), "shop", nil)
	services.SetLatestMetrics(reading())

	db, err := services.MetricsDB()
	s.Require().NoError(err)
	minute := time.Now().Truncate(time.Minute).Add(-time.Minute).Unix()
	insert := `INSERT INTO app_samples (app_id, ts, readings, tasks_avg, tasks_max, cpu_percent_avg, cpu_percent_max,
		memory_used_bytes_avg, memory_used_bytes_max, net_rx_bytes_per_sec_avg, net_rx_bytes_per_sec_max) VALUES (?, ?, 4, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err = db.Exec(insert, app.ID, minute, 1.25, 2, 3, 9, 100, 150, 10, 20)
	s.Require().NoError(err)
	_, err = db.Exec(insert, other.ID, minute, 1, 1, 50, 50, 900, 900, 0, 0)
	s.Require().NoError(err)

	b := signedIn(s.T(), &s.TestCase)
	_, page := b.visit(fmt.Sprintf("/apps/%d", app.ID) + "?tab=metrics&range=1h")

	live := page.Props["metrics"].(map[string]any)
	s.Equal("blog", live["app"])
	s.EqualValues(3, live["cpu_percent"])
	s.Equal(true, live["memory_limited"], "1000 bytes of the host's 8 GiB")

	series := page.Props["series"].(map[string]any)
	s.Equal("1h", series["range"])
	s.EqualValues(60, series["step_seconds"])
	point := func(avg, max float64) []any {
		return []any{map[string]any{"t": float64(minute), "avg": avg, "max": max}}
	}
	s.Equal(point(1.25, 2), series["tasks"], "a redeploy's overlap shows in the peak")
	s.Equal(point(3, 9), series["cpu"], "this app's rows only")
	s.Equal(point(100, 150), series["memory"])
	s.Equal(point(10, 20), series["network"].(map[string]any)["rx"])
	s.Equal([]any{}, series["block"].(map[string]any)["read"], "not measured")

	// Without a limit of its own, Docker reports the host's memory.
	r := reading()
	r.Apps[0].MemoryLimit = *r.Host.MemoryTotal
	services.SetLatestMetrics(r)
	_, page = b.visit(fmt.Sprintf("/apps/%d", app.ID))
	s.Equal(false, page.Props["metrics"].(map[string]any)["memory_limited"])
}

func (s *AppMetricsTestSuite) TestListsTheDeploysThatChangedWhatRunsInTheRange() {
	app := createTestApp(s.T(), "blog", nil)
	other := createTestApp(s.T(), "shop", nil)
	now := time.Now().UTC().Truncate(time.Second)
	insert := func(appID uint, status models.DeploymentStatus, trigger models.DeploymentTrigger, ref string, finished time.Time) uint {
		at := carbon.NewDateTime(carbon.FromStdTime(finished))
		d := models.Deployment{AppID: appID, Status: status, Trigger: trigger, Ref: ref, StartedAt: at, FinishedAt: at}
		s.Require().NoError(facades.Orm().Query().Create(&d))
		return d.ID
	}
	older := insert(app.ID, models.DeploymentSucceeded, models.TriggerManual, "v1", now.Add(-30*time.Hour))
	webhook := insert(app.ID, models.DeploymentSucceeded, models.TriggerWebhook, "v2", now.Add(-2*time.Hour))
	rollback := insert(app.ID, models.DeploymentSucceeded, models.TriggerRollback, "sha256:abc", now.Add(-90*time.Minute))
	insert(app.ID, models.DeploymentFailed, models.TriggerWebhook, "v3", now.Add(-30*time.Minute))
	insert(app.ID, models.DeploymentRolledBack, models.TriggerWebhook, "v4", now.Add(-20*time.Minute))
	insert(other.ID, models.DeploymentSucceeded, models.TriggerManual, "v9", now.Add(-10*time.Minute))
	running := models.Deployment{AppID: app.ID, Status: models.DeploymentDeploying, Trigger: models.TriggerManual, Ref: "v5"}
	s.Require().NoError(facades.Orm().Query().Create(&running))

	b := signedIn(s.T(), &s.TestCase)
	deploys := func(rng string) []any {
		_, page := b.visit(fmt.Sprintf("/apps/%d?tab=metrics&range=%s", app.ID, rng))
		return page.Props["series"].(map[string]any)["deploys"].([]any)
	}
	deploy := func(id uint, at time.Time, ref, trigger string) map[string]any {
		return map[string]any{"id": float64(id), "at": float64(at.Unix()), "ref": ref, "trigger": trigger}
	}

	s.Equal([]any{
		deploy(webhook, now.Add(-2*time.Hour), "v2", "webhook"),
		deploy(rollback, now.Add(-90*time.Minute), "sha256:abc", "rollback"),
	}, deploys("24h"), "succeeded ones only, oldest first: a failed or rolled back deploy left the old version running")
	s.Len(deploys("7d"), 3, "older ones in a longer range")
	s.Equal(float64(older), deploys("7d")[0].(map[string]any)["id"])
	s.Equal([]any{}, deploys("1h"), "none finished in the last hour, as a list, never null")
}
