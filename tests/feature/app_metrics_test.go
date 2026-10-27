package feature

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"goravel/app/metrics"
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
