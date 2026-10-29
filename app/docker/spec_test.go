package docker

import (
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testDigest = "ghcr.io/owner/blog@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestSwarmSpec(t *testing.T) {
	cfg := DefaultConfig()
	spec := ServiceSpec{
		Name:     "echowa-blog",
		App:      "blog",
		Image:    testDigest,
		Env:      map[string]string{"B": "2", "A": "1=one"},
		Replicas: 2,
		Route:    &Route{Domain: "blog.example.com", Port: 8080, TLS: true},
	}

	got, err := swarmSpec(spec, cfg, 7)
	require.NoError(t, err)

	assert.Equal(t, "echowa-blog", got.Name)
	assert.Equal(t, "true", got.Labels[LabelManaged])
	assert.Equal(t, "blog", got.Labels[LabelApp])
	assert.Equal(t, "Host(`blog.example.com`)", got.Labels["traefik.http.routers.echowa-blog.rule"])
	assert.Equal(t, "le", got.Labels["traefik.http.routers.echowa-blog.tls.certresolver"])

	container := got.TaskTemplate.ContainerSpec
	assert.Equal(t, testDigest, container.Image)
	assert.Equal(t, []string{"A=1=one", "B=2"}, container.Env, "sorted, values kept verbatim")
	assert.Nil(t, container.Healthcheck, "nil keeps the image's HEALTHCHECK")
	assert.Equal(t, []swarm.NetworkAttachmentConfig{{Target: "echowa-public"}}, got.TaskTemplate.Networks)
	assert.EqualValues(t, 7, got.TaskTemplate.ForceUpdate)
	assert.EqualValues(t, 2, *got.Mode.Replicated.Replicas)
	assert.Equal(t, &swarm.EndpointSpec{Mode: swarm.ResolutionModeVIP}, got.EndpointSpec, "Traefik ignores services without an explicit mode")

	assert.Equal(t, &swarm.UpdateConfig{
		Parallelism:   1,
		Order:         swarm.UpdateOrderStartFirst,
		FailureAction: swarm.UpdateFailureActionRollback,
		Monitor:       30 * time.Second,
	}, got.UpdateConfig)
	assert.Equal(t, swarm.UpdateOrderStartFirst, got.RollbackConfig.Order)
	assert.Equal(t, swarm.UpdateFailureActionPause, got.RollbackConfig.FailureAction)
}

func TestSwarmSpecWithoutRouteHasNoTraefikLabels(t *testing.T) {
	got, err := swarmSpec(ServiceSpec{Name: "echowa-worker", App: "worker", Image: testDigest, Replicas: 1}, DefaultConfig(), 0)
	require.NoError(t, err)

	assert.Equal(t, map[string]string{LabelManaged: "true", LabelApp: "worker", LabelSpec: got.Labels[LabelSpec]}, got.Labels)
	assert.Empty(t, got.TaskTemplate.ContainerSpec.Env)
}

func TestSwarmSpecHealthcheckDefaults(t *testing.T) {
	spec := ServiceSpec{Name: "echowa-a", App: "a", Image: testDigest, Replicas: 1,
		Healthcheck: &Healthcheck{Command: "curl -f http://localhost/up", Retries: 5}}

	got, err := swarmSpec(spec, DefaultConfig(), 0)
	require.NoError(t, err)

	health := got.TaskTemplate.ContainerSpec.Healthcheck
	require.NotNil(t, health)
	assert.Equal(t, []string{"CMD-SHELL", "curl -f http://localhost/up"}, health.Test)
	assert.Equal(t, 10*time.Second, health.Interval)
	assert.Equal(t, 5*time.Second, health.Timeout)
	assert.Equal(t, 30*time.Second, health.StartPeriod)
	assert.Equal(t, 5, health.Retries, "explicit values win")
}

func TestSwarmSpecRejectsBadInput(t *testing.T) {
	_, err := swarmSpec(ServiceSpec{App: "a", Image: testDigest}, DefaultConfig(), 0)
	assert.ErrorContains(t, err, "required")

	_, err = swarmSpec(ServiceSpec{Name: "echowa-a", App: "a", Image: testDigest,
		Route: &Route{Domain: "bad domain", Port: 80}}, DefaultConfig(), 0)
	assert.ErrorContains(t, err, "invalid domain")
}

func TestSwarmSpecWithVolumesUpdatesStopFirst(t *testing.T) {
	spec := ServiceSpec{Name: "echowa-db", App: "db", Image: testDigest, Replicas: 1,
		Args:    []string{"sh", "-c", `exec server --password "$PASSWORD"`},
		Volumes: []Volume{{Name: "echowa-db-data", Target: "/var/lib/postgresql"}}}

	got, err := swarmSpec(spec, DefaultConfig(), 0)
	require.NoError(t, err)

	container := got.TaskTemplate.ContainerSpec
	assert.Equal(t, spec.Args, container.Args)
	assert.Equal(t, []mount.Mount{{
		Type:          mount.TypeVolume,
		Source:        "echowa-db-data",
		Target:        "/var/lib/postgresql",
		VolumeOptions: &mount.VolumeOptions{Labels: map[string]string{LabelManaged: "true", LabelApp: "db"}},
	}}, container.Mounts)
	assert.Equal(t, swarm.UpdateOrderStopFirst, got.UpdateConfig.Order, "two tasks must never share a volume")
	assert.Equal(t, swarm.UpdateOrderStopFirst, got.RollbackConfig.Order)
	assert.Equal(t, swarm.UpdateFailureActionRollback, got.UpdateConfig.FailureAction, "the healthcheck still gates the update")
}

func TestSwarmSpecWithoutVolumesOrArgsKeepsTheImageDefaults(t *testing.T) {
	got, err := swarmSpec(ServiceSpec{Name: "echowa-a", App: "a", Image: testDigest, Replicas: 3}, DefaultConfig(), 0)
	require.NoError(t, err)

	assert.Nil(t, got.TaskTemplate.ContainerSpec.Args, "nil keeps the image's CMD")
	assert.Nil(t, got.TaskTemplate.ContainerSpec.Command, "nil keeps the image's ENTRYPOINT")
	assert.Equal(t, map[string]string{LabelApp: "a"}, got.TaskTemplate.ContainerSpec.Labels)
	assert.Empty(t, got.TaskTemplate.ContainerSpec.Mounts)
	assert.Equal(t, swarm.UpdateOrderStartFirst, got.UpdateConfig.Order)
}

func TestSwarmSpecForARoleServiceLabelsItsTasks(t *testing.T) {
	got, err := swarmSpec(ServiceSpec{
		Name: "echowa-db-pooler", App: "db", Role: RolePooler, Image: testDigest, Replicas: 1,
		Entrypoint: []string{"sh", "-c", "exec pgbouncer"},
	}, DefaultConfig(), 0)
	require.NoError(t, err)

	assert.Equal(t, map[string]string{LabelManaged: "true", LabelApp: "db", LabelRole: RolePooler, LabelSpec: got.Labels[LabelSpec]}, got.Labels,
		"the service still belongs to the app")
	assert.Equal(t, map[string]string{LabelApp: "db", LabelRole: RolePooler}, got.TaskTemplate.ContainerSpec.Labels,
		"the metrics collector reads the role to keep its tasks out of the app's figures")
	assert.Equal(t, []string{"sh", "-c", "exec pgbouncer"}, got.TaskTemplate.ContainerSpec.Command)
	assert.Nil(t, got.TaskTemplate.ContainerSpec.Args, "the image's CMD isn't added back (the pooler's docker test checks Docker drops it)")
}

func TestSpecHashChangesWithAnythingButTheForceUpdateCounter(t *testing.T) {
	spec := ServiceSpec{Name: "echowa-db-pooler", App: "db", Role: RolePooler, Image: testDigest, Replicas: 1,
		Env: map[string]string{"PASSWORD": "one"}, Limits: Limits{NanoCPUs: 500_000_000, MemoryBytes: 128 << 20}}
	hash := func(spec ServiceSpec, force uint64) string {
		t.Helper()
		got, err := swarmSpec(spec, DefaultConfig(), force)
		require.NoError(t, err)
		require.Len(t, got.Labels[LabelSpec], 64)
		return got.Labels[LabelSpec]
	}

	assert.Equal(t, hash(spec, 1), hash(spec, 2), "every deploy bumps the counter")

	changed := spec
	changed.Env = map[string]string{"PASSWORD": "two"}
	assert.NotEqual(t, hash(spec, 1), hash(changed, 1), "a new password")
	changed = spec
	changed.Limits.MemoryBytes = 256 << 20
	assert.NotEqual(t, hash(spec, 1), hash(changed, 1), "new limits")
	changed = spec
	changed.Image = "ghcr.io/owner/blog@sha256:" + strings.Repeat("f", 64)
	assert.NotEqual(t, hash(spec, 1), hash(changed, 1), "a new image")
	cfg := DefaultConfig()
	cfg.UpdateMonitor = time.Minute
	got, err := swarmSpec(spec, cfg, 1)
	require.NoError(t, err)
	assert.NotEqual(t, hash(spec, 1), got.Labels[LabelSpec], "a change in how Echowa deploys")
}

func TestSwarmSpecRejectsBadVolumes(t *testing.T) {
	base := ServiceSpec{Name: "echowa-db", App: "db", Image: testDigest, Replicas: 1}
	for name, volumes := range map[string][]Volume{
		"no name":         {{Target: "/data"}},
		"relative target": {{Name: "echowa-db-data", Target: "data"}},
	} {
		spec := base
		spec.Volumes = volumes
		_, err := swarmSpec(spec, DefaultConfig(), 0)
		assert.ErrorContains(t, err, "absolute target", name)
	}

	base.Replicas = 2
	base.Volumes = []Volume{{Name: "echowa-db-data", Target: "/data"}}
	_, err := swarmSpec(base, DefaultConfig(), 0)
	assert.ErrorContains(t, err, "can't run 2 replicas")
}

func TestSwarmSpecWithAHostPortPublishesItInHostMode(t *testing.T) {
	spec := ServiceSpec{Name: "echowa-db", App: "db", Image: testDigest, Replicas: 1,
		HostPort: &HostPort{Published: 4040, Target: 5432}}

	got, err := swarmSpec(spec, DefaultConfig(), 0)
	require.NoError(t, err)

	assert.Equal(t, &swarm.EndpointSpec{
		Mode: swarm.ResolutionModeVIP,
		Ports: []swarm.PortConfig{{
			Protocol:      network.TCP,
			TargetPort:    5432,
			PublishedPort: 4040,
			PublishMode:   swarm.PortConfigPublishModeHost,
		}},
	}, got.EndpointSpec)
	assert.Equal(t, swarm.UpdateOrderStopFirst, got.UpdateConfig.Order, "two tasks can't bind one host port")
	assert.Equal(t, swarm.UpdateOrderStopFirst, got.RollbackConfig.Order)

	spec.Replicas = 2
	_, err = swarmSpec(spec, DefaultConfig(), 0)
	assert.ErrorContains(t, err, "can't run 2 replicas")

	spec.Replicas = 1
	spec.HostPort.Published = 70000
	_, err = swarmSpec(spec, DefaultConfig(), 0)
	assert.ErrorContains(t, err, "out of range")
}

func TestSwarmSpecLimitsEachTaskWithoutSwap(t *testing.T) {
	spec := ServiceSpec{Name: "echowa-api", App: "api", Image: testDigest, Replicas: 1,
		Limits: Limits{NanoCPUs: 1_500_000_000, MemoryBytes: 512 << 20}}

	got, err := swarmSpec(spec, DefaultConfig(), 0)
	require.NoError(t, err)

	resources := got.TaskTemplate.Resources
	require.NotNil(t, resources)
	assert.Equal(t, &swarm.Limit{NanoCPUs: 1_500_000_000, MemoryBytes: 512 << 20, Pids: maxProcesses}, resources.Limits)
	assert.Nil(t, resources.Reservations, "a reservation would count twice while a start-first update overlaps")
	require.NotNil(t, resources.SwapBytes)
	assert.Zero(t, *resources.SwapBytes, "the memory limit is all the memory a task gets")

	spec.Limits.SwapBytes = 256 << 20
	got, err = swarmSpec(spec, DefaultConfig(), 0)
	require.NoError(t, err)
	assert.EqualValues(t, 256<<20, *got.TaskTemplate.Resources.SwapBytes, "swap only where the app allows it")
}

func TestSwarmSpecWithoutLimitsStillCapsProcesses(t *testing.T) {
	got, err := swarmSpec(ServiceSpec{Name: "echowa-api", App: "api", Image: testDigest, Replicas: 1}, DefaultConfig(), 0)
	require.NoError(t, err)

	resources := got.TaskTemplate.Resources
	require.NotNil(t, resources)
	assert.Equal(t, &swarm.Limit{Pids: maxProcesses}, resources.Limits, "a fork bomb can't fill the host's process table")
	assert.Nil(t, resources.SwapBytes)
}
