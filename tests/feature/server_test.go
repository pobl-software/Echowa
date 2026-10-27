package feature

import (
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"goravel/app/metrics"
	"goravel/app/services"
	"goravel/tests"
)

type ServerTestSuite struct {
	suite.Suite
	tests.TestCase
}

func TestServerTestSuite(t *testing.T) {
	suite.Run(t, new(ServerTestSuite))
}

func (s *ServerTestSuite) SetupTest() {
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

func (s *ServerTestSuite) TearDownTest() {
	services.SetLatestMetrics(nil)
}

func ptr[T any](v T) *T { return &v }

func reading() *metrics.Reading {
	return &metrics.Reading{
		At: time.Now(),
		Host: metrics.Host{
			CPUPercent: ptr(12.5), CPUCount: ptr(4), Load1: ptr(0.5), Load5: ptr(0.4), Load15: ptr(0.3),
			MemoryTotal: ptr(uint64(8 << 30)), MemoryUsed: ptr(uint64(2 << 30)), Containers: ptr(2),
		},
		Disks:   []metrics.Disk{{Mount: "/", Device: "/dev/vda1", Filesystem: "ext4", TotalBytes: 100, UsedBytes: 40, AvailableBytes: 55}},
		DiskIO:  []metrics.DiskIO{},
		Network: []metrics.Interface{},
		Apps:    []metrics.App{{Name: "blog", Tasks: 1, CPUPercent: ptr(3.0), MemoryUsed: 100, MemoryLimit: 1000}},
		Others:  []metrics.Container{{Name: "traefik", MemoryUsed: 50, MemoryLimit: 1000}},
	}
}

func (s *ServerTestSuite) TestBeforeTheFirstReading() {
	b := signedIn(s.T(), &s.TestCase)
	_, page := b.visit("/server")

	page.AssertComponent("Server")
	s.Nil(page.Props["latest"], "nothing read yet: the page shows a waiting state")
	s.Equal([]any{"1h", "6h", "24h", "7d", "30d", "90d"}, page.Props["ranges"])
	series := page.Props["series"].(map[string]any)
	s.Equal("24h", series["range"])
	s.Equal([]any{}, series["cpu"].(map[string]any)["usage"], "empty lists, never null")
	s.Equal([]any{}, series["memory"])
	s.Equal([]any{}, series["disks"])
	s.Equal([]any{}, series["network"])

	_, page = b.visit("/dashboard")
	s.Nil(page.Props["metrics"], "no strip yet")
}

func (s *ServerTestSuite) TestShowsTheLatestReadingAndCharts() {
	app := createTestApp(s.T(), "blog", nil)
	services.SetLatestMetrics(reading())
	db, err := services.MetricsDB()
	s.Require().NoError(err)
	minute := time.Now().Truncate(time.Minute).Add(-time.Minute).Unix()
	_, err = db.Exec(`INSERT INTO host_samples (ts, readings, cpu_percent_avg, cpu_percent_max, memory_used_bytes_avg, memory_used_bytes_max,
		memory_total_bytes_avg, memory_total_bytes_max) VALUES (?, 4, 10, 30, 2, 3, 8, 8)`, minute)
	s.Require().NoError(err)
	_, err = db.Exec(`INSERT INTO disk_samples (mount, ts, readings, used_bytes_avg, used_bytes_max, available_bytes_avg, available_bytes_max)
		VALUES ('/', ?, 4, 40, 40, 60, 60)`, minute)
	s.Require().NoError(err)

	b := signedIn(s.T(), &s.TestCase)
	_, page := b.visit("/server?range=1h")

	latest := page.Props["latest"].(map[string]any)
	s.EqualValues(12.5, latest["host"].(map[string]any)["cpu_percent"])
	s.Equal("blog", latest["apps"].([]any)[0].(map[string]any)["app"])
	s.Equal("traefik", latest["other_containers"].([]any)[0].(map[string]any)["name"])
	s.Equal(map[string]any{"blog": float64(app.ID)}, page.Props["appIds"])

	series := page.Props["series"].(map[string]any)
	s.Equal("1h", series["range"])
	s.EqualValues(60, series["step_seconds"])
	s.Equal([]any{map[string]any{"t": float64(minute), "avg": 10.0, "max": 30.0}}, series["cpu"].(map[string]any)["usage"])
	s.Equal([]any{map[string]any{"t": float64(minute), "avg": 25.0, "max": 37.5}}, series["memory"], "used of total, as a percentage")
	disks := series["disks"].([]any)
	s.Require().Len(disks, 1)
	s.Equal([]any{map[string]any{"t": float64(minute), "avg": 40.0, "max": 40.0}}, disks[0].(map[string]any)["percent"])
	s.Equal([]any{}, series["swap"], "no swap measured")

	_, page = b.visit("/server?range=30d")
	s.EqualValues(7200, page.Props["series"].(map[string]any)["step_seconds"], "long ranges read hourly rollups")

	_, page = b.visit("/dashboard")
	s.EqualValues(12.5, page.Props["metrics"].(map[string]any)["host"].(map[string]any)["cpu_percent"])
}

func (s *ServerTestSuite) TestGuestsAreSentToLogin() {
	b := newBrowser(s.T(), &s.TestCase)
	b.request("GET", "/server", nil, true, nil).AssertHeader("Location", "/login")
}
