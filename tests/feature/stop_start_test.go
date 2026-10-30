package feature

import (
	"context"
	"fmt"
	nethttp "net/http"
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

type StopStartTestSuite struct {
	suite.Suite
	tests.TestCase
	docker *docker.Fake
}

func TestStopStartTestSuite(t *testing.T) {
	suite.Run(t, new(StopStartTestSuite))
}

func (s *StopStartTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.docker = s.FakeDocker()
	original := deploy.Dispatch
	deploy.Dispatch = func(uint, time.Duration) error { return nil }
	s.T().Cleanup(func() { deploy.Dispatch = original })
}

func (s *StopStartTestSuite) deployed(app *models.App, ref string) models.Deployment {
	d, err := deploy.Request(context.Background(), app, models.TriggerManual, ref)
	s.Require().NoError(err)
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	var row models.Deployment
	s.Require().NoError(facades.Orm().Query().Where("id", d.ID).FirstOrFail(&row))

	return row
}

func (s *StopStartTestSuite) latest(appID uint) models.Deployment {
	var d models.Deployment
	s.Require().NoError(facades.Orm().Query().Where("app_id", appID).Order("id desc").First(&d))

	return d
}

func (s *StopStartTestSuite) state(b *browser, url string) string {
	_, page := b.visit(url)

	return page.Props["status"].(map[string]any)["state"].(string)
}

func (s *StopStartTestSuite) TestStoppingScalesTheServiceToZero() {
	app := createTestApp(s.T(), "blog", nil)
	s.deployed(app, "v1")
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d", app.ID)
	s.Equal("running", s.state(b, url))

	resp := b.submit(nethttp.MethodPost, url+"/stop", url, nil)

	resp.AssertHeader("Location", url)
	s.Equal([]string{app.ServiceName()}, s.docker.Stopped())
	_, page := b.visit(url)
	s.Contains(page.Flash["success"], "Stopping blog.")
	s.Equal("stopped", page.Props["status"].(map[string]any)["state"])
}

func (s *StopStartTestSuite) TestStoppingWaitsForADeploymentToFinish() {
	app := createTestApp(s.T(), "blog", nil)
	s.deployed(app, "v1")
	queued, err := deploy.Request(context.Background(), app, models.TriggerManual, "v2")
	s.Require().NoError(err)
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(url)

	b.submit(nethttp.MethodPost, url+"/stop", url, nil).AssertHeader("Location", url)

	_, page := b.visit(url)
	s.Contains(page.Flash["error"], fmt.Sprintf("Deployment #%d of blog is running.", queued.ID))
	s.Empty(s.docker.Stopped())
}

func (s *StopStartTestSuite) TestStoppingWaitsForOtherWorkOnTheApp() {
	app := createTestApp(s.T(), "blog", nil)
	s.deployed(app, "v1")
	release, _, ok := services.LockApp(app.ID, services.WorkBackup)
	s.Require().True(ok)
	defer release()
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(url)

	b.submit(nethttp.MethodPost, url+"/stop", url, nil)

	_, page := b.visit(url)
	s.Contains(page.Flash["error"], "A backup of blog is running.")
	s.Empty(s.docker.Stopped())
}

func (s *StopStartTestSuite) TestStoppingAnAppThatWasNeverDeployed() {
	app := createTestApp(s.T(), "blog", nil)
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(url)

	b.submit(nethttp.MethodPost, url+"/stop", url, nil)

	_, page := b.visit(url)
	s.Contains(page.Flash["error"], "blog isn't deployed.")
	s.Empty(s.docker.Stopped())
}

func (s *StopStartTestSuite) TestStartingDeploysTheImageItRan() {
	app := createTestApp(s.T(), "blog", nil)
	v1 := s.deployed(app, "v1")
	v2 := s.deployed(app, "v2")
	s.Require().NotEqual(*v1.Digest, *v2.Digest)
	s.Require().NoError(services.StopApp(context.Background(), app))
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d", app.ID)
	s.Equal("stopped", s.state(b, url))

	b.submit(nethttp.MethodPost, url+"/start", url, nil).AssertHeader("Location", url)

	started := s.latest(app.ID)
	s.Equal(models.TriggerManual, started.Trigger)
	s.Equal(models.DeploymentQueued, started.Status)
	s.Equal(*v2.Digest, "ghcr.io/owner/blog@"+started.Ref, "the digest it ran, not the default tag")
	_, page := b.visit(url)
	s.Contains(page.Flash["success"], fmt.Sprintf("Starting blog. Deployment #%d deploys the image it ran.", started.ID))
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	s.Equal("running", s.state(b, url))
}

func (s *StopStartTestSuite) reload(id uint) models.App {
	var app models.App
	s.Require().NoError(facades.Orm().Query().Where("id", id).FirstOrFail(&app))

	return app
}

func (s *StopStartTestSuite) TestStartingNeedsAStoppedApp() {
	app := createTestApp(s.T(), "blog", nil)
	s.deployed(app, "v1")
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(url)
	before := s.latest(app.ID)

	b.submit(nethttp.MethodPost, url+"/start", url, nil)
	_, page := b.visit(url)
	s.Contains(page.Flash["error"], "blog isn't stopped.")

	s.docker.Status = func(name string) (docker.ServiceStatus, error) {
		return docker.ServiceStatus{Name: name, Desired: 0, Running: 1}, nil
	}
	b.submit(nethttp.MethodPost, url+"/start", url, nil)
	_, page = b.visit(url)
	s.Contains(page.Flash["error"], "blog is still stopping.")

	s.Equal(before.ID, s.latest(app.ID).ID, "nothing was queued")
}

func (s *StopStartTestSuite) TestStartingWaitsForADeploymentToFinish() {
	app := createTestApp(s.T(), "blog", nil)
	s.deployed(app, "v1")
	s.Require().NoError(services.StopApp(context.Background(), app))
	queued, err := deploy.Request(context.Background(), app, models.TriggerManual, "v2")
	s.Require().NoError(err)
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(url)

	b.submit(nethttp.MethodPost, url+"/start", url, nil)

	_, page := b.visit(url)
	s.Contains(page.Flash["error"], fmt.Sprintf("Deployment #%d of blog is running.", queued.ID))
	s.Equal(queued.ID, s.latest(app.ID).ID)
}

func (s *StopStartTestSuite) TestASettingsChangeWaitsForAStoppedAppToStart() {
	tmpl, _ := templates.Find("postgres")
	app, err := services.CreateApp(context.Background(), services.AppInput{
		Name: "db", Repository: tmpl.Repository, DefaultTag: tmpl.DefaultTag, Port: tmpl.Port, Replicas: 1,
	}, &tmpl)
	s.Require().NoError(err)
	s.deployed(app, app.DefaultTag)
	s.Require().NoError(services.StopApp(context.Background(), app))
	b := signedIn(s.T(), &s.TestCase)
	url := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(url)
	before := s.latest(app.ID)

	resp := b.submit(nethttp.MethodPost, url+"/slow-queries", url, nil)

	resp.AssertHeader("Location", url)
	s.True(s.reload(app.ID).SlowQueries, "the setting is saved")
	s.Equal(before.ID, s.latest(app.ID).ID, "no deployment, which would start it")
	_, page := b.visit(url)
	s.Contains(page.Flash["success"], "Slow queries on. It applies when you start db.")
	s.Equal("stopped", page.Props["status"].(map[string]any)["state"])
}
