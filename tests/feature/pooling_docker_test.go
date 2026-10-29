//go:build docker

// Turns connection pooling on for a PostgreSQL app on a real Swarm
// manager, through Echowa's own deploy and pooler sync, so the spec
// services.PoolerSpec builds runs for real (ADR 0035). Run it with
// deploy/dev-vm.sh test-docker.
package feature

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"goravel/app/deploy"
	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
	"goravel/app/templates"
	"goravel/tests"
)

type PoolingDockerTestSuite struct {
	suite.Suite
	tests.TestCase
	engine docker.Client
}

func TestPoolingDockerTestSuite(t *testing.T) {
	suite.Run(t, new(PoolingDockerTestSuite))
}

func (s *PoolingDockerTestSuite) SetupTest() {
	s.RefreshDatabase()
	cfg := docker.DefaultConfig()
	cfg.UpdateMonitor = 2 * time.Second
	cfg.DeployTimeout = 3 * time.Minute
	cfg.PollInterval = 500 * time.Millisecond
	engine, err := docker.New(cfg)
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = engine.Close() })
	if err := engine.Check(context.Background()); err != nil {
		s.T().Skipf("needs a local Swarm manager: %s", err)
	}
	if out, err := exec.Command("docker", "network", "inspect", cfg.Network).CombinedOutput(); err != nil {
		s.T().Skipf("needs the %s network (deploy/dev-vm.sh up): %s", cfg.Network, out)
	}
	s.engine = engine
	s.UseReal(s.T(), engine)

	original := deploy.Dispatch
	deploy.Dispatch = func(uint, time.Duration) error { return nil }
	s.T().Cleanup(func() {
		services.WaitForPoolers()
		deploy.Dispatch = original
	})
}

// deployed creates a PostgreSQL app and deploys it as the New app page
// would, and removes its services and volume when the test ends.
func (s *PoolingDockerTestSuite) deployed() *models.App {
	ctx := context.Background()
	tmpl, _ := templates.Find("postgres")
	app, err := services.CreateApp(ctx, services.TemplateInput(tmpl, fmt.Sprintf("it-pool-%d", time.Now().UnixNano()%1_000_000)), &tmpl)
	s.Require().NoError(err)
	s.T().Cleanup(func() {
		_ = s.engine.RemoveService(context.Background(), app.PoolerServiceName())
		_ = s.engine.RemoveService(context.Background(), app.ServiceName())
		deadline := time.Now().Add(time.Minute)
		for s.engine.RemoveVolume(context.Background(), templates.VolumeName(app.Name, "data")) != nil && time.Now().Before(deadline) {
			time.Sleep(time.Second)
		}
	})
	s.redeploy(app)

	return app
}

func (s *PoolingDockerTestSuite) redeploy(app *models.App) {
	ctx := context.Background()
	d, err := deploy.Request(ctx, app, models.TriggerManual, app.DefaultTag)
	s.Require().NoError(err)
	s.Require().NoError(deploy.RunQueued(ctx, app.ID))
	s.Require().NoError(facades.Orm().Query().Find(d, d.ID))
	s.Require().Equal(models.DeploymentSucceeded, d.Status, "deploy: %v", d.Error)
	services.WaitForPoolers()
}

// throughPooler runs a query from the database's container through the
// pooler, as another app on the network would connect.
func (s *PoolingDockerTestSuite) throughPooler(app *models.App, query string) string {
	s.T().Helper()
	cmd := []string{"sh", "-c", `PGPASSWORD="$POSTGRES_PASSWORD" exec psql -h "$1" -p 6432 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 -tAc "$2"`,
		"sh", app.PoolerServiceName(), query}
	var out, errs bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	code, err := s.engine.ExecStream(ctx, app.ServiceName(), cmd, nil, &out, &errs)
	s.Require().NoError(err)
	s.Require().Equal(0, code, errs.String())

	return strings.TrimSpace(out.String())
}

func (s *PoolingDockerTestSuite) poolerTasks(app *models.App) []string {
	tasks, err := s.engine.ServiceTasks(context.Background(), app.PoolerServiceName())
	s.Require().NoError(err)
	var running []string
	for _, task := range tasks {
		if task.Running {
			running = append(running, task.ID)
		}
	}

	return running
}

func (s *PoolingDockerTestSuite) TestAPooledDatabaseServesThroughItsPoolerAcrossRedeploys() {
	ctx := context.Background()
	app := s.deployed()

	s.Require().NoError(services.SetPooling(ctx, app, true))
	services.WaitForPoolers()
	status := services.AppPoolerStatus(ctx, app)
	s.Require().Empty(status.Error)
	s.Equal(services.StateRunning, status.Service.State)
	live, err := s.engine.ServiceStatus(ctx, app.PoolerServiceName())
	s.Require().NoError(err)
	postgres, _ := templates.Find("postgres")
	s.Equal(postgres.Pooler.Limits, live.Limits, "the template's caps, not half the server")
	s.Equal("1", s.throughPooler(app, "select 1"))

	tasks := s.poolerTasks(app)
	s.Require().Len(tasks, 1)
	s.Require().NoError(services.SyncPoolers(ctx))
	s.Equal(tasks, s.poolerTasks(app), "a restart of Echowa leaves the pooler's clients connected")

	s.redeploy(app)
	s.Equal(tasks, s.poolerTasks(app), "a redeploy of the database alone leaves the pooler running")
	s.Equal("2", s.throughPooler(app, "select 2"), "and it reaches the new task")

	s.Require().NoError(services.SetPooling(ctx, app, false))
	services.WaitForPoolers()
	_, err = s.engine.ServiceStatus(ctx, app.PoolerServiceName())
	s.ErrorIs(err, docker.ErrServiceNotFound)
	_, err = s.engine.ServiceStatus(ctx, app.ServiceName())
	s.NoError(err, "the database keeps running")
}
