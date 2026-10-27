package feature

import (
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"testing"

	"github.com/stretchr/testify/suite"

	"goravel/app/docker"
	"goravel/app/models"
	"goravel/app/services"
	"goravel/app/templates"
	"goravel/tests"
)

type DashboardControllerTestSuite struct {
	suite.Suite
	tests.TestCase
	docker *docker.Fake
}

func TestDashboardControllerTestSuite(t *testing.T) {
	suite.Run(t, new(DashboardControllerTestSuite))
}

func (s *DashboardControllerTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.docker = s.FakeDocker()
}

func (s *DashboardControllerTestSuite) TestGuestsAreSentToLogin() {
	b := newBrowser(s.T(), &s.TestCase)
	b.request(nethttp.MethodGet, "/dashboard", nil, true, nil).AssertHeader("Location", "/login")
	b.request(nethttp.MethodGet, "/", nil, false, nil).AssertHeader("Location", "/dashboard")
}

func (s *DashboardControllerTestSuite) TestListsAppsWithLiveStatusAndLastDeployment() {
	ctx := context.Background()
	running := createTestApp(s.T(), "blog", nil)
	notDeployed := createTestApp(s.T(), "shop", nil)
	s.Require().NoError(s.docker.DeployService(ctx, docker.ServiceSpec{
		Name: running.ServiceName(), App: "blog", Replicas: 2,
		Image: "ghcr.io/owner/blog@sha256:3f1c2a9b8d7e0123456789abcdef0123456789abcdef0123456789abcdef0123",
	}))
	_, err := services.CreateDeployment(ctx, running, models.TriggerManual, "v1")
	s.Require().NoError(err)
	latest, err := services.CreateDeployment(ctx, running, models.TriggerWebhook, "v2")
	s.Require().NoError(err)

	b := signedIn(s.T(), &s.TestCase)
	b.forbid(string(running.WebhookSecret), string(notDeployed.WebhookSecret))
	_, page := b.visit("/dashboard")

	page.AssertComponent("Dashboard")
	apps := page.Props["apps"].([]any)
	s.Require().Len(apps, 2)
	blog := apps[0].(map[string]any)
	s.Equal("blog", blog["name"])
	s.Equal("blog.example.com", blog["domain"])
	status := blog["status"].(map[string]any)
	s.Equal(services.StateRunning, status["state"])
	s.EqualValues(2, status["running"])
	s.EqualValues(2, status["desired"])
	s.Equal("3f1c2a9b8d7e", status["digest"])
	last := blog["last_deployment"].(map[string]any)
	s.EqualValues(latest.ID, last["id"])
	s.Equal("queued", last["status"])
	s.NotContains(last, "log", "lists don't carry deploy logs")

	shop := apps[1].(map[string]any)
	s.Equal(services.StateNotDeployed, shop["status"].(map[string]any)["state"])
	s.Nil(shop["last_deployment"])
}

func (s *DashboardControllerTestSuite) TestUnknownStatusWhenDockerDoesNotAnswer() {
	createTestApp(s.T(), "blog", nil)
	s.docker.Status = func(string) (docker.ServiceStatus, error) {
		return docker.ServiceStatus{}, errors.New("connection refused")
	}

	_, page := signedIn(s.T(), &s.TestCase).visit("/dashboard")

	app := page.Props["apps"].([]any)[0].(map[string]any)
	s.Equal(services.StateUnknown, app["status"].(map[string]any)["state"])
}

func (s *DashboardControllerTestSuite) TestDockerBanner() {
	b := signedIn(s.T(), &s.TestCase)

	_, page := b.visit("/dashboard")
	s.Nil(page.Props["dockerProblem"], "no banner when Docker is a Swarm manager")

	s.docker.CheckErr = fmt.Errorf("%w (node state %q)", docker.ErrNotSwarm, "inactive")
	_, page = b.visit("/registries")
	s.Contains(page.Props["dockerProblem"], "docker swarm init")

	s.docker.CheckErr = fmt.Errorf("%w: dial unix /var/run/docker.sock: connect: no such file", docker.ErrDaemonUnreachable)
	_, page = b.visit("/dashboard")
	s.Contains(page.Props["dockerProblem"], "can't reach the Docker daemon")

	s.docker.CheckErr = docker.ErrNotManager
	_, page = b.visit("/dashboard")
	s.Contains(page.Props["dockerProblem"], "isn't a Swarm manager")

	_, page = newBrowser(s.T(), &s.TestCase).visit("/login")
	s.Nil(page.Props["dockerProblem"], "guests don't trigger a Docker check")
}

func (s *DashboardControllerTestSuite) TestEachAppListsTheDatabasesLinkedToIt() {
	ctx := context.Background()
	shop := createTestApp(s.T(), "shop", nil)
	createTestApp(s.T(), "blog", nil)
	tmpl, _ := templates.Find("postgres")
	db, err := services.CreateApp(ctx, services.TemplateInput(tmpl, "db"), &tmpl)
	s.Require().NoError(err)
	s.Require().NoError(services.LinkApps(ctx, shop.ID, db.ID))

	b := signedIn(s.T(), &s.TestCase)
	_, page := b.visit("/dashboard")

	apps := map[string]map[string]any{}
	for _, app := range page.Props["apps"].([]any) {
		apps[app.(map[string]any)["name"].(string)] = app.(map[string]any)
	}
	s.Equal([]any{float64(db.ID)}, apps["shop"]["databases"])
	s.Equal([]any{}, apps["blog"]["databases"], "an empty list, not null")
	s.Nil(apps["blog"]["template"])
	s.Equal("postgres", apps["db"]["template"])
	s.Equal([]any{}, apps["db"]["databases"])
	s.Len(apps, 3, "the database is still listed, for the stats and its row")
}
