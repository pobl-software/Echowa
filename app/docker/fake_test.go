package docker

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFakeDigest(t *testing.T) {
	assert.Regexp(t, `^ghcr\.io/owner/app@sha256:[0-9a-f]{64}$`, FakeDigest("ghcr.io/owner/app:v1"))
	assert.Regexp(t, `^localhost:5000/app@sha256:`, FakeDigest("localhost:5000/app:v1"), "a registry port isn't a tag")
	assert.Equal(t, FakeDigest("app:v1"), FakeDigest("app:v1"))
	assert.NotEqual(t, FakeDigest("app:v1"), FakeDigest("app:v2"))
}

func TestFakeRollbackRestoresThePreviousSpec(t *testing.T) {
	ctx := context.Background()
	fake := &Fake{Wait: func(spec ServiceSpec) (UpdateResult, error) {
		if spec.Image == "app@sha256:bad" {
			return UpdateResult{Outcome: OutcomeRolledBack, Message: "rollback completed"}, nil
		}
		return UpdateResult{Outcome: OutcomeCompleted}, nil
	}}
	spec := ServiceSpec{Name: "echowa-app", App: "app", Image: "app@sha256:good", Replicas: 1}

	require.NoError(t, fake.DeployService(ctx, spec))
	_, err := fake.WaitForUpdate(ctx, "echowa-app")
	require.NoError(t, err)

	spec.Image = "app@sha256:bad"
	require.NoError(t, fake.DeployService(ctx, spec))
	result, err := fake.WaitForUpdate(ctx, "echowa-app")
	require.NoError(t, err)
	assert.Equal(t, OutcomeRolledBack, result.Outcome)

	status, err := fake.ServiceStatus(ctx, "echowa-app")
	require.NoError(t, err)
	assert.Equal(t, "app@sha256:good", status.Image)
	assert.Equal(t, UpdateRollbackCompleted, status.UpdateState)
	assert.Len(t, fake.Deploys(), 2)

	require.NoError(t, fake.RemoveService(ctx, "echowa-app"))
	_, err = fake.ServiceStatus(ctx, "echowa-app")
	assert.ErrorIs(t, err, ErrServiceNotFound)
}

func TestFakeRemoveVolumeWaitsForTheService(t *testing.T) {
	ctx := context.Background()
	fake := &Fake{}
	spec := ServiceSpec{Name: "echowa-db", App: "db", Image: "postgres@sha256:a", Replicas: 1,
		Volumes: []Volume{{Name: "echowa-db-data", Target: "/data"}}}
	require.NoError(t, fake.DeployService(ctx, spec))
	exists, _ := fake.VolumeExists(ctx, "echowa-db-data")
	assert.True(t, exists, "a deploy creates its volumes")

	assert.ErrorIs(t, fake.RemoveVolume(ctx, "echowa-db-data"), ErrVolumeInUse)
	require.NoError(t, fake.RemoveService(ctx, "echowa-db"))
	exists, _ = fake.VolumeExists(ctx, "echowa-db-data")
	assert.True(t, exists, "removing the service keeps its volumes")
	require.NoError(t, fake.RemoveVolume(ctx, "echowa-db-data"))
	assert.Equal(t, []string{"echowa-db-data"}, fake.RemovedVolumes())
	exists, _ = fake.VolumeExists(ctx, "echowa-db-data")
	assert.False(t, exists)
}

func TestFakeListsServicesByRole(t *testing.T) {
	ctx := context.Background()
	fake := &Fake{}
	require.NoError(t, fake.DeployService(ctx, ServiceSpec{Name: "echowa-db", App: "db", Image: "postgres@sha256:a", Replicas: 1}))
	require.NoError(t, fake.DeployService(ctx, ServiceSpec{Name: "echowa-db-pooler", App: "db", Role: RolePooler, Image: "pgbouncer@sha256:a", Replicas: 1}))

	poolers, err := fake.ServicesWithRole(ctx, RolePooler)
	require.NoError(t, err)
	assert.Equal(t, []ServiceRef{{Name: "echowa-db-pooler", App: "db"}}, poolers)

	require.NoError(t, fake.RemoveService(ctx, "echowa-db-pooler"))
	poolers, err = fake.ServicesWithRole(ctx, RolePooler)
	require.NoError(t, err)
	assert.Empty(t, poolers)
	_, err = fake.ServicesWithRole(ctx, "")
	assert.Error(t, err, "an empty role would match every service without one")
}

func TestFakeMatchesTheSpecItRuns(t *testing.T) {
	ctx := context.Background()
	fake := &Fake{Wait: func(spec ServiceSpec) (UpdateResult, error) {
		if spec.Env["PASSWORD"] == "bad" {
			return UpdateResult{Outcome: OutcomeRolledBack}, nil
		}
		return UpdateResult{Outcome: OutcomeCompleted}, nil
	}}
	spec := ServiceSpec{Name: "echowa-db-pooler", App: "db", Role: RolePooler, Image: "pgbouncer@sha256:a", Replicas: 1,
		Env: map[string]string{"PASSWORD": "good"}}

	same, err := fake.ServiceMatches(ctx, spec)
	require.NoError(t, err)
	assert.False(t, same, "no service yet")
	require.NoError(t, fake.DeployService(ctx, spec))
	same, err = fake.ServiceMatches(ctx, spec)
	require.NoError(t, err)
	assert.True(t, same)

	bad := spec
	bad.Env = map[string]string{"PASSWORD": "bad"}
	require.NoError(t, fake.DeployService(ctx, bad))
	_, err = fake.WaitForUpdate(ctx, bad.Name)
	require.NoError(t, err)
	same, err = fake.ServiceMatches(ctx, bad)
	require.NoError(t, err)
	assert.False(t, same, "a rollback goes back to the previous spec, so the next sync tries again")
	same, err = fake.ServiceMatches(ctx, spec)
	require.NoError(t, err)
	assert.True(t, same)
}
