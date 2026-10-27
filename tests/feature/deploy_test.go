package feature

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goravel/framework/support/carbon"
	"github.com/stretchr/testify/suite"

	"goravel/app/deploy"
	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
	"goravel/tests"
)

type DeployTestSuite struct {
	suite.Suite
	tests.TestCase
	docker     *docker.Fake
	dispatched []dispatch
	mu         sync.Mutex
}

type dispatch struct {
	appID uint
	delay time.Duration
}

func TestDeployTestSuite(t *testing.T) {
	suite.Run(t, new(DeployTestSuite))
}

func (s *DeployTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.docker = s.FakeDocker()
	s.dispatched = nil
	original := deploy.Dispatch
	deploy.Dispatch = func(appID uint, delay time.Duration) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.dispatched = append(s.dispatched, dispatch{appID, delay})
		return nil
	}
	s.T().Cleanup(func() { deploy.Dispatch = original })
}

func (s *DeployTestSuite) request(app *models.App, ref string) *models.Deployment {
	d, err := deploy.Request(context.Background(), app, models.TriggerManual, ref)
	s.Require().NoError(err)

	return d
}

func (s *DeployTestSuite) load(id uint) models.Deployment {
	var d models.Deployment
	s.Require().NoError(facades.Orm().Query().Where("id", id).FirstOrFail(&d))

	return d
}

func (s *DeployTestSuite) loadApp(id uint) models.App {
	var app models.App
	s.Require().NoError(facades.Orm().Query().Where("id", id).FirstOrFail(&app))

	return app
}

func (s *DeployTestSuite) TestRequestQueuesAndDispatches() {
	app := createTestApp(s.T(), "blog", nil)

	d := s.request(app, "v1")

	s.Equal(models.DeploymentQueued, d.Status)
	s.Equal([]dispatch{{app.ID, 0}}, s.dispatched)
}

func (s *DeployTestSuite) TestSuccessfulDeploy() {
	registry := createTestRegistry(s.T(), registryToken)
	app := createTestApp(s.T(), "blog", &registry.ID)
	s.Require().NoError(services.SetEnvVars(context.Background(), app.ID, map[string]string{"API_KEY": "envSecretValue"}))
	var pulledWith *docker.RegistryAuth
	s.docker.Pull = func(ref string, auth *docker.RegistryAuth) (string, error) {
		pulledWith = auth
		return docker.FakeDigest(ref), nil
	}
	d := s.request(app, "v1.2.3")

	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))

	done := s.load(d.ID)
	s.Equal(models.DeploymentSucceeded, done.Status)
	digest := docker.FakeDigest("ghcr.io/owner/blog:v1.2.3")
	s.Equal(digest, *done.Digest)
	s.NotNil(done.StartedAt)
	s.NotNil(done.FinishedAt)
	s.Nil(done.Error)
	s.Equal([]string{"ghcr.io/owner/blog:v1.2.3"}, s.docker.Pulls())
	s.Equal(&docker.RegistryAuth{ServerAddress: "ghcr.io", Username: "louis", Password: registryToken}, pulledWith)

	deploys := s.docker.Deploys()
	s.Require().Len(deploys, 1)
	s.Equal(docker.ServiceSpec{
		Name: "echowa-blog", App: "blog", Image: digest, Replicas: 1,
		Env:    map[string]string{"API_KEY": "envSecretValue"},
		Route:  &docker.Route{Domain: "blog.example.com", Port: 8080, TLS: true},
		Limits: fakeDefaults,
	}, deploys[0])

	s.Equal(done.ID, *s.loadApp(app.ID).CurrentDeploymentID)

	lines := strings.Split(strings.TrimSpace(done.Log), "\n")
	s.Regexp(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ Deployment #\d+ started \(manual, ref v1\.2\.3\)\.$`, lines[0])
	s.Contains(done.Log, "Pulling ghcr.io/owner/blog:v1.2.3 with registry ghcr (ghcr.io as louis).")
	s.Contains(done.Log, "Resolved to "+digest)
	s.Contains(done.Log, "1 replica(s), 1 env var(s), https://blog.example.com to port 8080")
	s.Contains(done.Log, "Deployment succeeded")
	s.NotContains(done.Log, "envSecretValue")
	s.NotContains(done.Log, registryToken)
}

func (s *DeployTestSuite) TestDeployMountsVolumesAndPassesArgs() {
	app := createTestApp(s.T(), "db", nil)
	args := []string{"sh", "-c", `exec server --password "$PASSWORD"`}
	app.Args = args
	s.Require().NoError(facades.Orm().Query().Save(app))
	addTestVolume(s.T(), app, "echowa-db-2", "/var/log/db")
	addTestVolume(s.T(), app, "echowa-db-1", "/var/lib/db")
	d := s.request(app, "v1")

	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))

	done := s.load(d.ID)
	s.Equal(models.DeploymentSucceeded, done.Status)
	deploys := s.docker.Deploys()
	s.Require().Len(deploys, 1)
	s.Equal(args, deploys[0].Args)
	s.Equal([]docker.Volume{
		{Name: "echowa-db-1", Target: "/var/lib/db"},
		{Name: "echowa-db-2", Target: "/var/log/db"},
	}, deploys[0].Volumes, "sorted by path")
	s.Contains(done.Log, "custom command, volume echowa-db-1 at /var/lib/db, volume echowa-db-2 at /var/log/db.")
	s.Contains(done.Log, "stop-first, since a volume has one writer")
	s.NotContains(done.Log, "$PASSWORD", "the log doesn't print the arguments")
}

func (s *DeployTestSuite) TestDeployByDigestPullsTheDigest() {
	app := createTestApp(s.T(), "blog", nil)
	digest := "sha256:" + strings.Repeat("ab", 32)
	d := s.request(app, digest)

	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))

	s.Equal([]string{"ghcr.io/owner/blog@" + digest}, s.docker.Pulls())
	s.Equal(models.DeploymentSucceeded, s.load(d.ID).Status)
}

func (s *DeployTestSuite) TestPullFailure() {
	app := createTestApp(s.T(), "blog", nil)
	previous := s.request(app, "v1")
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	s.docker.Pull = func(ref string, _ *docker.RegistryAuth) (string, error) {
		return "", errors.New("pull " + ref + ": manifest unknown")
	}
	d := s.request(app, "v2")

	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))

	failed := s.load(d.ID)
	s.Equal(models.DeploymentFailed, failed.Status)
	s.Equal("pull ghcr.io/owner/blog:v2: manifest unknown", *failed.Error)
	s.Nil(failed.Digest)
	s.NotNil(failed.FinishedAt)
	s.Contains(failed.Log, "Failed: pull ghcr.io/owner/blog:v2: manifest unknown")
	s.Len(s.docker.Deploys(), 1, "nothing is deployed after a failed pull")
	s.Equal(previous.ID, *s.loadApp(app.ID).CurrentDeploymentID)
}

func (s *DeployTestSuite) TestHealthFailureRollsBack() {
	app := createTestApp(s.T(), "blog", nil)
	healthcheck := "curl -f http://localhost:8080/up"
	app.HealthcheckCmd = &healthcheck
	s.Require().NoError(facades.Orm().Query().Save(app))
	good := s.request(app, "v1")
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))

	s.docker.Wait = func(spec docker.ServiceSpec) (docker.UpdateResult, error) {
		if strings.Contains(spec.Image, docker.FakeDigest("ghcr.io/owner/blog:v2")) {
			return docker.UpdateResult{Outcome: docker.OutcomeRolledBack, Message: "rollback completed"}, nil
		}
		return docker.UpdateResult{Outcome: docker.OutcomeCompleted}, nil
	}
	bad := s.request(app, "v2")
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))

	rolledBack := s.load(bad.ID)
	s.Equal(models.DeploymentRolledBack, rolledBack.Status)
	s.Equal("Swarm rolled the update back: rollback completed", *rolledBack.Error)
	s.Contains(rolledBack.Log, "The previous version is still running.")
	s.Equal(&docker.Healthcheck{Command: healthcheck}, s.docker.Deploys()[1].Healthcheck)
	s.Equal(good.ID, *s.loadApp(app.ID).CurrentDeploymentID, "current stays on the last good deployment")

	status, err := s.docker.ServiceStatus(context.Background(), "echowa-blog")
	s.Require().NoError(err)
	s.Equal(docker.FakeDigest("ghcr.io/owner/blog:v1"), status.Image)
}

func (s *DeployTestSuite) TestPausedUpdateAndWaitErrorsFail() {
	app := createTestApp(s.T(), "blog", nil)
	s.docker.Wait = func(docker.ServiceSpec) (docker.UpdateResult, error) {
		return docker.UpdateResult{Outcome: docker.OutcomePaused, Message: "update paused due to failure"}, nil
	}
	paused := s.request(app, "v1")
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	s.Equal(models.DeploymentFailed, s.load(paused.ID).Status)
	s.Contains(*s.load(paused.ID).Error, "update paused due to failure")

	s.docker.Wait = func(docker.ServiceSpec) (docker.UpdateResult, error) {
		return docker.UpdateResult{}, docker.ErrTimeout
	}
	timedOut := s.request(app, "v2")
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	s.Equal(models.DeploymentFailed, s.load(timedOut.ID).Status)
	s.Nil(s.loadApp(app.ID).CurrentDeploymentID)
}

// Requests for one app queue behind the running deployment and run in
// order. Another app deploys in parallel.
func (s *DeployTestSuite) TestConcurrentRequests() {
	ctx := context.Background()
	blog := createTestApp(s.T(), "blog", nil)
	shop := createTestApp(s.T(), "shop", nil)

	var inFlight, maxInFlight atomic.Int32
	release := make(chan struct{})
	firstStarted := make(chan struct{})
	// Only the first blog deploy blocks. Anything else that waits must not
	// hang the test if the per-app claim ever breaks.
	var blocked atomic.Bool
	s.docker.Deploy = func(spec docker.ServiceSpec) error {
		if spec.App == "blog" {
			n := inFlight.Add(1)
			if n > maxInFlight.Load() {
				maxInFlight.Store(n)
			}
		}
		return nil
	}
	s.docker.Wait = func(spec docker.ServiceSpec) (docker.UpdateResult, error) {
		if spec.App == "blog" {
			if blocked.CompareAndSwap(false, true) {
				close(firstStarted)
				<-release
			}
			inFlight.Add(-1)
		}
		return docker.UpdateResult{Outcome: docker.OutcomeCompleted}, nil
	}

	first := s.request(blog, "v1")
	done := make(chan error, 1)
	go func() { done <- deploy.RunQueued(ctx, blog.ID) }()
	<-firstStarted

	// While v1 is deploying: more requests for blog queue up...
	second := s.request(blog, "v2")
	third := s.request(blog, "v3")
	s.ErrorIs(deploy.RunQueued(ctx, blog.ID), deploy.ErrBusy, "a second runner for the same app returns at once")
	s.Equal(models.DeploymentQueued, s.load(second.ID).Status)

	// ...and the job that hit the busy app comes back later.
	s.Require().NoError((&deploy.Job{}).Handle(blog.ID))
	s.Equal(dispatch{blog.ID, 3 * time.Second}, s.dispatched[len(s.dispatched)-1])

	// Another app isn't held up.
	other := s.request(shop, "v1")
	s.Require().NoError(deploy.RunQueued(ctx, shop.ID))
	s.Equal(models.DeploymentSucceeded, s.load(other.ID).Status)

	close(release)
	s.Require().NoError(<-done)

	for _, d := range []*models.Deployment{first, second, third} {
		s.Equal(models.DeploymentSucceeded, s.load(d.ID).Status)
	}
	var blogImages []string
	for _, spec := range s.docker.Deploys() {
		if spec.App == "blog" {
			blogImages = append(blogImages, spec.Image)
		}
	}
	s.Equal([]string{
		docker.FakeDigest("ghcr.io/owner/blog:v1"),
		docker.FakeDigest("ghcr.io/owner/blog:v2"),
		docker.FakeDigest("ghcr.io/owner/blog:v3"),
	}, blogImages, "in request order")
	s.EqualValues(1, maxInFlight.Load(), "never two blog deploys at once")
	s.Equal(third.ID, *s.loadApp(blog.ID).CurrentDeploymentID)
}

func (s *DeployTestSuite) TestRecoverFailsStuckDeploymentsAndRequeues() {
	ctx := context.Background()
	app := createTestApp(s.T(), "blog", nil)
	other := createTestApp(s.T(), "shop", nil)
	old := carbon.NewDateTime(carbon.Now().SubHour())
	insert := func(appID uint, status models.DeploymentStatus, startedAt *carbon.DateTime) uint {
		d := models.Deployment{AppID: appID, Status: status, Trigger: models.TriggerManual, Ref: "v1", StartedAt: startedAt}
		s.Require().NoError(facades.Orm().Query().Create(&d))
		_, err := facades.Orm().Query().Model(&d).Update("created_at", old)
		s.Require().NoError(err)
		return d.ID
	}
	pulling := insert(app.ID, models.DeploymentPulling, old)
	deploying := insert(app.ID, models.DeploymentDeploying, old)
	queued := insert(other.ID, models.DeploymentQueued, nil)
	justStarted := carbon.NewDateTime(carbon.Now().AddSecond())
	ours := insert(app.ID, models.DeploymentDeploying, justStarted)
	_, err := facades.DB().Table("jobs").Insert(map[string]any{
		"queue": "default", "payload": "{}", "attempts": 1, "reserved_at": old, "available_at": old, "created_at": old,
	})
	s.Require().NoError(err)

	s.Require().NoError(deploy.Recover(ctx))

	for _, id := range []uint{pulling, deploying} {
		d := s.load(id)
		s.Equal(models.DeploymentFailed, d.Status)
		s.Contains(*d.Error, "Echowa restarted while this deployment was")
		s.Contains(d.Log, "stopped part way")
		s.NotNil(d.FinishedAt)
	}
	s.Equal(models.DeploymentDeploying, s.load(ours).Status, "work this process started is left alone")
	s.Equal(models.DeploymentQueued, s.load(queued).Status)
	s.Equal([]dispatch{{other.ID, 0}}, s.dispatched, "queued deployments get a fresh job")
	count, err := facades.DB().Table("jobs").Count()
	s.Require().NoError(err)
	s.Zero(count, "reserved jobs from the old process are cleared")
}

func (s *DeployTestSuite) TestJobArgumentsAsTheQueueDeliversThem() {
	app := createTestApp(s.T(), "blog", nil)
	d := s.request(app, "v1")

	s.Require().NoError((&deploy.Job{}).Handle(float64(app.ID)), "JSON numbers decode as float64")
	s.Equal(models.DeploymentSucceeded, s.load(d.ID).Status)
	s.Error((&deploy.Job{}).Handle())
	s.Error((&deploy.Job{}).Handle("blog"))
}
