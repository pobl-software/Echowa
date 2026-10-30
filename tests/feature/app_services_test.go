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
	"goravel/app/templates"
	"goravel/tests"
)

type AppServicesTestSuite struct {
	suite.Suite
	tests.TestCase
	dispatched []uint
}

func TestAppServicesTestSuite(t *testing.T) {
	suite.Run(t, new(AppServicesTestSuite))
}

func (s *AppServicesTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.Require().True(facades.Cache().Flush())
	s.FakeDocker()
	s.dispatched = nil
	original := deploy.Dispatch
	deploy.Dispatch = func(appID uint, _ time.Duration) error {
		s.dispatched = append(s.dispatched, appID)
		return nil
	}
	s.T().Cleanup(func() { deploy.Dispatch = original })
}

func (s *AppServicesTestSuite) appNamed(name string) models.App {
	var app models.App
	s.Require().NoError(facades.Orm().Query().Where("name", name).FirstOrFail(&app))

	return app
}

func (s *AppServicesTestSuite) serviceNames(b *browser, app *models.App) []string {
	_, page := b.visit(fmt.Sprintf("/apps/%d?tab=services", app.ID))
	names := []string{}
	for _, row := range page.Props["linkedServices"].([]any) {
		names = append(names, row.(map[string]any)["name"].(string))
	}

	return names
}

func (s *AppServicesTestSuite) countLinks() int64 {
	n, err := facades.Orm().Query().Model(&models.AppLink{}).Count()
	s.Require().NoError(err)

	return n
}

func (s *AppServicesTestSuite) TestAddCreatesAWorkerOnTheAppsImageWithItsEnvironment() {
	ctx := context.Background()
	web := createTestApp(s.T(), "pobl", nil)
	s.Require().NoError(services.SetEnvVars(ctx, web.ID, map[string]string{"APP_KEY": "base64:secret", "DB_HOST": "echowa-pobl-postgres"}))
	tab := fmt.Sprintf("/apps/%d?tab=services", web.ID)
	b := signedIn(s.T(), &s.TestCase)
	b.visit(tab)

	resp := b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/services", web.ID), tab, map[string]any{
		"name": " Pobl-Horizon ", "repository": "ghcr.io/owner/pobl", "default_tag": "latest", "registry_id": nil,
		"port": 8080, "replicas": 1, "healthcheck_cmd": nil, "command": "php artisan horizon", "copy_env": true,
	})

	resp.AssertHeader("Location", tab)
	worker := s.appNamed("pobl-horizon")
	s.Nil(worker.Template)
	s.Nil(worker.Domain)
	s.Equal([]string{"php", "artisan", "horizon"}, worker.Args)
	env, err := services.EnvVars(ctx, worker.ID)
	s.Require().NoError(err)
	s.Equal(map[string]string{"APP_KEY": "base64:secret", "DB_HOST": "echowa-pobl-postgres"}, env)
	s.Equal([]uint{worker.ID}, s.dispatched, "a new service deploys at once")
	spec := deploy.ServiceSpec(&worker, "ghcr.io/owner/pobl@sha256:abc", env, services.ResourceLimits{})
	s.Equal([]string{"php", "artisan", "horizon"}, spec.Args, "the command replaces the image's CMD")

	_, page := b.visit(tab)
	s.Equal("Created pobl-horizon, deploying now.", page.Flash["success"])
	rows := page.Props["linkedServices"].([]any)
	s.Require().Len(rows, 1)
	row := rows[0].(map[string]any)
	s.Equal("pobl-horizon", row["name"])
	s.Equal("ghcr.io/owner/pobl:latest", row["image"])
	s.Equal("echowa-pobl-horizon", row["host"])
	s.Equal(float64(8080), row["port"])
	s.Empty(page.Props["linkedDatabases"], "a service isn't a database")

	_, shown := b.visit(fmt.Sprintf("/apps/%d", worker.ID))
	partOf := shown.Props["serviceChoices"].(map[string]any)["part_of"].([]any)
	s.Equal([]any{map[string]any{"id": float64(web.ID), "name": "pobl"}}, partOf)
}

func (s *AppServicesTestSuite) TestAddWithoutCopyingStartsWithNoEnvironment() {
	web := createTestApp(s.T(), "pobl", nil)
	s.Require().NoError(services.SetEnvVars(context.Background(), web.ID, map[string]string{"APP_KEY": "secret"}))
	tab := fmt.Sprintf("/apps/%d?tab=services", web.ID)
	b := signedIn(s.T(), &s.TestCase)
	b.visit(tab)

	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/services", web.ID), tab, map[string]any{
		"name": "pobl-gotenberg", "repository": "gotenberg/gotenberg", "default_tag": "8",
		"port": 3000, "replicas": 1, "command": "", "copy_env": false,
	}).AssertHeader("Location", tab)

	gotenberg := s.appNamed("pobl-gotenberg")
	s.Nil(gotenberg.Args, "an empty command keeps the image's CMD")
	env, err := services.EnvVars(context.Background(), gotenberg.ID)
	s.Require().NoError(err)
	s.Empty(env)
}

func (s *AppServicesTestSuite) TestAddRefusesABadServiceAndMakesNothing() {
	web := createTestApp(s.T(), "pobl", nil)
	createTestApp(s.T(), "blog", nil)
	tab := fmt.Sprintf("/apps/%d?tab=services", web.ID)
	valid := func(change map[string]any) map[string]any {
		form := map[string]any{"name": "worker", "repository": "ghcr.io/owner/pobl", "default_tag": "latest", "port": 8080, "replicas": 1}
		for k, v := range change {
			form[k] = v
		}
		return form
	}

	for _, tt := range []struct {
		name  string
		form  map[string]any
		field string
		want  string
	}{
		{"taken name", valid(map[string]any{"name": "blog"}), "name", "already been taken"},
		{"no image", valid(map[string]any{"repository": ""}), "repository", "required"},
		{"open quote", valid(map[string]any{"command": "sh -c 'echo"}), "command", "quote that isn't closed"},
	} {
		s.Run(tt.name, func() {
			b := signedIn(s.T(), &s.TestCase)
			b.visit(tab)

			b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/services", web.ID), tab, tt.form).AssertHeader("Location", tab)

			_, page := b.visit(tab)
			s.Contains(page.Props["errors"].(map[string]any)[tt.field], tt.want)
			n, err := facades.Orm().Query().Model(&models.App{}).Count()
			s.Require().NoError(err)
			s.EqualValues(2, n)
			s.Empty(s.dispatched)
		})
	}
}

func (s *AppServicesTestSuite) TestLinkOffersAndLinksCustomAppsOnly() {
	ctx := context.Background()
	web := createTestApp(s.T(), "pobl", nil)
	worker := createTestApp(s.T(), "worker", nil)
	other := createTestApp(s.T(), "other", nil)
	helper := createTestApp(s.T(), "helper", nil)
	s.Require().NoError(services.LinkApps(ctx, other.ID, helper.ID))
	tmpl, _ := templates.Find("redis")
	cache, err := services.CreateApp(ctx, services.TemplateInput(tmpl, "cache"), &tmpl)
	s.Require().NoError(err)
	tab := fmt.Sprintf("/apps/%d?tab=services", web.ID)
	b := signedIn(s.T(), &s.TestCase)

	_, page := b.visit(tab)
	existing := page.Props["serviceChoices"].(map[string]any)["existing"].([]any)
	s.Equal([]any{
		map[string]any{"id": float64(helper.ID), "name": "helper", "repository": "ghcr.io/owner/helper"},
		map[string]any{"id": float64(worker.ID), "name": "worker", "repository": "ghcr.io/owner/worker"},
	}, existing, "not itself, not a database, not an app with services of its own")

	for _, tt := range []struct {
		name string
		link []uint
		want string
	}{
		{"nothing picked", []uint{}, "Pick a service to link."},
		{"a database", []uint{cache.ID}, "Only apps of your own image can be linked as services."},
		{"itself", []uint{web.ID}, "An app can't be its own service."},
		{"an app with services", []uint{other.ID}, "other has services of its own, so it can't be one."},
	} {
		s.Run(tt.name, func() {
			b.visit(tab)
			b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/services/link", web.ID), tab, map[string]any{"link": tt.link}).AssertHeader("Location", tab)
			_, page := b.visit(tab)
			s.Contains(page.Props["errors"].(map[string]any)["link"], tt.want)
			s.EqualValues(1, s.countLinks())
		})
	}

	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/services/link", web.ID), tab, map[string]any{"link": []uint{worker.ID, helper.ID}}).AssertHeader("Location", tab)
	s.Equal([]string{"helper", "worker"}, s.serviceNames(b, web))
	s.Empty(s.dispatched, "linking deploys nothing")
}

func (s *AppServicesTestSuite) TestAServiceHasNoServicesOfItsOwn() {
	ctx := context.Background()
	web := createTestApp(s.T(), "pobl", nil)
	worker := createTestApp(s.T(), "worker", nil)
	other := createTestApp(s.T(), "other", nil)
	s.Require().NoError(services.LinkApps(ctx, web.ID, worker.ID))
	tab := fmt.Sprintf("/apps/%d?tab=services", worker.ID)
	b := signedIn(s.T(), &s.TestCase)

	_, page := b.visit(tab)
	s.Empty(page.Props["serviceChoices"].(map[string]any)["existing"])

	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/services/link", worker.ID), tab, map[string]any{"link": []uint{other.ID}}).AssertHeader("Location", tab)
	_, page = b.visit(tab)
	s.Equal("worker is a service of pobl, so it can't have services of its own.", page.Props["errors"].(map[string]any)["link"])

	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/services", worker.ID), tab, map[string]any{
		"name": "nested", "repository": "ghcr.io/owner/x", "default_tag": "latest", "port": 80, "replicas": 1,
	}).AssertHeader("Location", tab)
	_, page = b.visit(tab)
	s.Contains(page.Props["errors"].(map[string]any)["name"], "worker is a service of pobl")
	s.EqualValues(1, s.countLinks())
}

func (s *AppServicesTestSuite) TestADatabaseHasNoServicesSection() {
	tmpl, _ := templates.Find("postgres")
	db, err := services.CreateApp(context.Background(), services.TemplateInput(tmpl, "db"), &tmpl)
	s.Require().NoError(err)
	worker := createTestApp(s.T(), "worker", nil)
	page := fmt.Sprintf("/apps/%d", db.ID)
	b := signedIn(s.T(), &s.TestCase)

	_, show := b.visit(page)
	s.Empty(show.Props["linkedServices"])
	s.Nil(show.Props["serviceChoices"])

	b.submit(nethttp.MethodPost, fmt.Sprintf("/apps/%d/services/link", db.ID), page, map[string]any{"link": []uint{worker.ID}}).AssertNotFound()
	s.Zero(s.countLinks())
}

func (s *AppServicesTestSuite) TestUnlinkKeepsTheService() {
	web := createTestApp(s.T(), "pobl", nil)
	worker := createTestApp(s.T(), "worker", nil)
	s.Require().NoError(services.LinkApps(context.Background(), web.ID, worker.ID))
	tab := fmt.Sprintf("/apps/%d?tab=services", web.ID)
	b := signedIn(s.T(), &s.TestCase)
	b.visit(tab)

	b.submit(nethttp.MethodDelete, fmt.Sprintf("/apps/%d/services/%d", web.ID, worker.ID), tab, nil).AssertHeader("Location", tab)

	s.Empty(s.serviceNames(b, web))
	s.appNamed("worker")
}

func (s *AppServicesTestSuite) TestDockerConfigSetsACustomAppsCommandOnly() {
	ctx := context.Background()
	app := createTestApp(s.T(), "worker", nil)
	tmpl, _ := templates.Find("redis")
	cache, err := services.CreateApp(ctx, services.TemplateInput(tmpl, "cache"), &tmpl)
	s.Require().NoError(err)
	seeded := cache.Args
	b := signedIn(s.T(), &s.TestCase)

	for _, tt := range []struct {
		app  *models.App
		port int
		want []string
	}{
		{app, 8080, []string{"php", "artisan", "queue:work", "--queue=high,default"}},
		{cache, 6379, seeded},
	} {
		docker := fmt.Sprintf("/apps/%d?tab=docker", tt.app.ID)
		b.visit(docker)
		b.submit(nethttp.MethodPut, fmt.Sprintf("/apps/%d", tt.app.ID), docker, map[string]any{
			"repository": tt.app.Repository, "default_tag": tt.app.DefaultTag, "port": tt.port, "replicas": 1,
			"command": `php artisan queue:work "--queue=high,default"`,
		}).AssertHeader("Location", docker)

		s.Equal(tt.want, s.appNamed(tt.app.Name).Args)
	}
}
