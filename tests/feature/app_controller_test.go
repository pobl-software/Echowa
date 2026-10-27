package feature

import (
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"testing"

	"github.com/stretchr/testify/suite"

	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
	"goravel/tests"
)

type AppControllerTestSuite struct {
	suite.Suite
	tests.TestCase
	docker *docker.Fake
}

func TestAppControllerTestSuite(t *testing.T) {
	suite.Run(t, new(AppControllerTestSuite))
}

func (s *AppControllerTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.Require().True(facades.Cache().Flush())
	s.docker = s.FakeDocker()
}

const registryToken = "ghp_registryTokenNeverShown"

func newAppForm() map[string]any {
	return map[string]any{
		"name": "blog", "repository": "ghcr.io/owner/blog", "default_tag": "latest",
		"registry_id": nil, "domain": "blog.example.com", "port": 8080, "replicas": 1,
		"healthcheck_cmd": nil, "tls": true,
	}
}

func (s *AppControllerTestSuite) loadApp(name string) models.App {
	var app models.App
	s.Require().NoError(facades.Orm().Query().Where("name", name).FirstOrFail(&app))

	return app
}

func (s *AppControllerTestSuite) TestCreateFormListsRegistriesWithoutTokens() {
	createTestRegistry(s.T(), registryToken)
	b := signedIn(s.T(), &s.TestCase)
	b.forbid(registryToken)

	_, page := b.visit("/apps/create")

	page.AssertComponent("Apps/Create")
	registries := page.Props["registries"].([]any)
	s.Require().Len(registries, 1)
	s.Equal(map[string]any{"id": float64(1), "name": "ghcr", "host": "ghcr.io"}, registries[0])
}

func (s *AppControllerTestSuite) TestStoreCreatesTheAppWithAWebhookSecret() {
	registry := createTestRegistry(s.T(), registryToken)
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/apps/create")

	form := newAppForm()
	form["name"] = "  Blog "
	form["registry_id"] = registry.ID
	form["domain"] = "Blog.Example.com"
	resp := b.submit(nethttp.MethodPost, "/apps", "/apps/create", form)

	app := s.loadApp("blog")
	resp.AssertHeader("Location", fmt.Sprintf("/apps/%d?tab=environment", app.ID))
	s.Equal("blog.example.com", *app.Domain, "filters lowercase the domain")
	s.Equal(registry.ID, *app.RegistryID)
	s.Equal(8080, app.Port)
	s.True(app.TLS)
	s.Regexp(`^[0-9a-f]{64}$`, string(app.WebhookSecret))
}

func (s *AppControllerTestSuite) TestARegistryOnlyPullsFromItsOwnHost() {
	registry := createTestRegistry(s.T(), registryToken)
	app := createTestApp(s.T(), "shop", &registry.ID)
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/apps/create")

	form := newAppForm()
	form["registry_id"] = registry.ID
	for _, repository := range []string{"nginx", "evil.example/owner/blog"} {
		form["repository"] = repository
		b.submit(nethttp.MethodPost, "/apps", "/apps/create", form).AssertHeader("Location", "/apps/create")
		_, page := b.visit("/apps/create")
		s.Contains(page.Props["errors"].(map[string]any)["repository"], "isn't on ghcr.io", repository)
	}

	docker := fmt.Sprintf("/apps/%d?tab=docker", app.ID)
	b.visit(docker)
	form["name"] = nil
	form["repository"] = "docker.io/owner/shop"
	b.submit(nethttp.MethodPut, fmt.Sprintf("/apps/%d", app.ID), docker, form).AssertHeader("Location", docker)
	s.Equal("ghcr.io/owner/shop", s.loadApp("shop").Repository, "unchanged")
	count, err := facades.Orm().Query().Model(&models.App{}).Count()
	s.Require().NoError(err)
	s.EqualValues(1, count, "nothing was created")
}

func (s *AppControllerTestSuite) TestStoreValidates() {
	createTestApp(s.T(), "blog", nil)
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/apps/create")

	form := newAppForm()
	form["repository"] = "ghcr.io/owner/blog:v1"
	form["port"] = 0
	resp := b.submit(nethttp.MethodPost, "/apps", "/apps/create", form)
	resp.AssertHeader("Location", "/apps/create")

	_, page := b.visit("/apps/create")
	errs := page.Props["errors"].(map[string]any)
	s.Contains(errs, "name", "blog is taken")
	s.Contains(errs, "domain", "blog.example.com is taken")
	s.Contains(errs, "repository")
	s.Contains(errs, "port")
}

func (s *AppControllerTestSuite) TestShowSendsEnvKeysButNoSecrets() {
	registry := createTestRegistry(s.T(), registryToken)
	app := createTestApp(s.T(), "blog", &registry.ID)
	s.Require().NoError(services.SetEnvVars(context.Background(), app.ID, map[string]string{
		"DATABASE_URL": "postgres://user:envSecretPassword@db/blog", "B_KEY": "envSecretTwo",
	}))
	b := signedIn(s.T(), &s.TestCase)
	b.forbid(registryToken, string(app.WebhookSecret), "envSecretPassword", "envSecretTwo")

	_, page := b.visit(fmt.Sprintf("/apps/%d", app.ID))

	page.AssertComponent("Apps/Show")
	s.Equal([]any{"B_KEY", "DATABASE_URL"}, page.Props["envKeys"])
	s.Equal("echowa-blog", page.Props["serviceName"])
	props := page.Props["app"].(map[string]any)
	s.Equal("blog", props["name"])
	s.NotContains(props, "webhook_secret")
	s.NotContains(props["registry"], "token")
	s.Equal(services.StateNotDeployed, page.Props["status"].(map[string]any)["state"])

	// A full page load embeds the same page object in HTML.
	b.request(nethttp.MethodGet, fmt.Sprintf("/apps/%d", app.ID), nil, false, nil)
}

// Go's nil slices encode as null, which crashes pages that map over them.
func (s *AppControllerTestSuite) TestEmptyListsAreArraysNotNull() {
	app := createTestApp(s.T(), "blog", nil)
	b := signedIn(s.T(), &s.TestCase)

	_, page := b.visit(fmt.Sprintf("/apps/%d", app.ID))
	s.Equal([]any{}, page.Props["envKeys"])
	s.Equal([]any{}, page.Props["registries"])

	_, page = b.visit("/apps/create")
	s.Equal([]any{}, page.Props["registries"])

	_, page = b.visit("/registries")
	s.Equal([]any{}, page.Props["registries"])

	s.Require().NoError(services.DeleteApp(context.Background(), app))
	_, page = b.visit("/dashboard")
	s.Equal([]any{}, page.Props["apps"])
}

func (s *AppControllerTestSuite) TestMissingAppIs404() {
	b := signedIn(s.T(), &s.TestCase)
	b.request(nethttp.MethodGet, "/apps/999", nil, true, nil).AssertNotFound()
	b.request(nethttp.MethodGet, "/apps/abc", nil, true, nil).AssertNotFound()
	b.submit(nethttp.MethodDelete, "/apps/999", "/dashboard", nil).AssertNotFound()
}

func (s *AppControllerTestSuite) TestUpdateDockerKeepsTheNameAndTheDomain() {
	app := createTestApp(s.T(), "blog", nil)
	b := signedIn(s.T(), &s.TestCase)
	showURL := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(showURL)

	form := newAppForm()
	form["name"] = "renamed"
	form["domain"] = "ignored.example.com"
	form["tls"] = false
	form["replicas"] = 3
	form["healthcheck_cmd"] = "curl -f http://localhost:8080/up"
	resp := b.submit(nethttp.MethodPut, showURL, showURL, form)
	resp.AssertStatus(nethttp.StatusSeeOther)
	resp.AssertHeader("Location", showURL+"?tab=docker")

	updated := s.loadApp("blog")
	s.Equal(3, updated.Replicas)
	s.Equal("curl -f http://localhost:8080/up", *updated.HealthcheckCmd)
	s.Equal("blog.example.com", *updated.Domain, "the Docker config tab doesn't change the domain")
	s.True(updated.TLS)
	s.Equal(string(app.WebhookSecret), string(updated.WebhookSecret), "updating doesn't touch the secret")
}

func (s *AppControllerTestSuite) TestUpdateDomainChecksTheDomain() {
	app := createTestApp(s.T(), "blog", nil)
	createTestApp(s.T(), "shop", nil)
	b := signedIn(s.T(), &s.TestCase)
	showURL := fmt.Sprintf("/apps/%d", app.ID)
	domainURL := showURL + "/domain"
	b.visit(showURL)

	resp := b.submit(nethttp.MethodPut, domainURL, showURL, map[string]any{"domain": "", "tls": false, "replicas": 3})
	resp.AssertStatus(nethttp.StatusSeeOther)
	resp.AssertHeader("Location", showURL+"?tab=domains")

	updated := s.loadApp("blog")
	s.Nil(updated.Domain, "an empty domain is stored as NULL")
	s.False(updated.TLS)
	s.Equal(1, updated.Replicas, "the Domains tab doesn't change the Docker config")
	s.Equal("ghcr.io/owner/blog", updated.Repository)

	b.submit(nethttp.MethodPut, domainURL, showURL, map[string]any{"domain": "shop.example.com", "tls": true}).AssertHeader("Location", showURL)
	_, page := b.visit(showURL)
	s.Contains(page.Props["errors"], "domain")
	s.Nil(s.loadApp("blog").Domain)
}

func (s *AppControllerTestSuite) TestUpdateKeepsAnAppWithAVolumeToOneReplica() {
	app := createTestApp(s.T(), "blog", nil)
	addTestVolume(s.T(), app, "echowa-blog-1", "/data")
	b := signedIn(s.T(), &s.TestCase)
	showURL := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(showURL)

	form := newAppForm()
	form["replicas"] = 2
	b.submit(nethttp.MethodPut, showURL, showURL, form).AssertHeader("Location", showURL)
	_, page := b.visit(showURL)
	s.Equal("An app with a volume runs one replica. Two tasks can't share its data.", page.Props["errors"].(map[string]any)["replicas"])
	s.Equal(1, s.loadApp("blog").Replicas)

	form["replicas"] = 1
	form["healthcheck_cmd"] = "pg_isready"
	b.submit(nethttp.MethodPut, showURL, showURL, form).AssertHeader("Location", showURL+"?tab=docker")
	s.Equal("pg_isready", *s.loadApp("blog").HealthcheckCmd)
}

func (s *AppControllerTestSuite) TestDestroyRemovesTheServiceThenTheApp() {
	app := createTestApp(s.T(), "blog", nil)
	b := signedIn(s.T(), &s.TestCase)
	showURL := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(showURL)

	resp := b.submit(nethttp.MethodDelete, showURL, showURL, nil)
	resp.AssertStatus(nethttp.StatusSeeOther)
	resp.AssertHeader("Location", "/dashboard")
	s.Equal([]string{"echowa-blog"}, s.docker.Removed())
	count, err := facades.Orm().Query().Model(&models.App{}).Count()
	s.Require().NoError(err)
	s.Zero(count)
}

func (s *AppControllerTestSuite) TestDestroyKeepsTheAppWhenTheServiceCantBeRemoved() {
	app := createTestApp(s.T(), "blog", nil)
	s.docker.Remove = func(string) error { return fmt.Errorf("echowa-blog: %w", docker.ErrNotManaged) }
	b := signedIn(s.T(), &s.TestCase)
	showURL := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(showURL)

	b.submit(nethttp.MethodDelete, showURL, showURL, nil).AssertHeader("Location", showURL)
	_, page := b.visit(showURL)
	s.Contains(page.Flash["error"], "app was kept")
	s.Equal("blog", s.loadApp("blog").Name)
}

func (s *AppControllerTestSuite) TestEnvEditorSavesTheWholeSet() {
	app := createTestApp(s.T(), "blog", nil)
	ctx := context.Background()
	s.Require().NoError(services.SetEnvVars(ctx, app.ID, map[string]string{
		"KEEP": "keptSecret", "RENAME": "movedSecret", "CHANGE": "oldSecret", "DROP": "droppedSecret",
	}))
	b := signedIn(s.T(), &s.TestCase)
	b.forbid("keptSecret", "movedSecret", "oldSecret", "droppedSecret", "newSecret")
	envURL := fmt.Sprintf("/apps/%d/env", app.ID)
	showURL := fmt.Sprintf("/apps/%d?tab=environment", app.ID)
	b.visit(showURL)

	resp := b.submit(nethttp.MethodPut, envURL, showURL, map[string]any{"vars": []map[string]any{
		{"key": "KEEP", "original_key": "KEEP", "value": nil},
		{"key": "RENAMED", "original_key": "RENAME", "value": nil},
		{"key": "CHANGE", "original_key": "CHANGE", "value": "newSecret"},
		{"key": "ADDED", "original_key": nil, "value": ""},
	}})
	resp.AssertHeader("Location", showURL)
	_, check := b.visit(showURL)
	s.Empty(check.Props["errors"])

	env, err := services.EnvVars(ctx, app.ID)
	s.Require().NoError(err)
	s.Equal(map[string]string{"KEEP": "keptSecret", "RENAMED": "movedSecret", "CHANGE": "newSecret", "ADDED": ""}, env)

	_, page := b.visit(showURL)
	s.Equal([]any{"ADDED", "CHANGE", "KEEP", "RENAMED"}, page.Props["envKeys"])
}

func (s *AppControllerTestSuite) TestEnvEditorValidates() {
	app := createTestApp(s.T(), "blog", nil)
	s.Require().NoError(services.SetEnvVars(context.Background(), app.ID, map[string]string{"KEEP": "kept"}))
	b := signedIn(s.T(), &s.TestCase)
	envURL := fmt.Sprintf("/apps/%d/env", app.ID)
	showURL := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(showURL)

	put := func(vars []map[string]any) map[string]any {
		b.submit(nethttp.MethodPut, envURL, showURL, map[string]any{"vars": vars}).AssertHeader("Location", showURL)
		_, page := b.visit(showURL)
		return page.Props["errors"].(map[string]any)
	}

	errs := put([]map[string]any{{"key": "1BAD", "value": "x"}, {"key": "OK", "value": "x"}, {"key": "OK", "value": "y"}})
	s.Contains(errs, "vars.0.key")
	s.Contains(errs, "vars.2.key", "duplicate keys")

	errs = put([]map[string]any{{"key": "NEW", "original_key": "MISSING", "value": nil}})
	s.Equal("enter a value", errs["vars.0.value"])

	errs = put(nil)
	s.Contains(errs, "vars", "the list must be sent, so a bad request can't wipe the environment")

	env, err := services.EnvVars(context.Background(), app.ID)
	s.Require().NoError(err)
	s.Equal(map[string]string{"KEEP": "kept"}, env, "failed saves change nothing")
}

func (s *AppControllerTestSuite) TestEnvImportMergesPastedDotEnv() {
	app := createTestApp(s.T(), "blog", nil)
	s.Require().NoError(services.SetEnvVars(context.Background(), app.ID, map[string]string{"KEEP": "kept", "PORT": "1"}))
	b := signedIn(s.T(), &s.TestCase)
	b.forbid("pastedSecret")
	importURL := fmt.Sprintf("/apps/%d/env/import", app.ID)
	showURL := fmt.Sprintf("/apps/%d?tab=environment", app.ID)
	b.visit(showURL)

	resp := b.submit(nethttp.MethodPost, importURL, showURL, map[string]any{
		"text": "# from .env\nexport PORT=3000\nAPI_KEY=\"pastedSecret\"\n",
	})
	resp.AssertHeader("Location", showURL)
	_, page := b.visit(showURL)
	s.Equal("Imported 2 variable(s). They apply on the next deploy.", page.Flash["success"])

	env, err := services.EnvVars(context.Background(), app.ID)
	s.Require().NoError(err)
	s.Equal(map[string]string{"KEEP": "kept", "PORT": "3000", "API_KEY": "pastedSecret"}, env)

	b.submit(nethttp.MethodPost, importURL, showURL, map[string]any{"text": "OK=1\nnot a line"})
	_, page = b.visit(showURL)
	s.Equal("line 2: expected KEY=value", page.Props["errors"].(map[string]any)["text"])
}

func (s *AppControllerTestSuite) TestEnvRevealReturnsOneValueAsPlainJSON() {
	app := createTestApp(s.T(), "blog", nil)
	s.Require().NoError(services.SetEnvVars(context.Background(), app.ID, map[string]string{"API_KEY": "revealedSecret"}))
	b := signedIn(s.T(), &s.TestCase)
	b.forbid("revealedSecret")
	revealURL := fmt.Sprintf("/apps/%d/env/reveal", app.ID)
	showURL := fmt.Sprintf("/apps/%d", app.ID)
	b.visit(showURL)

	// The editor's fetch: plain JSON with the XSRF token, not an Inertia visit.
	resp := b.request(nethttp.MethodPost, revealURL, map[string]any{"key": "API_KEY"}, false, map[string]string{
		"X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie),
		"Accept":       "application/json",
	})
	resp.AssertOk()
	resp.AssertHeader("Cache-Control", "no-store")
	resp.AssertJson(map[string]any{"value": "revealedSecret"})

	b.request(nethttp.MethodPost, revealURL, map[string]any{"key": "NOPE"}, false, map[string]string{
		"X-XSRF-TOKEN": b.cookieValue(middlewareXsrfCookie),
	}).AssertNotFound()

	b.request(nethttp.MethodPost, revealURL, map[string]any{"key": "API_KEY"}, false, nil).AssertStatus(419)

	guest := newBrowser(s.T(), &s.TestCase)
	guest.visit("/login")
	guest.request(nethttp.MethodPost, revealURL, map[string]any{"key": "API_KEY"}, false, map[string]string{
		"X-XSRF-TOKEN": guest.cookieValue(middlewareXsrfCookie),
	}).AssertHeader("Location", "/login")
}

func (s *AppControllerTestSuite) TestShowWhenDockerIsDown() {
	app := createTestApp(s.T(), "blog", nil)
	s.docker.CheckErr = docker.ErrDaemonUnreachable
	s.docker.Status = func(string) (docker.ServiceStatus, error) { return docker.ServiceStatus{}, errors.New("down") }

	_, page := signedIn(s.T(), &s.TestCase).visit(fmt.Sprintf("/apps/%d", app.ID))

	s.Equal(services.StateUnknown, page.Props["status"].(map[string]any)["state"])
	s.NotNil(page.Props["dockerProblem"])
}
