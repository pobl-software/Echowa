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

type PruneTestSuite struct {
	suite.Suite
	tests.TestCase
	docker *docker.Fake
}

func TestPruneTestSuite(t *testing.T) {
	suite.Run(t, new(PruneTestSuite))
}

func (s *PruneTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.docker = s.FakeDocker()
	original := deploy.Dispatch
	deploy.Dispatch = func(uint, time.Duration) error { return nil }
	s.T().Cleanup(func() { deploy.Dispatch = original })
}

// deploy runs deployments of each ref in order and returns their digests.
func (s *PruneTestSuite) deploy(app *models.App, refs ...string) []string {
	var digests []string
	for _, ref := range refs {
		d, err := deploy.Request(context.Background(), app, models.TriggerManual, ref)
		s.Require().NoError(err)
		s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
		var row models.Deployment
		s.Require().NoError(facades.Orm().Query().Where("id", d.ID).FirstOrFail(&row))
		s.Require().NotNil(row.Digest)
		digests = append(digests, *row.Digest)
	}

	return digests
}

func (s *PruneTestSuite) TestKeepsEachAppsLastFiveSuccessfulDeployments() {
	blog := createTestApp(s.T(), "blog", nil)
	shop := createTestApp(s.T(), "shop", nil)
	createTestApp(s.T(), "never-deployed", nil)
	blogDigests := s.deploy(blog, "v1", "v2", "v3", "v4", "v5", "v6", "v7")
	shopDigests := s.deploy(shop, "v1", "v2")
	// A failed deploy after v7 doesn't push v3 out of the last five.
	s.docker.Wait = func(docker.ServiceSpec) (docker.UpdateResult, error) {
		return docker.UpdateResult{Outcome: docker.OutcomeRolledBack, Message: "task failed"}, nil
	}
	rolledBack := s.deploy(blog, "v8")[0]
	s.docker.Wait = nil
	// A deploy that has pulled but not yet updated the service.
	inFlight, err := services.CreateDeployment(context.Background(), shop, models.TriggerManual, "v3")
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Model(inFlight).Update(map[string]any{"status": models.DeploymentDeploying, "digest": "ghcr.io/owner/shop@sha256:" + fmt.Sprintf("%064d", 3)})
	s.Require().NoError(err)

	opts, err := services.PruneOptions(context.Background(), services.PruneConfig{Keep: 5, MinAge: 72 * time.Hour})
	s.Require().NoError(err)

	s.ElementsMatch([]string{"ghcr.io/owner/blog", "ghcr.io/owner/shop", "ghcr.io/owner/never-deployed"}, opts.Repositories)
	s.Equal(72*time.Hour, opts.OlderThan)
	s.ElementsMatch(append(append(blogDigests[2:], shopDigests...), "ghcr.io/owner/shop@sha256:"+fmt.Sprintf("%064d", 3)), opts.Keep)
	s.NotContains(opts.Keep, blogDigests[0])
	s.NotContains(opts.Keep, blogDigests[1])
	s.NotContains(opts.Keep, rolledBack)
}

func (s *PruneTestSuite) TestRunPruneReportsAndRemembersWhatItRemoved() {
	app := createTestApp(s.T(), "blog", nil)
	digests := s.deploy(app, "v1")
	s.docker.Prune = func(docker.PruneOptions) (docker.PruneReport, error) {
		return docker.PruneReport{Removed: []string{"ghcr.io/owner/blog:v0"}, SpaceReclaimed: 1 << 20}, nil
	}

	run, err := services.RunPrune(context.Background(), services.PruneScheduled)

	s.Require().NoError(err)
	s.Equal([]string{"ghcr.io/owner/blog:v0"}, run.Removed)
	s.Equal(digests, run.Kept)
	s.EqualValues(1<<20, run.SpaceReclaimed)
	s.NotNil(run.FinishedAt)
	current, last := services.PruneState()
	s.Nil(current)
	s.Equal(run, *last)
	s.Len(s.docker.Prunes(), 1)
}

func (s *PruneTestSuite) TestPruneNowRunsInTheBackgroundOneAtATime() {
	createTestApp(s.T(), "blog", nil)
	release := make(chan struct{})
	s.docker.Prune = func(docker.PruneOptions) (docker.PruneReport, error) {
		<-release
		return docker.PruneReport{Removed: []string{"ghcr.io/owner/blog:old"}}, nil
	}
	b := signedIn(s.T(), &s.TestCase)
	_, page := b.visit("/settings")
	page.AssertComponent("Settings/Index")
	prune := page.Props["prune"].(map[string]any)
	s.EqualValues(5, prune["keep"])
	s.Equal("3 days", prune["min_age"])
	s.Nil(prune["running"])

	b.submit(nethttp.MethodPost, "/settings/prune", "/settings", nil).AssertHeader("Location", "/settings")
	_, page = b.visit("/settings")
	s.Equal("Pruning images.", page.Flash["success"])
	s.NotNil(page.Props["prune"].(map[string]any)["running"], "the page polls while this is set")

	b.submit(nethttp.MethodPost, "/settings/prune", "/settings", nil)
	_, page = b.visit("/settings")
	s.Equal("A prune is already running.", page.Flash["info"])

	close(release)
	s.Eventually(func() bool {
		current, _ := services.PruneState()
		return current == nil
	}, 2*time.Second, 10*time.Millisecond)
	_, page = b.visit("/settings")
	last := page.Props["prune"].(map[string]any)["last"].(map[string]any)
	s.Equal("manual", last["trigger"])
	s.Equal([]any{"ghcr.io/owner/blog:old"}, last["removed"])
	s.Len(s.docker.Prunes(), 1)
}

func (s *PruneTestSuite) TestGuestsCantPrune() {
	b := newBrowser(s.T(), &s.TestCase)
	b.request(nethttp.MethodGet, "/settings", nil, true, nil).AssertHeader("Location", "/login")
	s.Empty(s.docker.Prunes())
}
