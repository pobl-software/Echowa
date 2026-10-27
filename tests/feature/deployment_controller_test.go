package feature

import (
	"context"
	"fmt"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"goravel/app/deploy"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
	"goravel/tests"
)

type DeploymentControllerTestSuite struct {
	suite.Suite
	tests.TestCase
}

func TestDeploymentControllerTestSuite(t *testing.T) {
	suite.Run(t, new(DeploymentControllerTestSuite))
}

func (s *DeploymentControllerTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.FakeDocker()
}

func (s *DeploymentControllerTestSuite) jobs() int64 {
	count, err := facades.DB().Table("jobs").Count()
	s.Require().NoError(err)

	return count
}

func (s *DeploymentControllerTestSuite) TestDeployQueuesTheDefaultTagOnTheDatabaseQueue() {
	app := createTestApp(s.T(), "blog", nil)
	b := signedIn(s.T(), &s.TestCase)
	showURL := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(showURL)

	resp := b.submit(nethttp.MethodPost, showURL+"/deploy", showURL, map[string]any{"ref": ""})

	var deployment models.Deployment
	s.Require().NoError(facades.Orm().Query().Where("app_id", app.ID).FirstOrFail(&deployment))
	resp.AssertHeader("Location", fmt.Sprintf("/apps/%d/deployments/%d", app.ID, deployment.ID))
	s.Equal("latest", deployment.Ref)
	s.Equal(models.TriggerManual, deployment.Trigger)
	s.Equal(models.DeploymentQueued, deployment.Status)
	s.EqualValues(1, s.jobs(), "one job waits on the database queue for the in-process worker")

	b.submit(nethttp.MethodPost, showURL+"/deploy", showURL, map[string]any{"ref": "v2.0.0"})
	var latest models.Deployment
	s.Require().NoError(facades.Orm().Query().Where("app_id", app.ID).Order("id desc").First(&latest))
	s.Equal("v2.0.0", latest.Ref)
}

func (s *DeploymentControllerTestSuite) TestDeployRejectsBadRefs() {
	app := createTestApp(s.T(), "blog", nil)
	b := signedIn(s.T(), &s.TestCase)
	showURL := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(showURL)

	b.submit(nethttp.MethodPost, showURL+"/deploy", showURL, map[string]any{"ref": "ghcr.io/other/image:v1"}).
		AssertHeader("Location", showURL)
	_, page := b.visit(showURL)
	s.Contains(page.Props["errors"], "ref")
	s.Zero(s.jobs())
}

func (s *DeploymentControllerTestSuite) TestShowPageListsDeploymentsAndTheDetailHasTheLog() {
	app := createTestApp(s.T(), "blog", nil)
	other := createTestApp(s.T(), "shop", nil)
	s.Require().NoError(services.SetEnvVars(context.Background(), app.ID, map[string]string{"API_KEY": "envSecretValue"}))
	original := deploy.Dispatch
	deploy.Dispatch = func(uint, time.Duration) error { return nil }
	s.T().Cleanup(func() { deploy.Dispatch = original })
	d, err := deploy.Request(context.Background(), app, models.TriggerManual, "v1")
	s.Require().NoError(err)
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	b := signedIn(s.T(), &s.TestCase)
	b.forbid("envSecretValue", string(app.WebhookSecret))

	_, page := b.visit(fmt.Sprintf("/apps/%d?tab=deployments", app.ID))
	deployments := page.Props["deployments"].([]any)
	s.Require().Len(deployments, 1)
	row := deployments[0].(map[string]any)
	s.Equal("succeeded", row["status"])
	s.NotContains(row, "log")
	s.EqualValues(d.ID, page.Props["app"].(map[string]any)["current_deployment_id"])

	_, page = b.visit(fmt.Sprintf("/apps/%d/deployments/%d", app.ID, d.ID))
	page.AssertComponent("Deployments/Show")
	detail := page.Props["deployment"].(map[string]any)
	s.Contains(detail["log"], "Deployment succeeded")
	s.Equal("echowa-blog", page.Props["serviceName"])

	b.request(nethttp.MethodGet, fmt.Sprintf("/apps/%d/deployments/%d", other.ID, d.ID), nil, true, nil).AssertNotFound()
	b.request(nethttp.MethodGet, fmt.Sprintf("/apps/%d/deployments/999", app.ID), nil, true, nil).AssertNotFound()
}
