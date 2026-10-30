//go:build docker

// Mounts a volume on a custom app on a real Swarm manager, through Echowa's
// own deploy, and checks what survives (ADR 0039): a file in the volume
// outlives a redeploy and a move to another path, one in the container's own
// filesystem doesn't, and a removed volume is gone from Docker once the next
// deploy succeeds. Run it with deploy/dev-vm.sh test-docker. It pulls
// nginx:alpine.
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

type VolumesDockerTestSuite struct {
	suite.Suite
	tests.TestCase
	engine docker.Client
}

func TestVolumesDockerTestSuite(t *testing.T) {
	suite.Run(t, new(VolumesDockerTestSuite))
}

func (s *VolumesDockerTestSuite) SetupTest() {
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
	s.T().Cleanup(func() { deploy.Dispatch = original })
}

// customApp creates an nginx app with no domain, and removes its service
// and volumes when the test ends.
func (s *VolumesDockerTestSuite) customApp() *models.App {
	app, err := services.CreateApp(context.Background(), services.AppInput{
		Name:       fmt.Sprintf("it-vol-%d", time.Now().UnixNano()%1_000_000),
		Repository: "nginx", DefaultTag: "alpine", Port: 80, Replicas: 1,
	}, nil)
	s.Require().NoError(err)
	s.T().Cleanup(func() {
		_ = s.engine.RemoveService(context.Background(), app.ServiceName())
		for _, name := range []string{templates.VolumeName(app.Name, "data")} {
			deadline := time.Now().Add(time.Minute)
			for s.engine.RemoveVolume(context.Background(), name) != nil && time.Now().Before(deadline) {
				time.Sleep(time.Second)
			}
		}
	})

	return app
}

func (s *VolumesDockerTestSuite) redeploy(app *models.App) models.Deployment {
	ctx := context.Background()
	d, err := deploy.Request(ctx, app, models.TriggerManual, app.DefaultTag)
	s.Require().NoError(err)
	s.Require().NoError(deploy.RunQueued(ctx, app.ID))
	s.Require().NoError(facades.Orm().Query().Find(d, d.ID))
	s.Require().Equal(models.DeploymentSucceeded, d.Status, "deploy: %v", d.Error)

	return *d
}

// sh runs script in the app's container and returns its output.
func (s *VolumesDockerTestSuite) sh(app *models.App, script string) string {
	s.T().Helper()
	var out, errs bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	code, err := s.engine.ExecStream(ctx, app.ServiceName(), []string{"sh", "-c", script}, nil, &out, &errs)
	s.Require().NoError(err)
	s.Require().Equal(0, code, errs.String())

	return strings.TrimSpace(out.String())
}

func (s *VolumesDockerTestSuite) volumeExists(name string) bool {
	exists, err := s.engine.VolumeExists(context.Background(), name)
	s.Require().NoError(err)

	return exists
}

func (s *VolumesDockerTestSuite) volumeRows(app *models.App) []models.AppVolume {
	var rows []models.AppVolume
	s.Require().NoError(facades.Orm().Query().Where("app_id", app.ID).Get(&rows))

	return rows
}

func (s *VolumesDockerTestSuite) TestAVolumeKeepsItsFilesAndARemovedOneIsDeleted() {
	ctx := context.Background()
	app := s.customApp()
	name := templates.VolumeName(app.Name, "data")
	_, err := services.AddVolume(ctx, app, "data", "/data")
	s.Require().NoError(err)

	s.redeploy(app)
	s.True(s.volumeExists(name), "the first deploy creates the volume")
	s.sh(app, "echo kept > /data/note; echo gone > /tmp/note")

	d := s.redeploy(app)
	s.Contains(d.Log, "stop-first")
	s.Equal("kept", s.sh(app, "cat /data/note"), "a file in the volume survives a redeploy")
	s.Equal("missing", s.sh(app, "cat /tmp/note 2>/dev/null || echo missing"), "one in the container's own filesystem doesn't")

	var volume models.AppVolume
	s.Require().NoError(facades.Orm().Query().Where("name", name).FirstOrFail(&volume))
	s.Require().NoError(services.MoveVolume(ctx, &volume, "/srv/data"))
	s.redeploy(app)
	s.Equal("kept", s.sh(app, "cat /srv/data/note"), "the data moves with the path")
	s.Equal("missing", s.sh(app, "cat /data/note 2>/dev/null || echo missing"))

	s.Require().NoError(services.MarkVolumeRemoved(ctx, &volume))
	s.True(s.volumeExists(name), "nothing is removed until a deploy has stopped mounting it")
	d = s.redeploy(app)
	s.False(s.volumeExists(name), "the deploy removes the volume once its old task is gone")
	s.Empty(s.volumeRows(app))
	s.Contains(d.Log, "Removed volume "+name)
	s.Equal("missing", s.sh(app, "cat /srv/data/note 2>/dev/null || echo missing"))
}
