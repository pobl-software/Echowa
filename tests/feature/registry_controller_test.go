package feature

import (
	"errors"
	"fmt"
	nethttp "net/http"
	"testing"

	"github.com/stretchr/testify/suite"

	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/tests"
)

type RegistryControllerTestSuite struct {
	suite.Suite
	tests.TestCase
	docker *docker.Fake
}

func TestRegistryControllerTestSuite(t *testing.T) {
	suite.Run(t, new(RegistryControllerTestSuite))
}

func (s *RegistryControllerTestSuite) SetupTest() {
	s.RefreshDatabase()
	s.docker = s.FakeDocker()
}

func (s *RegistryControllerTestSuite) loadRegistry(id uint) models.Registry {
	var registry models.Registry
	s.Require().NoError(facades.Orm().Query().Where("id", id).FirstOrFail(&registry))

	return registry
}

func (s *RegistryControllerTestSuite) TestIndexListsRegistriesWithAppCounts() {
	registry := createTestRegistry(s.T(), registryToken)
	createTestApp(s.T(), "blog", &registry.ID)
	createTestApp(s.T(), "shop", &registry.ID)
	createTestApp(s.T(), "public", nil)
	b := signedIn(s.T(), &s.TestCase)
	b.forbid(registryToken)

	_, page := b.visit("/settings/registries")

	page.AssertComponent("Settings/Registries")
	rows := page.Props["registries"].([]any)
	s.Require().Len(rows, 1)
	s.Equal(map[string]any{"id": float64(registry.ID), "name": "ghcr", "host": "ghcr.io", "username": "louis", "apps_count": float64(2)}, rows[0])
}

func (s *RegistryControllerTestSuite) TestStoreEncryptsTheToken() {
	b := signedIn(s.T(), &s.TestCase)
	b.forbid("ghp_brandNewToken")
	b.visit("/settings/registries/create")

	resp := b.submit(nethttp.MethodPost, "/settings/registries", "/settings/registries/create", map[string]any{
		"name": " ghcr ", "host": "GHCR.io", "username": "louis", "token": "ghp_brandNewToken",
	})
	resp.AssertHeader("Location", "/settings/registries")

	var registry models.Registry
	s.Require().NoError(facades.Orm().Query().Where("name", "ghcr").FirstOrFail(&registry))
	s.Equal("ghcr.io", registry.Host)
	s.Equal("ghp_brandNewToken", string(registry.Token))
	var stored string
	s.Require().NoError(facades.DB().Table("registries").Where("id", registry.ID).Value("token", &stored))
	s.NotContains(stored, "ghp_brandNewToken")

	_, page := b.visit("/settings/registries")
	s.Contains(page.Flash["success"], "Added ghcr")
}

func (s *RegistryControllerTestSuite) TestStoreValidates() {
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/settings/registries/create")

	b.submit(nethttp.MethodPost, "/settings/registries", "/settings/registries/create", map[string]any{
		"name": "", "host": "https://ghcr.io", "username": "", "token": "",
	}).AssertHeader("Location", "/settings/registries/create")

	_, page := b.visit("/settings/registries/create")
	errs := page.Props["errors"].(map[string]any)
	for _, field := range []string{"name", "host", "username", "token"} {
		s.Contains(errs, field)
	}
}

func (s *RegistryControllerTestSuite) TestEditNeverSendsTheTokenAndBlankKeepsIt() {
	registry := createTestRegistry(s.T(), registryToken)
	b := signedIn(s.T(), &s.TestCase)
	b.forbid(registryToken, "ghp_replacement")
	editURL := fmt.Sprintf("/settings/registries/%d/edit", registry.ID)
	updateURL := fmt.Sprintf("/settings/registries/%d", registry.ID)

	_, page := b.visit(editURL)
	page.AssertComponent("Settings/RegistryForm")
	s.NotContains(page.Props["registry"], "token")

	b.submit(nethttp.MethodPut, updateURL, editURL, map[string]any{
		"name": "github", "host": "ghcr.io", "username": "someone", "token": "",
	}).AssertHeader("Location", "/settings/registries")
	updated := s.loadRegistry(registry.ID)
	s.Equal("github", updated.Name)
	s.Equal("someone", updated.Username)
	s.Equal(registryToken, string(updated.Token), "a blank token keeps the stored one")

	b.submit(nethttp.MethodPut, updateURL, editURL, map[string]any{
		"name": "github", "host": "ghcr.io", "username": "someone", "token": "ghp_replacement",
	}).AssertHeader("Location", "/settings/registries")
	s.Equal("ghp_replacement", string(s.loadRegistry(registry.ID).Token))
}

func (s *RegistryControllerTestSuite) TestDestroyRefusesARegistryInUse() {
	registry := createTestRegistry(s.T(), registryToken)
	app := createTestApp(s.T(), "blog", &registry.ID)
	b := signedIn(s.T(), &s.TestCase)
	b.visit("/settings/registries")
	url := fmt.Sprintf("/settings/registries/%d", registry.ID)

	b.submit(nethttp.MethodDelete, url, "/settings/registries", nil).AssertHeader("Location", "/settings/registries")
	_, page := b.visit("/settings/registries")
	s.Contains(page.Flash["error"], "apps still pull through it")
	s.Equal("ghcr", s.loadRegistry(registry.ID).Name)

	_, err := facades.Orm().Query().Delete(app)
	s.Require().NoError(err)
	b.submit(nethttp.MethodDelete, url, "/settings/registries", nil).AssertHeader("Location", "/settings/registries")
	count, err := facades.Orm().Query().Model(&models.Registry{}).Count()
	s.Require().NoError(err)
	s.Zero(count)
}

func (s *RegistryControllerTestSuite) TestTestConnectionLogsInWithTheStoredCredentials() {
	registry := createTestRegistry(s.T(), registryToken)
	var got docker.RegistryAuth
	s.docker.Login = func(auth docker.RegistryAuth) error {
		got = auth
		return nil
	}
	b := signedIn(s.T(), &s.TestCase)
	b.forbid(registryToken)
	b.visit("/settings/registries")
	url := fmt.Sprintf("/settings/registries/%d/test", registry.ID)

	b.submit(nethttp.MethodPost, url, "/settings/registries", nil).AssertHeader("Location", "/settings/registries")
	s.Equal(docker.RegistryAuth{ServerAddress: "ghcr.io", Username: "louis", Password: registryToken}, got)
	_, page := b.visit("/settings/registries")
	s.Equal("Logged in to ghcr.io as louis.", page.Flash["success"])

	s.docker.Login = func(docker.RegistryAuth) error {
		return errors.New("log in to ghcr.io: Error response from daemon: denied: denied")
	}
	b.submit(nethttp.MethodPost, url, "/settings/registries", nil).AssertHeader("Location", "/settings/registries")
	_, page = b.visit("/settings/registries")
	s.Contains(page.Flash["error"], "Login to ghcr.io as louis failed")
	s.Contains(page.Flash["error"], "denied")
}

func (s *RegistryControllerTestSuite) TestMissingRegistryIs404() {
	b := signedIn(s.T(), &s.TestCase)
	b.request(nethttp.MethodGet, "/settings/registries/42/edit", nil, true, nil).AssertNotFound()
}
