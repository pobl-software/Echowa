package feature

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"goravel/app/docker"
	"goravel/app/metrics"
	"goravel/app/models"
	"goravel/app/services"
	"goravel/app/templates"
	"goravel/tests"
)

type DatabasesTestSuite struct {
	suite.Suite
	tests.TestCase
	docker *docker.Fake
}

func TestDatabasesTestSuite(t *testing.T) {
	suite.Run(t, new(DatabasesTestSuite))
}

func (s *DatabasesTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.docker = s.FakeDocker()
	services.SetLatestDatabases(nil)
	db, err := services.MetricsDB()
	s.Require().NoError(err)
	for _, table := range metrics.Tables {
		_, err := db.Exec("DELETE FROM " + table.Name)
		s.Require().NoError(err)
	}
}

func (s *DatabasesTestSuite) templateApp(slug, name string) *models.App {
	tmpl, _ := templates.Find(slug)
	app, err := services.CreateApp(context.Background(), services.AppInput{
		Name: name, Repository: tmpl.Repository, DefaultTag: tmpl.DefaultTag, Port: tmpl.Port, Replicas: 1,
	}, &tmpl)
	s.Require().NoError(err)

	return app
}

func postgresStatus(commits int) []byte {
	return fmt.Appendf(nil, `{"size_bytes": 4096, "connections": 3, "max_connections": 100, "longest_transaction_seconds": 0,
		"idle_in_transaction": 0, "transaction_id_age": 25, "cache_hits": %d, "cache_misses": 0, "commits": %d,
		"rollbacks": 0, "temp_bytes": 0, "deadlocks": 0}`, commits*10, commits)
}

func (s *DatabasesTestSuite) storedRows(appID uint) int {
	db, err := services.MetricsDB()
	s.Require().NoError(err)
	var n int
	s.Require().NoError(db.QueryRow("SELECT count(*) FROM database_samples WHERE app_id = ?", appID).Scan(&n))

	return n
}

func (s *DatabasesTestSuite) TestEachRunningDatabaseIsCheckedAndStored() {
	postgres := s.templateApp("postgres", "db")
	custom := createTestApp(s.T(), "blog", nil)
	commits := 100
	var ran []string
	s.docker.ExecFunc = func(service string, cmd []string) ([]byte, error) {
		ran = append(ran, service)
		tmpl, _ := templates.Find("postgres")
		s.Equal(tmpl.Inspect.Health, cmd, "only the template's own command runs")
		return postgresStatus(commits), nil
	}

	now := time.Now()
	services.CheckDatabases(context.Background(), now)
	s.Equal([]string{"echowa-db"}, ran, "a custom app isn't a database")
	latest := services.LatestDatabase(postgres)
	s.Require().NotNil(latest)
	s.Equal(4096.0, *latest.SizeBytes)
	s.Nil(latest.CommitsPerSec, "no rate from the first check")
	s.Nil(services.LatestDatabase(custom))

	commits = 160
	services.CheckDatabases(context.Background(), now.Add(time.Minute))
	latest = services.LatestDatabase(postgres)
	s.Require().NotNil(latest.CommitsPerSec)
	s.InDelta(1, *latest.CommitsPerSec, 1e-9)
	s.Equal(2, s.storedRows(postgres.ID), "one row a minute")
}

func (s *DatabasesTestSuite) TestADatabaseThatIsntRunningOrFailsIsSkipped() {
	postgres := s.templateApp("postgres", "db")
	s.docker.ExecFunc = func(service string, _ []string) ([]byte, error) {
		return nil, fmt.Errorf("%s: %w", service, docker.ErrNotRunning)
	}
	services.CheckDatabases(context.Background(), time.Now())
	s.Nil(services.LatestDatabase(postgres))
	s.Zero(s.storedRows(postgres.ID))

	s.docker.ExecFunc = func(string, []string) ([]byte, error) {
		return nil, &docker.ExecError{ExitCode: 2, Stderr: `psql: error: FATAL: role "db" does not exist`}
	}
	services.CheckDatabases(context.Background(), time.Now())
	s.Nil(services.LatestDatabase(postgres), "a failed check shows nothing, not old figures")
	s.Zero(s.storedRows(postgres.ID))

	s.docker.ExecFunc = func(string, []string) ([]byte, error) { return []byte("not json"), nil }
	services.CheckDatabases(context.Background(), time.Now())
	s.Nil(services.LatestDatabase(postgres))
}

func (s *DatabasesTestSuite) TestTheAppPageShowsADatabasesFigures() {
	postgres := s.templateApp("postgres", "db")
	custom := createTestApp(s.T(), "blog", nil)
	s.docker.ExecFunc = func(string, []string) ([]byte, error) { return postgresStatus(100), nil }
	services.SetLatestMetrics(&metrics.Reading{
		Host:  metrics.Host{MemoryTotal: ptr(uint64(8 << 30)), MemoryAvailable: ptr(uint64(6 << 30))},
		Disks: []metrics.Disk{{Mount: "/", UsedBytes: 10, AvailableBytes: 90}, {Mount: "/var/lib/docker", UsedBytes: 95, AvailableBytes: 5}, {Mount: "/var/lib/dockerish"}},
		Apps:  []metrics.App{{Name: "db", Tasks: 1, MemoryUsed: 512 << 20}},
	})
	s.T().Cleanup(func() { services.SetLatestMetrics(nil) })
	services.CheckDatabases(context.Background(), time.Now())

	b := signedIn(s.T(), &s.TestCase)
	_, page := b.visit(fmt.Sprintf("/apps/%d?tab=metrics", postgres.ID))
	live := page.Props["database"].(map[string]any)
	s.Equal(4096.0, live["size_bytes"])
	s.Equal(100.0, live["max_connections"])
	s.Equal(float64(512<<20), live["memory_used_bytes"])
	s.Equal("/var/lib/docker", live["disk"].(map[string]any)["mount"], "the filesystem holding Docker's data")
	series := page.Props["databaseSeries"].(map[string]any)
	s.NotEmpty(series["size"], "charted from the stored minute")

	_, page = b.visit(fmt.Sprintf("/apps/%d?tab=metrics", custom.ID))
	s.Nil(page.Props["database"], "a custom app isn't a database")
	s.Nil(page.Props["databaseSeries"])
}
