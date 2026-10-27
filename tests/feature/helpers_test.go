package feature

import (
	"context"
	nethttp "net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
	"goravel/tests"
)

// signedIn returns a browser logged in as a fresh admin.
func signedIn(t *testing.T, tc *tests.TestCase) *browser {
	t.Helper()
	_, _, err := services.SaveUser("Admin", "admin@example.test", "password123")
	require.NoError(t, err)
	// The login limiter counts every attempt, and every test signs in as
	// the same user from the same IP.
	require.True(t, facades.Cache().Flush())

	b := newBrowser(t, tc)
	b.visit("/login")
	b.submit(nethttp.MethodPost, "/login", "/login", map[string]any{
		"email": "admin@example.test", "password": "password123",
	}).AssertFound()

	return b
}

func createTestRegistry(t *testing.T, token string) *models.Registry {
	t.Helper()
	registry, err := services.CreateRegistry(context.Background(), services.RegistryInput{
		Name: "ghcr", Host: "ghcr.io", Username: "louis", Token: token,
	})
	require.NoError(t, err)

	return registry
}

func addTestVolume(t *testing.T, app *models.App, name, target string) {
	t.Helper()
	require.NoError(t, facades.Orm().Query().Create(&models.AppVolume{AppID: app.ID, Name: name, Target: target}))
}

func createTestApp(t *testing.T, name string, registryID *uint) *models.App {
	t.Helper()
	domain := name + ".example.com"
	app, err := services.CreateApp(context.Background(), services.AppInput{
		Name: name, Repository: "ghcr.io/owner/" + name, DefaultTag: "latest", RegistryID: registryID,
		Domain: &domain, Port: 8080, Replicas: 1, TLS: true,
	}, nil)
	require.NoError(t, err)

	return app
}
