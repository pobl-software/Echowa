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
	"goravel/database/migrations"
	"goravel/tests"
)

type AppTemplatesTestSuite struct {
	suite.Suite
	tests.TestCase
	docker     *docker.Fake
	dispatched []uint
}

func TestAppTemplatesTestSuite(t *testing.T) {
	suite.Run(t, new(AppTemplatesTestSuite))
}

func (s *AppTemplatesTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.Require().True(facades.Cache().Flush())
	s.docker = s.FakeDocker()
	s.dispatched = nil
	original := deploy.Dispatch
	deploy.Dispatch = func(appID uint, _ time.Duration) error {
		s.dispatched = append(s.dispatched, appID)
		return nil
	}
	s.T().Cleanup(func() { deploy.Dispatch = original })
}

// templateForm is the New app form as the page fills it for slug.
func templateForm(slug, name string) map[string]any {
	tmpl, _ := templates.Find(slug)

	return map[string]any{
		"template": slug, "name": name, "repository": tmpl.Repository, "default_tag": tmpl.DefaultTag,
		"registry_id": nil, "domain": nil, "port": tmpl.Port, "replicas": 1,
		"healthcheck_cmd": tmpl.Healthcheck, "tls": false,
	}
}

func (s *AppTemplatesTestSuite) loadApp(name string) models.App {
	var app models.App
	s.Require().NoError(facades.Orm().Query().With("Volumes").Where("name", name).FirstOrFail(&app))

	return app
}

func (s *AppTemplatesTestSuite) TestCreateFormOffersTemplatesWithoutValues() {
	b := signedIn(s.T(), &s.TestCase)

	_, page := b.visit("/apps/create")

	list := page.Props["templates"].([]any)
	s.Require().Len(list, 3)
	postgres := list[0].(map[string]any)
	s.Equal("postgres", postgres["slug"])
	s.Equal("PostgreSQL", postgres["name"])
	s.Equal("18", postgres["default_tag"])
	s.Equal(float64(5432), postgres["port"])
	s.Equal(false, postgres["http"])
	s.Equal([]any{"/var/lib/postgresql"}, postgres["volumes"])
	s.Equal([]any{
		map[string]any{"key": "POSTGRES_USER", "source": "app_name"},
		map[string]any{"key": "POSTGRES_DB", "source": "app_name"},
		map[string]any{"key": "POSTGRES_PASSWORD", "source": "generated"},
	}, postgres["env"])
	mysql := list[1].(map[string]any)
	s.Equal("mysql", mysql["slug"])
	s.Equal([]any{
		map[string]any{"key": "MYSQL_USER", "source": "fixed"},
		map[string]any{"key": "MYSQL_DATABASE", "source": "app_name"},
		map[string]any{"key": "MYSQL_PASSWORD", "source": "generated"},
		map[string]any{"key": "MYSQL_ROOT_PASSWORD", "source": "generated"},
	}, mysql["env"], "a fixed value is a source too, never the value")
	s.Equal("redis", list[2].(map[string]any)["slug"])
}

func (s *AppTemplatesTestSuite) TestStoreFromATemplateSeedsTheAppAndDeploysIt() {
	for _, tt := range []struct {
		slug, password, target string
		args                   bool
	}{
		{"postgres", "POSTGRES_PASSWORD", "/var/lib/postgresql", false},
		{"mysql", "MYSQL_PASSWORD", "/var/lib/mysql", false},
		{"redis", "REDIS_PASSWORD", "/data", true},
	} {
		s.Run(tt.slug, func() {
			name := tt.slug + "-db"
			b := signedIn(s.T(), &s.TestCase)
			b.visit("/apps/create?template=" + tt.slug)

			resp := b.submit(nethttp.MethodPost, "/apps", "/apps/create?template="+tt.slug, templateForm(tt.slug, name))

			app := s.loadApp(name)
			var deployment models.Deployment
			s.Require().NoError(facades.Orm().Query().Where("app_id", app.ID).FirstOrFail(&deployment))
			resp.AssertHeader("Location", fmt.Sprintf("/apps/%d/deployments/%d", app.ID, deployment.ID))
			tmpl, _ := templates.Find(tt.slug)
			s.Equal(tmpl.DefaultTag, deployment.Ref, "the first deploy is of the template's tag")
			s.Equal(models.DeploymentQueued, deployment.Status)
			s.Contains(s.dispatched, app.ID)

			s.Equal(tt.slug, *app.Template)
			s.Nil(app.Domain)
			s.Equal(tmpl.Healthcheck, *app.HealthcheckCmd)
			s.Equal(tt.args, app.Args != nil)
			s.Require().Len(app.Volumes, 1)
			s.Equal("echowa-"+name+"-data", app.Volumes[0].Name)
			s.Equal(tt.target, app.Volumes[0].Target)

			env, err := services.EnvVars(context.Background(), app.ID)
			s.Require().NoError(err)
			password := env[tt.password]
			s.Regexp(`^[0-9a-f]{64}$`, password)
			var raw string
			s.Require().NoError(facades.DB().Table("app_env_vars").Where("app_id", app.ID).Where("key", tt.password).Value("value", &raw))
			s.NotContains(raw, password, "stored encrypted")

			// The password never reaches a page, including the redirect's.
			b.forbid(password)
			b.visit(fmt.Sprintf("/apps/%d/deployments/%d", app.ID, deployment.ID))
			b.visit(fmt.Sprintf("/apps/%d", app.ID))
			b.visit(fmt.Sprintf("/apps/%d?tab=environment", app.ID))

			s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
			deploys := s.docker.Deploys()
			spec := deploys[len(deploys)-1]
			s.Equal("echowa-"+name, spec.Name)
			s.Equal(password, spec.Env[tt.password])
			s.Equal([]docker.Volume{{Name: "echowa-" + name + "-data", Target: tt.target}}, spec.Volumes)
			s.Equal(app.Args, spec.Args)
			s.Nil(spec.Route, "a database isn't routed")

			// The row's copy is only for an older release after a rollback.
			_, err = facades.Orm().Query().Model(&app).Update("args", `["stale"]`)
			s.Require().NoError(err)
			_, err = deploy.Request(context.Background(), &app, models.TriggerManual, tmpl.DefaultTag)
			s.Require().NoError(err)
			s.Require().NoError(deploy.RunQueued(context.Background(), app.ID))
			deploys = s.docker.Deploys()
			s.Equal(tmpl.Args(templates.Options{}), deploys[len(deploys)-1].Args, "the deploy builds a template app's args")
		})
	}
}

func (s *AppTemplatesTestSuite) TestEachAppGetsItsOwnPassword() {
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/apps/create?template=postgres")
	b.submit(nethttp.MethodPost, "/apps", "/apps/create?template=postgres", templateForm("postgres", "one"))
	b.submit(nethttp.MethodPost, "/apps", "/apps/create?template=postgres", templateForm("postgres", "two"))

	one, err := services.EnvVars(context.Background(), s.loadApp("one").ID)
	s.Require().NoError(err)
	two, err := services.EnvVars(context.Background(), s.loadApp("two").ID)
	s.Require().NoError(err)
	s.NotEqual(one["POSTGRES_PASSWORD"], two["POSTGRES_PASSWORD"])
	s.Equal("two", two["POSTGRES_USER"])
}

func (s *AppTemplatesTestSuite) TestStoreRefusesAnUnknownTemplate() {
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/apps/create")

	form := templateForm("postgres", "db")
	form["template"] = "mongodb"
	b.submit(nethttp.MethodPost, "/apps", "/apps/create", form).AssertHeader("Location", "/apps/create")

	_, page := b.visit("/apps/create")
	s.Equal("There's no template called mongodb.", page.Props["errors"].(map[string]any)["template"])
	s.assertNoApps()
}

func (s *AppTemplatesTestSuite) TestStoreRefusesADomainOrReplicasForADatabase() {
	b := signedIn(s.T(), &s.TestCase)
	referer := "/apps/create?template=postgres"
	b.visit(referer)

	form := templateForm("postgres", "db")
	form["domain"] = "db.example.com"
	b.submit(nethttp.MethodPost, "/apps", referer, form).AssertHeader("Location", referer)
	_, page := b.visit(referer)
	s.Equal("PostgreSQL doesn't speak HTTP, so it has no domain. Other apps reach it at echowa-db:5432.",
		page.Props["errors"].(map[string]any)["domain"])

	form["domain"] = nil
	form["replicas"] = 2
	b.submit(nethttp.MethodPost, "/apps", referer, form)
	_, page = b.visit(referer)
	s.Contains(page.Props["errors"], "replicas")

	s.assertNoApps()
	s.Empty(s.dispatched)
}

func (s *AppTemplatesTestSuite) TestUpdateRefusesADomainForADatabase() {
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/apps/create?template=redis")
	b.submit(nethttp.MethodPost, "/apps", "/apps/create?template=redis", templateForm("redis", "cache"))
	app := s.loadApp("cache")
	showURL := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(showURL)

	form := map[string]any{"domain": "cache.example.com", "tls": false}
	b.submit(nethttp.MethodPut, showURL+"/domain", showURL, form).AssertHeader("Location", showURL)
	_, page := b.visit(showURL)
	s.Contains(page.Props["errors"].(map[string]any)["domain"], "Other apps reach it at echowa-cache:6379.")
	s.Nil(s.loadApp("cache").Domain)
}

func (s *AppTemplatesTestSuite) TestShowSendsTheConnectionShapeButNoPassword() {
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/apps/create?template=postgres")
	b.submit(nethttp.MethodPost, "/apps", "/apps/create?template=postgres", templateForm("postgres", "db"))
	app := s.loadApp("db")
	env, err := services.EnvVars(context.Background(), app.ID)
	s.Require().NoError(err)
	password := env["POSTGRES_PASSWORD"]
	b.forbid(password)

	_, page := b.visit(fmt.Sprintf("/apps/%d", app.ID))

	s.Equal(map[string]any{
		"host": "echowa-db", "port": float64(5432), "user": "db",
		"preview": "postgres://POSTGRES_USER:POSTGRES_PASSWORD@echowa-db:5432/POSTGRES_DB",
	}, page.Props["connect"])
	tmpl := page.Props["template"].(map[string]any)
	s.Equal("postgres", tmpl["slug"])
	s.Equal(false, tmpl["http"])
	volumes := page.Props["app"].(map[string]any)["volumes"].([]any)
	s.Require().Len(volumes, 1)
	s.Equal("/var/lib/postgresql", volumes[0].(map[string]any)["target"])

	// The panel's Reveal and Copy: plain JSON, never a page.
	revealURL := fmt.Sprintf("/apps/%d/connect/reveal", app.ID)
	resp := b.request(nethttp.MethodPost, revealURL, map[string]any{}, false, map[string]string{
		"X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie),
		"Accept":       "application/json",
	})
	resp.AssertOk()
	resp.AssertHeader("Cache-Control", "no-store")
	resp.AssertJson(map[string]any{"url": "postgres://db:" + password + "@echowa-db:5432/db", "password": password})

	b.request(nethttp.MethodPost, revealURL, map[string]any{}, false, map[string]string{"Accept": "application/json"}).
		AssertStatus(419)
}

func (s *AppTemplatesTestSuite) TestACustomAppHasNoConnectPanel() {
	app := createTestApp(s.T(), "blog", nil)
	b := signedIn(s.T(), &s.TestCase)

	_, page := b.visit(fmt.Sprintf("/apps/%d", app.ID))

	s.Nil(page.Props["connect"])
	s.Nil(page.Props["template"])
	s.NotContains(page.Props["app"], "volumes", "an app without volumes leaves the key out")
	b.request(nethttp.MethodPost, fmt.Sprintf("/apps/%d/connect/reveal", app.ID), map[string]any{}, false, map[string]string{
		"X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie),
	}).AssertNotFound()
}

// createDeployedDB makes a PostgreSQL app called db through the form, and
// runs its service in the fake with the volume mounted.
func (s *AppTemplatesTestSuite) createDeployedDB(b *browser) models.App {
	b.visit("/apps/create")
	b.submit(nethttp.MethodPost, "/apps", "/apps/create", templateForm("postgres", "db"))
	app := s.loadApp("db")
	s.Require().NoError(s.docker.DeployService(context.Background(), docker.ServiceSpec{
		Name: app.ServiceName(), App: app.Name, Image: "postgres@sha256:a", Replicas: 1,
		Volumes: []docker.Volume{{Name: "echowa-db-data", Target: "/var/lib/postgresql"}},
	}))

	return app
}

func (s *AppTemplatesTestSuite) TestDestroyRemovesTheVolumeOnceTheServiceIsGone() {
	b := signedIn(s.T(), &s.TestCase)
	app := s.createDeployedDB(b)
	var order []string
	s.docker.Remove = func(name string) error { order = append(order, name); return nil }
	inUse := 1
	s.docker.VolumeRemove = func(name string) error {
		order = append(order, name)
		if inUse > 0 {
			inUse--
			return fmt.Errorf("volume %s: %w", name, docker.ErrVolumeInUse)
		}
		return nil
	}
	showURL := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(showURL)

	b.submit(nethttp.MethodDelete, showURL, showURL, nil).AssertHeader("Location", "/dashboard")
	_, page := b.visit("/dashboard")
	s.Contains(page.Flash["success"], "Its data (echowa-db-data) is deleted once its task has stopped.")
	s.assertNoApps()
	s.Eventually(func() bool { return len(s.docker.RemovedVolumes()) == 1 }, 5*time.Second, 50*time.Millisecond)
	s.Equal([]string{"echowa-db-data"}, s.docker.RemovedVolumes())
	s.Equal([]string{"echowa-db", "echowa-db-data", "echowa-db-data"}, order, "the service first, then the volume until Docker lets go of it")
}

func (s *AppTemplatesTestSuite) TestDestroyKeepsTheVolumeWhenTheServiceCantBeRemoved() {
	b := signedIn(s.T(), &s.TestCase)
	app := s.createDeployedDB(b)
	s.docker.Remove = func(name string) error { return fmt.Errorf("%s: %w", name, docker.ErrNotManaged) }
	showURL := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(showURL)

	b.submit(nethttp.MethodDelete, showURL, showURL, nil).AssertHeader("Location", showURL)
	s.Len(s.loadApp("db").Volumes, 1)
	exists, err := s.docker.VolumeExists(context.Background(), "echowa-db-data")
	s.Require().NoError(err)
	s.True(exists)
	s.Empty(s.docker.RemovedVolumes())
}

func (s *AppTemplatesTestSuite) TestStoreRefusesALeftoverVolume() {
	s.docker.AddVolume("echowa-db-data")
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/apps/create?template=postgres")

	b.submit(nethttp.MethodPost, "/apps", "/apps/create?template=postgres", templateForm("postgres", "db")).
		AssertHeader("Location", "/apps/create?template=postgres")

	_, page := b.visit("/apps/create?template=postgres")
	s.Contains(page.Props["errors"].(map[string]any)["name"], "The volume echowa-db-data is left from an earlier app called db")
	s.assertNoApps()
	s.Empty(s.dispatched)

	// Another name gets its own volume.
	b.submit(nethttp.MethodPost, "/apps", "/apps/create?template=postgres", templateForm("postgres", "db2"))
	s.Equal("echowa-db2-data", s.loadApp("db2").Volumes[0].Name)
}

func (s *AppTemplatesTestSuite) assertNoApps() {
	n, err := facades.Orm().Query().Model(&models.App{}).Count()
	s.Require().NoError(err)
	s.Zero(n)
}

func (s *AppTemplatesTestSuite) TestRedisAppsMoveOffThePasswordArgument() {
	old := `redis-cli -a "$REDIS_PASSWORD" --no-auth-warning ping | grep -q PONG`
	custom := `redis-cli -a "$REDIS_PASSWORD" ping`
	for name, healthcheck := range map[string]string{"cache": old, "queue": custom} {
		_, err := services.CreateApp(context.Background(), services.AppInput{
			Name: name, Repository: "redis", DefaultTag: "8", Port: 6379, Replicas: 1, HealthcheckCmd: &healthcheck,
		}, nil)
		s.Require().NoError(err)
	}
	_, err := facades.Orm().Query().Table("apps").Where("id > ?", 0).Update("template", "redis")
	s.Require().NoError(err)

	s.Require().NoError((&migrations.M20261027180000UpdateRedisHealthcheck{}).Up())

	redis, _ := templates.Find("redis")
	s.Equal(redis.Healthcheck, *s.loadApp("cache").HealthcheckCmd)
	s.NotContains(redis.Healthcheck, "-a ")
	s.Equal(custom, *s.loadApp("queue").HealthcheckCmd, "a healthcheck the admin wrote is theirs")
}
