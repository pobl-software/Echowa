package feature

import (
	"context"
	"fmt"
	nethttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/romsar/gonertia/v3"
	"github.com/stretchr/testify/suite"

	"goravel/app/deploy"
	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
	"goravel/app/templates"
	"goravel/tests"
)

type SlowQueriesTestSuite struct {
	suite.Suite
	tests.TestCase
	docker *docker.Fake
}

func TestSlowQueriesTestSuite(t *testing.T) {
	suite.Run(t, new(SlowQueriesTestSuite))
}

func (s *SlowQueriesTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.docker = s.FakeDocker()
	original := deploy.Dispatch
	deploy.Dispatch = func(uint, time.Duration) error { return nil }
	s.T().Cleanup(func() { deploy.Dispatch = original })
}

func (s *SlowQueriesTestSuite) deployed(slug, name string) models.App {
	tmpl, _ := templates.Find(slug)
	app, err := services.CreateApp(context.Background(), services.AppInput{
		Name: name, Repository: tmpl.Repository, DefaultTag: tmpl.DefaultTag, Port: tmpl.Port, Replicas: 1,
	}, &tmpl)
	s.Require().NoError(err)
	_, err = services.CreateDeployment(context.Background(), app, models.TriggerManual, app.DefaultTag)
	s.Require().NoError(err)
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))

	return s.reload(app.ID)
}

func (s *SlowQueriesTestSuite) reload(id uint) models.App {
	var app models.App
	s.Require().NoError(facades.Orm().Query().Where("id", id).FirstOrFail(&app))

	return app
}

func (s *SlowQueriesTestSuite) lastArgs() []string {
	deploys := s.docker.Deploys()
	s.Require().NotEmpty(deploys)

	return deploys[len(deploys)-1].Args
}

// slowQueries asks the app page for its slowQueries prop, the way the
// Metrics section's partial reload does.
func slowQueries(b *browser, app models.App) map[string]any {
	resp := b.request(nethttp.MethodGet, fmt.Sprintf("/apps/%d?tab=metrics", app.ID), nil, true, map[string]string{
		"X-Inertia-Partial-Component": "Apps/Show",
		"X-Inertia-Partial-Data":      "slowQueries",
	})
	resp.AssertOk()
	body, err := resp.Content()
	if err != nil {
		b.t.Fatal(err)
	}
	result, _ := gonertia.AssertFromString(b.t, body).Props["slowQueries"].(map[string]any)

	return result
}

func (s *SlowQueriesTestSuite) TestSwitchingOnRestartsPostgresCollectingThem() {
	app := s.deployed("postgres", "db")
	s.Nil(s.lastArgs(), "not collected until asked")
	b := signedIn(s.T(), &s.TestCase)
	page := fmt.Sprintf("/apps/%d", app.ID)
	_, show := b.visit(page)
	s.Equal("switch", show.Props["template"].(map[string]any)["slow_queries"])
	s.NotContains(show.Props, "slowQueries", "never read on a full visit")

	resp := b.submit(nethttp.MethodPost, page+"/slow-queries", page, nil)
	s.True(s.reload(app.ID).SlowQueries)
	var queued models.Deployment
	s.Require().NoError(facades.Orm().Query().Where("app_id", app.ID).Order("id desc").FirstOrFail(&queued))
	resp.AssertHeader("Location", fmt.Sprintf("/apps/%d/deployments/%d", app.ID, queued.ID))
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	s.Contains(strings.Join(s.lastArgs(), " "), "shared_preload_libraries=pg_stat_statements")
	s.Contains(strings.Join(s.lastArgs(), " "), "track_utility=off", "a password in ALTER ROLE never reaches the list")

	b.submit(nethttp.MethodDelete, page+"/slow-queries", page, nil)
	s.False(s.reload(app.ID).SlowQueries)
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	s.Nil(s.lastArgs(), "and the server starts as before")
}

func (s *SlowQueriesTestSuite) TestTheListIsReadOnlyWhenOn() {
	app := s.deployed("postgres", "db")
	tmpl, _ := templates.Find("postgres")
	var ran [][]string
	s.docker.ExecFunc = func(service string, cmd []string) ([]byte, error) {
		ran = append(ran, cmd)
		return []byte(`{"since": "2026-10-27T09:00:00.123+00:00", "dropped": 0, "queries": [
			{"query": "SELECT * FROM users WHERE id = $1", "calls": 40, "total_ms": 120.5, "mean_ms": 3.01, "max_ms": 9.2, "rows": 40}]}`), nil
	}
	b := signedIn(s.T(), &s.TestCase)

	s.Nil(slowQueries(b, app), "off: nothing to read")
	s.Empty(ran)

	s.Require().NoError(services.SetSlowQueries(context.Background(), &app, true))
	result := slowQueries(b, app)
	s.Equal([][]string{tmpl.Inspect.SlowQueries}, ran)
	report := result["report"].(map[string]any)
	s.Equal("2026-10-27T09:00:00.123Z", report["since"])
	queries := report["queries"].([]any)
	s.Require().Len(queries, 1)
	s.Equal("SELECT * FROM users WHERE id = $1", queries[0].(map[string]any)["query"])
	s.Empty(result["problem"])

	s.docker.ExecFunc = func(string, []string) ([]byte, error) {
		return nil, &docker.ExecError{ExitCode: 1, Stderr: "ERROR:  pg_stat_statements must be loaded via \"shared_preload_libraries\"\n"}
	}
	result = slowQueries(b, app)
	s.Nil(result["report"])
	s.Equal(`The database answered: ERROR:  pg_stat_statements must be loaded via "shared_preload_libraries"`, result["problem"])

	s.docker.ExecFunc = nil
	s.Equal("db isn't running.", slowQueries(b, app)["problem"])
}

func (s *SlowQueriesTestSuite) TestResetClearsTheTotals() {
	app := s.deployed("postgres", "db")
	s.Require().NoError(services.SetSlowQueries(context.Background(), &app, true))
	tmpl, _ := templates.Find("postgres")
	var ran [][]string
	s.docker.ExecFunc = func(_ string, cmd []string) ([]byte, error) {
		ran = append(ran, cmd)
		return nil, nil
	}
	b := signedIn(s.T(), &s.TestCase)
	page := fmt.Sprintf("/apps/%d?tab=metrics", app.ID)
	b.visit(page)

	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/slow-queries/reset", app.ID), page, nil)
	s.Equal([][]string{tmpl.Inspect.ResetSlowQueries}, ran)
	_, show := b.visit(page)
	s.Equal("Slow query totals cleared.", show.Flash["success"])

	s.docker.ExecFunc = nil
	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/slow-queries/reset", app.ID), page, nil)
	_, show = b.visit(page)
	s.Equal("db isn't running.", show.Flash["error"])
}

func (s *SlowQueriesTestSuite) TestOnlyATemplateThatNeedsTheSwitchHasOne() {
	custom := createTestApp(s.T(), "blog", nil)
	b := signedIn(s.T(), &s.TestCase)
	page := fmt.Sprintf("/apps/%d", custom.ID)
	b.visit(page)

	b.submit(nethttp.MethodPost, page+"/slow-queries", page, nil).AssertNotFound()
	s.False(s.reload(custom.ID).SlowQueries)
	s.Nil(slowQueries(b, *custom))

	cache := s.deployed("redis", "cache")
	s.docker.ExecFunc = func(string, []string) ([]byte, error) {
		return []byte("# Commandstats\r\ncmdstat_get:calls=2,usec=30,usec_per_call=15.00\r\n"), nil
	}
	page = fmt.Sprintf("/apps/%d", cache.ID)
	_, show := b.visit(page)
	s.Equal("always", show.Props["template"].(map[string]any)["slow_queries"])
	b.submit(nethttp.MethodPost, page+"/slow-queries", page, nil).AssertNotFound()
	queries := slowQueries(b, cache)["report"].(map[string]any)["queries"].([]any)
	s.Equal("GET", queries[0].(map[string]any)["query"], "listed without a switch")
}
