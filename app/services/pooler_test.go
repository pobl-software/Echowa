package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/docker"
	"goravel/app/models"
	"goravel/app/templates"
)

func TestPoolerSpecGetsOnlyTheEnvThePoolerReads(t *testing.T) {
	slug := "postgres"
	app := &models.App{Name: "db", Template: &slug}
	env := map[string]string{
		"POSTGRES_USER":     "db",
		"POSTGRES_DB":       "db",
		"POSTGRES_PASSWORD": "secret",
		templates.EnvTLSCA:  "certificate",
		"EXTRA":             "x",
	}

	spec, err := PoolerSpec(app, env)
	require.NoError(t, err)

	assert.Equal(t, "echowa-db-pooler", spec.Name)
	assert.Equal(t, "db", spec.App)
	assert.Equal(t, docker.RolePooler, spec.Role)
	assert.EqualValues(t, 1, spec.Replicas)
	assert.Equal(t, map[string]string{
		templates.PoolerServerEnv: "echowa-db",
		"POSTGRES_USER":           "db",
		"POSTGRES_DB":             "db",
		"POSTGRES_PASSWORD":       "secret",
	}, spec.Env, "external access's certificate and the user's own vars stay with the database")
	assert.NotEmpty(t, spec.Entrypoint)
	require.NotNil(t, spec.Healthcheck)
	assert.Equal(t, docker.Limits{NanoCPUs: 500_000_000, MemoryBytes: 128 << 20}, spec.Limits, "the database's limits don't apply")
	assert.Nil(t, spec.Route)
	assert.Nil(t, spec.HostPort, "reached on the overlay only")
	assert.Empty(t, spec.Volumes)
}

func TestPoolerSpecNeedsTheDatabasesPassword(t *testing.T) {
	slug := "postgres"
	_, err := PoolerSpec(&models.App{Name: "db", Template: &slug}, map[string]string{"POSTGRES_USER": "db", "POSTGRES_DB": "db"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "POSTGRES_PASSWORD is missing")
}

func TestOnlyPostgresAppsHaveAPooler(t *testing.T) {
	for _, slug := range []string{"mysql", "redis"} {
		_, err := PoolerSpec(&models.App{Name: "db", Template: &slug}, map[string]string{})
		assert.ErrorIs(t, err, ErrNoPooler, slug)
	}
	_, err := PoolerSpec(&models.App{Name: "web"}, map[string]string{})
	assert.ErrorIs(t, err, ErrNoPooler, "a custom app")
}
