package feature

import (
	"context"
	"errors"
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

type PoolingTestSuite struct {
	suite.Suite
	tests.TestCase
	docker *docker.Fake
}

func TestPoolingTestSuite(t *testing.T) {
	suite.Run(t, new(PoolingTestSuite))
}

func (s *PoolingTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.docker = s.FakeDocker()
	original := deploy.Dispatch
	deploy.Dispatch = func(uint, time.Duration) error { return nil }
	s.T().Cleanup(func() {
		services.WaitForPoolers()
		deploy.Dispatch = original
	})
}

func (s *PoolingTestSuite) deployed(slug, name string) *models.App {
	tmpl, _ := templates.Find(slug)
	app, err := services.CreateApp(context.Background(), services.TemplateInput(tmpl, name), &tmpl)
	s.Require().NoError(err)
	s.redeploy(app)

	return s.reload(app.ID)
}

func (s *PoolingTestSuite) redeploy(app *models.App) {
	_, err := services.CreateDeployment(context.Background(), app, models.TriggerManual, app.DefaultTag)
	s.Require().NoError(err)
	s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
	services.WaitForPoolers()
}

func (s *PoolingTestSuite) reload(id uint) *models.App {
	var app models.App
	s.Require().NoError(facades.Orm().Query().Where("id", id).FirstOrFail(&app))

	return &app
}

func (s *PoolingTestSuite) pooled(slug, name string) *models.App {
	app := s.deployed(slug, name)
	s.Require().NoError(services.SetPooling(context.Background(), app, true))
	services.WaitForPoolers()

	return app
}

// poolerDeploys are the specs the pooler service called name was deployed
// with, oldest first.
func (s *PoolingTestSuite) poolerDeploys(name string) []docker.ServiceSpec {
	var specs []docker.ServiceSpec
	for _, spec := range s.docker.Deploys() {
		if spec.Name == name {
			specs = append(specs, spec)
		}
	}

	return specs
}

func (s *PoolingTestSuite) poolers() []docker.ServiceRef {
	refs, err := s.docker.ServicesWithRole(context.Background(), docker.RolePooler)
	s.Require().NoError(err)

	return refs
}

func (s *PoolingTestSuite) TestTheSwitchStartsThePoolerInTheBackgroundAndRemovesIt() {
	ctx := context.Background()
	app := s.deployed("postgres", "shop")
	env, err := services.EnvVars(ctx, app.ID)
	s.Require().NoError(err)
	pull := make(chan struct{})
	s.docker.Pull = func(ref string, _ *docker.RegistryAuth) (string, error) {
		<-pull
		return ref, nil
	}

	b := signedIn(s.T(), &s.TestCase)
	page := fmt.Sprintf("/apps/%d?tab=connection", app.ID)
	b.visit(page)
	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/pooling", app.ID), page, nil).AssertHeader("Location", page)
	s.True(s.reload(app.ID).Pooling)
	s.True(services.AppPoolerStatus(ctx, app).Syncing, "the pull is still going")
	close(pull)
	services.WaitForPoolers()

	postgres, _ := templates.Find("postgres")
	specs := s.poolerDeploys("echowa-shop-pooler")
	s.Require().Len(specs, 1)
	s.Equal(postgres.Pooler.Image, specs[0].Image)
	s.Contains(s.docker.Pulls(), postgres.Pooler.Image)
	s.Equal(docker.RolePooler, specs[0].Role)
	s.Equal(map[string]string{
		templates.PoolerServerEnv: "echowa-shop",
		"POSTGRES_USER":           env["POSTGRES_USER"],
		"POSTGRES_DB":             env["POSTGRES_DB"],
		"POSTGRES_PASSWORD":       env["POSTGRES_PASSWORD"],
	}, specs[0].Env)
	status := services.AppPoolerStatus(ctx, s.reload(app.ID))
	s.False(status.Syncing)
	s.Empty(status.Error)
	s.Require().NotNil(status.Service)
	s.Equal(services.StateRunning, status.Service.State)
	_, show := b.visit(page)
	s.Equal("Starting the connection pooler.", show.Flash["success"])

	b.submit(nethttp.MethodDelete, fmt.Sprintf("/apps/%d/pooling", app.ID), page, nil).AssertHeader("Location", page)
	services.WaitForPoolers()
	s.False(s.reload(app.ID).Pooling)
	s.Empty(s.poolers())
	s.Contains(s.docker.Removed(), "echowa-shop-pooler")
	s.NotContains(s.docker.Removed(), "echowa-shop", "the database keeps running")
}

func (s *PoolingTestSuite) TestThePoolerRestartsOnlyWhenItsSpecChanges() {
	ctx := context.Background()
	app := s.pooled("postgres", "shop")
	s.Require().Len(s.poolerDeploys("echowa-shop-pooler"), 1)
	pulls := len(s.docker.Pulls())

	s.Require().NoError(services.SyncPoolers(ctx))
	s.Len(s.poolerDeploys("echowa-shop-pooler"), 1, "a restart of Echowa leaves its clients connected")
	s.Len(s.docker.Pulls(), pulls, "and pulls nothing")

	s.redeploy(app)
	s.Len(s.poolerDeploys("echowa-shop-pooler"), 1, "a redeploy of the database alone doesn't restart it")

	env, err := services.EnvVars(ctx, app.ID)
	s.Require().NoError(err)
	env["POSTGRES_PASSWORD"] = "a-new-password"
	s.Require().NoError(services.SetEnvVars(ctx, app.ID, env))
	services.WaitForPoolers()
	s.Len(s.poolerDeploys("echowa-shop-pooler"), 1, "an env change waits for the database's deploy")
	s.redeploy(app)
	specs := s.poolerDeploys("echowa-shop-pooler")
	s.Require().Len(specs, 2)
	s.Equal("a-new-password", specs[1].Env["POSTGRES_PASSWORD"])
}

func (s *PoolingTestSuite) TestOnlyPostgresAppsHaveTheSwitch() {
	mysql := s.deployed("mysql", "cms")
	custom := createTestApp(s.T(), "blog", nil)
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/dashboard")

	for _, app := range []*models.App{mysql, custom} {
		b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/pooling", app.ID), "/dashboard", nil).AssertNotFound()
		s.False(s.reload(app.ID).Pooling)
	}
	s.Empty(s.poolers())
}

func (s *PoolingTestSuite) TestPoolingNeverTakesAnotherAppsService() {
	ctx := context.Background()
	shop := s.deployed("postgres", "shop")
	other := s.deployed("redis", "shop-pooler")
	otherSpec := s.poolerDeploys("echowa-shop-pooler")
	s.Require().Len(otherSpec, 1, "the redis app's own service")

	b := signedIn(s.T(), &s.TestCase)
	page := fmt.Sprintf("/apps/%d?tab=connection", shop.ID)
	b.visit(page)
	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/pooling", shop.ID), page, nil)
	_, show := b.visit(page)
	s.Equal("The app shop-pooler has the service name the pooler needs, echowa-shop-pooler. Delete that app to turn pooling on.", show.Flash["error"])
	s.False(s.reload(shop.ID).Pooling)

	// The switch was turned on by a release without the check.
	_, err := facades.Orm().Query().Model(shop).Update("pooling", true)
	s.Require().NoError(err)
	s.ErrorContains(services.SyncPooler(ctx, shop), "the app shop-pooler has the service name the pooler needs")
	s.Equal("the app shop-pooler has the service name the pooler needs, echowa-shop-pooler", services.AppPoolerStatus(ctx, shop).Error)

	s.Require().NoError(services.SetPooling(ctx, shop, false))
	services.WaitForPoolers()
	b.submit(nethttp.MethodDelete, fmt.Sprintf("/apps/%d", shop.ID), page, nil)
	services.WaitForPoolers()

	s.NotContains(s.docker.Removed(), "echowa-shop-pooler", "turning pooling off and deleting the app leave it alone")
	s.Equal(otherSpec, s.poolerDeploys("echowa-shop-pooler"))
	_, err = s.docker.ServiceStatus(ctx, other.ServiceName())
	s.NoError(err, "the redis app still runs")
}

func (s *PoolingTestSuite) TestANewAppCantTakeAPoolersName() {
	ctx := context.Background()
	shop := s.pooled("postgres", "shop")
	blog := createTestApp(s.T(), "blog", nil)
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/apps/create")

	form := newAppForm()
	form["name"] = "shop-pooler"
	form["domain"] = nil
	b.submit(nethttp.MethodPost, "/apps", "/apps/create", form).AssertHeader("Location", "/apps/create")
	_, page := b.visit("/apps/create")
	s.Contains(page.Props["errors"].(map[string]any)["name"], "Another app's connection pooler already uses this name.")

	tab := fmt.Sprintf("/apps/%d?tab=databases", blog.ID)
	b.visit(tab)
	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/databases", blog.ID), tab, map[string]any{
		"databases": []map[string]any{{"template": "postgres", "name": "shop-pooler"}},
	}).AssertHeader("Location", tab)
	_, page = b.visit(tab)
	s.Contains(page.Props["errors"].(map[string]any)["databases.0.name"], "Another app's connection pooler already uses this name.")

	s.Require().NoError(services.SetPooling(ctx, shop, false))
	services.WaitForPoolers()
	b.visit("/apps/create")
	b.submit(nethttp.MethodPost, "/apps", "/apps/create", form)
	var created models.App
	s.Require().NoError(facades.Orm().Query().Where("name", "shop-pooler").First(&created))
	s.NotZero(created.ID, "with pooling off, the name is free")
}

func (s *PoolingTestSuite) TestDeletingAnAppRemovesItsPoolerFirst() {
	shop := s.pooled("postgres", "shop")
	b := signedIn(s.T(), &s.TestCase)
	page := fmt.Sprintf("/apps/%d", shop.ID)
	b.visit(page)

	s.docker.Remove = func(name string) error {
		if name == "echowa-shop-pooler" {
			return errors.New("daemon busy")
		}
		return nil
	}
	b.submit(nethttp.MethodDelete, page, page, nil)
	_, show := b.visit(page)
	s.Equal("Couldn't remove the connection pooler echowa-shop-pooler, so the app was kept: daemon busy", show.Flash["error"])
	s.NotContains(s.docker.Removed(), "echowa-shop", "the database still runs")

	s.docker.Remove = nil
	b.submit(nethttp.MethodDelete, page, page, nil)
	services.WaitForPoolers()
	s.Equal([]string{"echowa-shop-pooler", "echowa-shop"}, s.docker.Removed())
	s.Empty(s.poolers())
	exists, err := facades.Orm().Query().Model(&models.App{}).Where("id", shop.ID).Exists()
	s.Require().NoError(err)
	s.False(exists)
}

func (s *PoolingTestSuite) TestAFailedSyncSaysWhyUntilOneSucceeds() {
	ctx := context.Background()
	app := s.pooled("postgres", "shop")
	s.docker.Wait = func(spec docker.ServiceSpec) (docker.UpdateResult, error) {
		if spec.Role == docker.RolePooler {
			return docker.UpdateResult{Outcome: docker.OutcomeRolledBack, Message: "task: non-zero exit (1)."}, nil
		}
		return docker.UpdateResult{Outcome: docker.OutcomeCompleted}, nil
	}
	env, err := services.EnvVars(ctx, app.ID)
	s.Require().NoError(err)
	env["POSTGRES_USER"] = "renamed"
	s.Require().NoError(services.SetEnvVars(ctx, app.ID, env))
	s.redeploy(app)

	status := services.AppPoolerStatus(ctx, app)
	s.False(status.Syncing)
	s.Equal("Swarm rolled the pooler back: task: non-zero exit (1)", status.Error)
	s.Equal(services.StateRunning, status.Service.State, "the old pooler still runs")

	s.docker.Wait = nil
	s.Require().NoError(services.SyncPoolers(ctx), "the rolled back spec doesn't match, so boot tries again")
	specs := s.poolerDeploys("echowa-shop-pooler")
	s.Require().Len(specs, 3)
	s.Equal("renamed", specs[2].Env["POSTGRES_USER"])
	s.Empty(services.AppPoolerStatus(ctx, app).Error)
}

func (s *PoolingTestSuite) TestBootRestoresPoolersAndRemovesOrphans() {
	ctx := context.Background()
	s.pooled("postgres", "shop")
	gone := s.pooled("postgres", "gone")
	// Deleted by a release that didn't know about poolers.
	_, err := facades.Orm().Query().Delete(gone)
	s.Require().NoError(err)
	// Removed by hand.
	s.Require().NoError(s.docker.RemoveService(ctx, "echowa-shop-pooler"))
	s.Equal([]docker.ServiceRef{{Name: "echowa-gone-pooler", App: "gone"}}, s.poolers())

	s.Require().NoError(services.SyncPoolers(ctx))

	s.Equal([]docker.ServiceRef{{Name: "echowa-shop-pooler", App: "shop"}}, s.poolers())
	s.Len(s.poolerDeploys("echowa-shop-pooler"), 2)
}

func (s *PoolingTestSuite) TestTheConnectionTabShowsThePoolerAndRevealsItsURL() {
	ctx := context.Background()
	app := s.deployed("postgres", "shop")
	env, err := services.EnvVars(ctx, app.ID)
	s.Require().NoError(err)
	user, password, db := env["POSTGRES_USER"], env["POSTGRES_PASSWORD"], env["POSTGRES_DB"]
	b := signedIn(s.T(), &s.TestCase)
	b.forbid(password)
	page := fmt.Sprintf("/apps/%d?tab=connection", app.ID)
	reveal := func() map[string]any {
		resp := b.request(nethttp.MethodPost, fmt.Sprintf("/apps/%d/connect/reveal", app.ID), map[string]any{}, false, map[string]string{
			"X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie),
			"Accept":       "application/json",
		})
		resp.AssertOk()
		body, err := resp.Json()
		s.Require().NoError(err)
		return body
	}

	_, show := b.visit(page)
	pooler := show.Props["pooler"].(map[string]any)
	s.Equal(map[string]any{
		"host": "echowa-shop-pooler", "port": float64(6432), "user": user,
		"preview": "postgres://POSTGRES_USER:POSTGRES_PASSWORD@echowa-shop-pooler:6432/POSTGRES_DB",
	}, pooler["connect"])
	s.Equal(map[string]any{"syncing": false, "error": "", "service": nil}, pooler["status"])
	s.Nil(reveal()["pooled_url"], "no pooled URL while pooling is off")

	s.Require().NoError(services.SetPooling(ctx, app, true))
	services.WaitForPoolers()
	_, show = b.visit(page)
	status := show.Props["pooler"].(map[string]any)["status"].(map[string]any)
	s.Equal(services.StateRunning, status["service"].(map[string]any)["state"])
	s.Equal(true, show.Props["app"].(map[string]any)["pooling"])
	secrets := reveal()
	s.Equal(fmt.Sprintf("postgres://%s:%s@echowa-shop-pooler:6432/%s", user, password, db), secrets["pooled_url"])
	s.Equal(fmt.Sprintf("postgres://%s:%s@echowa-shop:5432/%s", user, password, db), secrets["url"])

	mysql := s.deployed("mysql", "cms")
	_, show = b.visit(fmt.Sprintf("/apps/%d?tab=connection", mysql.ID))
	s.Nil(show.Props["pooler"])
}
