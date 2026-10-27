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
	"goravel/tests"
)

type RollbackTestSuite struct {
	suite.Suite
	tests.TestCase
	docker *docker.Fake
}

func TestRollbackTestSuite(t *testing.T) {
	suite.Run(t, new(RollbackTestSuite))
}

func (s *RollbackTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.docker = s.FakeDocker()
	original := deploy.Dispatch
	deploy.Dispatch = func(uint, time.Duration) error { return nil }
	s.T().Cleanup(func() { deploy.Dispatch = original })
}

// deployed runs a deployment of ref to the end and returns it.
func (s *RollbackTestSuite) deployed(app *models.App, ref string) models.Deployment {
	d, err := deploy.Request(context.Background(), app, models.TriggerManual, ref)
	s.Require().NoError(err)
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	var row models.Deployment
	s.Require().NoError(facades.Orm().Query().Where("id", d.ID).FirstOrFail(&row))

	return row
}

func (s *RollbackTestSuite) latest(appID uint) models.Deployment {
	var d models.Deployment
	s.Require().NoError(facades.Orm().Query().Where("app_id", appID).Order("id desc").First(&d))

	return d
}

func (s *RollbackTestSuite) TestRedeploysTheExactDigestOfAnEarlierDeployment() {
	app := createTestApp(s.T(), "blog", nil)
	v1 := s.deployed(app, "v1")
	v2 := s.deployed(app, "v2")
	s.Require().NotEqual(*v1.Digest, *v2.Digest)
	b := signedIn(s.T(), &s.TestCase)
	showURL := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(showURL)

	resp := b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/deployments/%d/redeploy", app.ID, v1.ID), showURL, nil)

	rollback := s.latest(app.ID)
	resp.AssertHeader("Location", fmt.Sprintf("/apps/%d/deployments/%d", app.ID, rollback.ID))
	s.Equal(models.TriggerRollback, rollback.Trigger)
	s.Equal(models.DeploymentQueued, rollback.Status)
	s.Equal(*v1.Digest, "ghcr.io/owner/blog@"+rollback.Ref, "the ref is v1's digest, not its tag")

	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	status, err := s.docker.ServiceStatus(context.Background(), app.ServiceName())
	s.Require().NoError(err)
	s.Equal(*v1.Digest, status.Image)
	s.Equal(*v1.Digest, s.docker.Pulls()[len(s.docker.Pulls())-1], "pulls by digest")
	var reloaded models.App
	s.Require().NoError(facades.Orm().Query().Where("id", app.ID).FirstOrFail(&reloaded))
	s.Equal(rollback.ID, *reloaded.CurrentDeploymentID)

	_, page := b.visit(showURL)
	s.Contains(page.Flash["success"], fmt.Sprintf("redeploying %s from #%d", services.ShortDigest(*v1.Digest), v1.ID))
}

func (s *RollbackTestSuite) TestOnlySucceededDeploymentsOfTheSameRepositoryCanBeRedeployed() {
	app := createTestApp(s.T(), "blog", nil)
	other := createTestApp(s.T(), "shop", nil)
	good := s.deployed(app, "v1")
	s.docker.Pull = func(string, *docker.RegistryAuth) (string, error) { return "", fmt.Errorf("manifest unknown") }
	failed := s.deployed(app, "v2")
	s.docker.Pull = nil
	s.Require().Equal(models.DeploymentFailed, failed.Status)
	b := signedIn(s.T(), &s.TestCase)
	showURL := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(showURL)
	redeploy := func(appID, deploymentID uint) string {
		return fmt.Sprintf("/apps/%d/deployments/%d/redeploy", appID, deploymentID)
	}

	b.submit(nethttp.MethodPost, redeploy(app.ID, failed.ID), showURL, nil).AssertHeader("Location", showURL)
	_, page := b.visit(showURL)
	s.Contains(page.Flash["error"], "didn't succeed")

	b.request(nethttp.MethodPost, redeploy(other.ID, good.ID), nil, true, map[string]string{
		"X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie), "Referer": testOrigin + showURL,
	}).AssertNotFound()

	_, err := facades.Orm().Query().Model(&models.App{}).Where("id", app.ID).Update("repository", "ghcr.io/owner/renamed")
	s.Require().NoError(err)
	b.submit(nethttp.MethodPost, redeploy(app.ID, good.ID), showURL, nil).AssertHeader("Location", showURL)
	_, page = b.visit(showURL)
	s.Contains(page.Flash["error"], "now pulls from ghcr.io/owner/renamed")

	s.Equal(failed.ID, s.latest(app.ID).ID, "nothing was queued")
}
