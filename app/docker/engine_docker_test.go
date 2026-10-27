//go:build docker

// Integration test against a real Swarm manager (Docker Desktop or OrbStack
// after `docker swarm init`). Run with:
//
//	go test -tags docker ./app/docker/...
//
// It pulls traefik/whoami v1.11.0 and v1.12.0 and busybox, creates and
// removes its own services and volumes, and prunes only traefik/whoami
// images other than v1.12.0.
package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	nethttp "net/http"
	"strings"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const whoami = "traefik/whoami"

const busybox = "busybox:1.37"

func TestEngineAgainstLocalSwarm(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cfg := DefaultConfig()
	cfg.UpdateMonitor = 3 * time.Second
	cfg.DeployTimeout = 2 * time.Minute
	cfg.PollInterval = 500 * time.Millisecond
	engine, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = engine.Close() })

	require.NoError(t, engine.Check(ctx), "needs a local Swarm manager: run `docker swarm init` (deploy/README.md)")
	ensureNetwork(t, engine, cfg.Network)

	name := fmt.Sprintf("echowa-it-%d", time.Now().UnixNano()%1_000_000)
	t.Cleanup(func() { _ = engine.RemoveService(context.Background(), name) })

	// Pull: a tag resolves to a digest, and pulling the digest is stable.
	older, err := engine.PullImage(ctx, whoami+":v1.11.0", nil)
	require.NoError(t, err)
	newer, err := engine.PullImage(ctx, whoami+":v1.12.0", nil)
	require.NoError(t, err)
	assert.Regexp(t, `^traefik/whoami@sha256:[0-9a-f]{64}$`, older)
	assert.NotEqual(t, older, newer)
	again, err := engine.PullImage(ctx, newer, nil)
	require.NoError(t, err)
	assert.Equal(t, newer, again, "pulling by digest returns the same digest")

	// Create.
	spec := ServiceSpec{
		Name:     name,
		App:      "it",
		Image:    older,
		Env:      map[string]string{"ECHOWA_TEST": "1"},
		Replicas: 1,
		Route:    &Route{Domain: name + ".localhost", Port: 80},
	}
	require.NoError(t, engine.DeployService(ctx, spec))
	result, err := engine.WaitForUpdate(ctx, name)
	require.NoError(t, err)
	assert.Equal(t, OutcomeCompleted, result.Outcome)

	status := waitForRunning(t, engine, name, 1)
	assert.Equal(t, older, status.Image)
	assert.Equal(t, UpdateNone, status.UpdateState, "a new service has no update status")

	svc, err := engine.inspect(ctx, name)
	require.NoError(t, err)
	assert.Equal(t, "true", svc.Spec.Labels[LabelManaged])
	assert.Equal(t, "Host(`"+name+".localhost`)", svc.Spec.Labels["traefik.http.routers."+name+".rule"])
	assert.Equal(t, []string{"ECHOWA_TEST=1"}, svc.Spec.TaskTemplate.ContainerSpec.Env)
	assert.Equal(t, swarm.UpdateOrderStartFirst, svc.Spec.UpdateConfig.Order)
	assert.Equal(t, swarm.UpdateFailureActionRollback, svc.Spec.UpdateConfig.FailureAction)

	// Stats: the task container carries the app label, and Docker reports
	// counters for it.
	stats, err := engine.ContainerStats(ctx)
	require.NoError(t, err)
	var task *ContainerStats
	for i := range stats {
		if strings.HasPrefix(stats[i].Name, name+".1.") {
			task = &stats[i]
		}
	}
	require.NotNil(t, task, "the task container is in %v", stats)
	assert.Equal(t, "it", task.App)
	assert.Positive(t, task.CPUNanos)
	assert.Positive(t, task.MemoryUsed)
	assert.Positive(t, task.MemoryLimit)
	assert.False(t, task.Read.IsZero())

	// Update to the newer image: the existing version index is passed, so
	// Swarm accepts it, and WaitForUpdate waits for this update, not the
	// state left by the create.
	spec.Image = newer
	require.NoError(t, engine.DeployService(ctx, spec))
	result, err = engine.WaitForUpdate(ctx, name)
	require.NoError(t, err)
	assert.Equal(t, OutcomeCompleted, result.Outcome)
	status = waitForRunning(t, engine, name, 1)
	assert.Equal(t, newer, status.Image)
	assert.Equal(t, UpdateCompleted, status.UpdateState)

	// Redeploying the same spec still rolls the task.
	before, err := engine.inspect(ctx, name)
	require.NoError(t, err)
	require.NoError(t, engine.DeployService(ctx, spec))
	result, err = engine.WaitForUpdate(ctx, name)
	require.NoError(t, err)
	assert.Equal(t, OutcomeCompleted, result.Outcome)
	after, err := engine.inspect(ctx, name)
	require.NoError(t, err)
	assert.Greater(t, after.Spec.TaskTemplate.ForceUpdate, before.Spec.TaskTemplate.ForceUpdate)

	// A deploy whose healthcheck never passes is rolled back by Swarm. The
	// whoami image has no shell, so any CMD-SHELL check fails.
	failing := spec
	failing.Image = older
	failing.Healthcheck = &Healthcheck{Command: "exit 1", Interval: time.Second, Timeout: time.Second, StartPeriod: time.Millisecond, Retries: 1}
	require.NoError(t, engine.DeployService(ctx, failing))
	result, err = engine.WaitForUpdate(ctx, name)
	require.NoError(t, err)
	assert.Equal(t, OutcomeRolledBack, result.Outcome, "message: %s", result.Message)
	status = waitForRunning(t, engine, name, 1)
	assert.Equal(t, newer, status.Image, "rollback restores the previous digest")

	// Logs: whoami prints its startup line with Go's log package, which
	// writes to stderr.
	logs, err := engine.StreamServiceLogs(ctx, name, LogOptions{})
	require.NoError(t, err)
	var lines []LogLine
	for line, err := range logs {
		require.NoError(t, err)
		lines = append(lines, line)
	}
	require.NotEmpty(t, lines)
	assert.True(t, containsLine(lines, "Starting up on port 80"), "lines: %v", lines)
	assert.False(t, lines[0].Time.IsZero(), "timestamps are parsed")
	assert.Equal(t, Stderr, lines[0].Stream)

	// Each line names its task, and the earlier deploys' stopped tasks
	// still have their lines: whoami says it's starting once per task.
	tasks, err := engine.ServiceTasks(ctx, name)
	require.NoError(t, err)
	svc, err = engine.inspect(ctx, name)
	require.NoError(t, err)
	generations := map[string]uint64{}
	for _, task := range tasks {
		generations[task.ID] = task.Generation
		if task.Running {
			assert.Equal(t, svc.Spec.TaskTemplate.ForceUpdate, task.Generation, "the rollback's task is the previous deploy's")
		}
	}
	started := map[uint64]bool{}
	for _, line := range lines {
		generation, ok := generations[line.Task]
		require.True(t, ok, "%q is from a task of the service", line.Text)
		if strings.Contains(line.Text, "Starting up") {
			started[generation] = true
		}
	}
	assert.GreaterOrEqual(t, len(started), 3, "deploys with a startup line: %v", started)

	// Following stops when the caller breaks out.
	followCtx, stopFollowing := context.WithTimeout(ctx, 30*time.Second)
	defer stopFollowing()
	follow, err := engine.StreamServiceLogs(followCtx, name, LogOptions{Follow: true, Tail: 1})
	require.NoError(t, err)
	for range follow {
		break
	}
	assert.NoError(t, followCtx.Err(), "breaking out returned without waiting for the timeout")

	// Cancelling ends a follow that's waiting for new lines, which is what
	// a browser closing the Logs tab does.
	cancelCtx, cancel := context.WithCancel(ctx)
	follow, err = engine.StreamServiceLogs(cancelCtx, name, LogOptions{Follow: true, Tail: 1})
	require.NoError(t, err)
	ended := make(chan struct{})
	go func() {
		for range follow {
		}
		close(ended)
	}()
	time.Sleep(time.Second)
	cancel()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling didn't end the Docker log stream")
	}

	// Echowa refuses to touch services it didn't create.
	unmanaged := name + "-unmanaged"
	zero := uint64(0)
	_, err = engine.api.ServiceCreate(ctx, client.ServiceCreateOptions{Spec: swarm.ServiceSpec{
		Annotations:  swarm.Annotations{Name: unmanaged},
		TaskTemplate: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{Image: newer}},
		Mode:         swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &zero}},
	}})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = engine.api.ServiceRemove(context.Background(), unmanaged, client.ServiceRemoveOptions{})
	})
	assert.ErrorIs(t, engine.RemoveService(ctx, unmanaged), ErrNotManaged)
	unmanagedSpec := spec
	unmanagedSpec.Name = unmanaged
	assert.ErrorIs(t, engine.DeployService(ctx, unmanagedSpec), ErrNotManaged)
	_, err = engine.Exec(ctx, unmanaged, []string{"true"})
	assert.ErrorIs(t, err, ErrNotManaged)

	// Missing services.
	_, err = engine.ServiceStatus(ctx, name+"-missing")
	assert.ErrorIs(t, err, ErrServiceNotFound)
	assert.NoError(t, engine.RemoveService(ctx, name+"-missing"))

	// Remove, then prune: the older image goes once its containers are
	// gone, and the kept digest stays.
	require.NoError(t, engine.RemoveService(ctx, name))
	_, err = engine.ServiceStatus(ctx, name)
	assert.ErrorIs(t, err, ErrServiceNotFound)

	opts := PruneOptions{Repositories: []string{whoami}, Keep: []string{newer}}
	require.Eventually(t, func() bool {
		if _, err := engine.PruneImages(ctx, opts); err != nil {
			t.Logf("prune: %v", err)
			return false
		}
		_, err := engine.api.ImageInspect(ctx, older)
		return cerrdefs.IsNotFound(err)
	}, 60*time.Second, 2*time.Second, "the older whoami image should be pruned once its containers are removed")
	_, err = engine.api.ImageInspect(ctx, newer)
	assert.NoError(t, err, "the kept digest survives pruning")
}

func TestEngineVolumeKeepsDataAcrossDeploys(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cfg := DefaultConfig()
	cfg.UpdateMonitor = 2 * time.Second
	cfg.DeployTimeout = time.Minute
	cfg.PollInterval = 500 * time.Millisecond
	engine, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = engine.Close() })
	require.NoError(t, engine.Check(ctx), "needs a local Swarm manager: run `docker swarm init` (deploy/README.md)")
	ensureNetwork(t, engine, cfg.Network)

	name := fmt.Sprintf("echowa-it-vol-%d", time.Now().UnixNano()%1_000_000)
	volume := name + "-data"
	t.Cleanup(func() {
		_ = engine.RemoveService(context.Background(), name)
		_, _ = engine.api.VolumeRemove(context.Background(), volume, client.VolumeRemoveOptions{Force: true})
	})

	image, err := engine.PullImage(ctx, busybox, nil)
	require.NoError(t, err)
	// Each task appends a line to the volume and prints how many there are.
	// The password comes from the env through sh, as a template's would.
	spec := ServiceSpec{
		Name:     name,
		App:      "it-vol",
		Image:    image,
		Env:      map[string]string{"SECRET": "s3cret"},
		Replicas: 1,
		Args:     []string{"sh", "-c", `echo run >> /data/runs; echo "runs=$(wc -l < /data/runs) secret=$SECRET"; exec sleep 3600`},
		Volumes:  []Volume{{Name: volume, Target: "/data"}},
	}
	for range 2 {
		require.NoError(t, engine.DeployService(ctx, spec))
		result, err := engine.WaitForUpdate(ctx, name)
		require.NoError(t, err)
		require.Equal(t, OutcomeCompleted, result.Outcome, "message: %s", result.Message)
		waitForRunning(t, engine, name, 1)
	}

	svc, err := engine.inspect(ctx, name)
	require.NoError(t, err)
	assert.Equal(t, swarm.UpdateOrderStopFirst, svc.Spec.UpdateConfig.Order)
	assert.Equal(t, spec.Args, svc.Spec.TaskTemplate.ContainerSpec.Args)

	vol, err := engine.api.VolumeInspect(ctx, volume, client.VolumeInspectOptions{})
	require.NoError(t, err)
	assert.Equal(t, "true", vol.Volume.Labels[LabelManaged], "Swarm created the volume with the mount's labels")
	assert.Equal(t, "it-vol", vol.Volume.Labels[LabelApp])

	require.Eventually(t, func() bool {
		logs, err := engine.StreamServiceLogs(ctx, name, LogOptions{})
		if err != nil {
			return false
		}
		var lines []LogLine
		for line, err := range logs {
			if err != nil {
				return false
			}
			lines = append(lines, line)
		}
		return containsLine(lines, "runs=2 secret=s3cret")
	}, 20*time.Second, 500*time.Millisecond, "the second task saw the first task's line, and the env through sh")

	assert.ErrorIs(t, engine.RemoveVolume(ctx, volume), ErrVolumeInUse)
	exists, err := engine.VolumeExists(ctx, volume)
	require.NoError(t, err)
	assert.True(t, exists)

	// Swarm removes the tasks after RemoveService returns.
	require.NoError(t, engine.RemoveService(ctx, name))
	require.Eventually(t, func() bool {
		err := engine.RemoveVolume(ctx, volume)
		if err != nil && !errors.Is(err, ErrVolumeInUse) {
			t.Logf("remove volume: %v", err)
		}
		return err == nil
	}, 60*time.Second, time.Second, "the volume is removable once the tasks are gone")
	_, err = engine.api.VolumeInspect(ctx, volume, client.VolumeInspectOptions{})
	assert.True(t, cerrdefs.IsNotFound(err), "the volume is gone: %v", err)
	assert.NoError(t, engine.RemoveVolume(ctx, volume), "removing a missing volume is not an error")
	exists, err = engine.VolumeExists(ctx, volume)
	require.NoError(t, err)
	assert.False(t, exists)

	// Echowa refuses to remove volumes it didn't create.
	unmanaged := name + "-unmanaged"
	_, err = engine.api.VolumeCreate(ctx, client.VolumeCreateOptions{Name: unmanaged})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = engine.api.VolumeRemove(context.Background(), unmanaged, client.VolumeRemoveOptions{}) })
	assert.ErrorIs(t, engine.RemoveVolume(ctx, unmanaged), ErrNotManaged)
}

func TestEngineHostPortSurvivesRedeploys(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cfg := DefaultConfig()
	cfg.UpdateMonitor = 2 * time.Second
	cfg.DeployTimeout = time.Minute
	cfg.PollInterval = 500 * time.Millisecond
	engine, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = engine.Close() })
	require.NoError(t, engine.Check(ctx), "needs a local Swarm manager: run `docker swarm init` (deploy/README.md)")
	ensureNetwork(t, engine, cfg.Network)

	name := fmt.Sprintf("echowa-it-port-%d", time.Now().UnixNano()%1_000_000)
	t.Cleanup(func() { _ = engine.RemoveService(context.Background(), name) })
	l, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())

	image, err := engine.PullImage(ctx, whoami+":v1.12.0", nil)
	require.NoError(t, err)
	spec := ServiceSpec{Name: name, App: "it-port", Image: image, Replicas: 1, HostPort: &HostPort{Published: port, Target: 80}}
	answers := func() bool {
		resp, err := nethttp.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return strings.Contains(string(body), "Hostname:")
	}

	// The second deploy only completes if the old task let go of the port.
	for range 2 {
		require.NoError(t, engine.DeployService(ctx, spec))
		result, err := engine.WaitForUpdate(ctx, name)
		require.NoError(t, err)
		require.Equal(t, OutcomeCompleted, result.Outcome, "message: %s", result.Message)
		assert.Eventually(t, answers, 20*time.Second, 250*time.Millisecond, "the app answers on host port %d", port)
	}

	spec.HostPort = nil
	require.NoError(t, engine.DeployService(ctx, spec))
	result, err := engine.WaitForUpdate(ctx, name)
	require.NoError(t, err)
	require.Equal(t, OutcomeCompleted, result.Outcome, "message: %s", result.Message)
	assert.Eventually(t, func() bool { return !answers() }, 20*time.Second, 250*time.Millisecond, "the port closes without a host port")
}

func TestEngineCheckReportsUnreachableDaemon(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Host = "unix:///nonexistent/docker.sock"
	engine, err := New(cfg)
	require.NoError(t, err)
	defer engine.Close()

	assert.ErrorIs(t, engine.Check(context.Background()), ErrDaemonUnreachable)
}

func ensureNetwork(t *testing.T, engine *Engine, network string) {
	t.Helper()
	ctx := context.Background()
	if _, err := engine.api.NetworkInspect(ctx, network, client.NetworkInspectOptions{}); err == nil {
		return
	}
	_, err := engine.api.NetworkCreate(ctx, network, client.NetworkCreateOptions{Driver: "overlay", Attachable: true})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = engine.api.NetworkRemove(context.Background(), network, client.NetworkRemoveOptions{}) })
}

// waitForRunning waits for the service's task counts to settle, since
// they lag the update status by a moment.
func waitForRunning(t *testing.T, engine *Engine, name string, replicas uint64) ServiceStatus {
	t.Helper()
	var status ServiceStatus
	require.Eventually(t, func() bool {
		var err error
		status, err = engine.ServiceStatus(context.Background(), name)
		return err == nil && status.Running == replicas && status.Desired == replicas
	}, 30*time.Second, 250*time.Millisecond, "service %s never reached %d running", name, replicas)

	return status
}

func containsLine(lines []LogLine, text string) bool {
	for _, line := range lines {
		if strings.Contains(line.Text, text) {
			return true
		}
	}

	return false
}

func TestEngineExecRunsInTheAppsContainer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cfg := DefaultConfig()
	cfg.UpdateMonitor = 2 * time.Second
	cfg.DeployTimeout = time.Minute
	cfg.PollInterval = 500 * time.Millisecond
	engine, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = engine.Close() })
	require.NoError(t, engine.Check(ctx), "needs a local Swarm manager: run `docker swarm init` (deploy/README.md)")
	ensureNetwork(t, engine, cfg.Network)

	app := fmt.Sprintf("it-exec-%d", time.Now().UnixNano()%1_000_000)
	name := "echowa-" + app
	t.Cleanup(func() { _ = engine.RemoveService(context.Background(), name) })
	_, err = engine.Exec(ctx, name, []string{"true"})
	assert.ErrorIs(t, err, ErrNotRunning)
	_, err = engine.ExecTo(ctx, name, []string{"true"}, io.Discard)
	assert.ErrorIs(t, err, ErrNotRunning)

	image, err := engine.PullImage(ctx, busybox, nil)
	require.NoError(t, err)
	spec := ServiceSpec{
		Name:     name,
		App:      app,
		Image:    image,
		Env:      map[string]string{"SECRET": "s3cret"},
		Replicas: 1,
		Args:     []string{"sleep", "3600"},
	}
	require.NoError(t, engine.DeployService(ctx, spec))
	result, err := engine.WaitForUpdate(ctx, name)
	require.NoError(t, err)
	require.Equal(t, OutcomeCompleted, result.Outcome, "message: %s", result.Message)
	waitForRunning(t, engine, name, 1)

	out, err := engine.Exec(ctx, name, []string{"sh", "-c", `echo "$1 $SECRET"; echo noise >&2`, "sh", "it's quoted"})
	require.NoError(t, err)
	assert.Equal(t, "it's quoted s3cret\n", string(out), "stdout only, with the container's env")

	_, err = engine.Exec(ctx, name, []string{"sh", "-c", "echo broken >&2; exit 3"})
	var exit *ExecError
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, 3, exit.ExitCode)
	assert.Equal(t, "broken", exit.Stderr)

	_, err = engine.Exec(ctx, name, []string{"head", "-c", fmt.Sprint(MaxExecOutput + 1), "/dev/zero"})
	assert.ErrorIs(t, err, ErrExecOutputTooLarge)

	short, cancelShort := context.WithTimeout(ctx, time.Second)
	defer cancelShort()
	start := time.Now()
	_, err = engine.Exec(short, name, []string{"sleep", "30"})
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 5*time.Second, "a cancelled context ends the exec's stream")

	var both strings.Builder
	code, err := engine.ExecTo(ctx, name, []string{"sh", "-c", `echo "out $SECRET"; sleep 0.2; echo err >&2; sleep 0.2; echo out2; exit 3`}, &both)
	require.NoError(t, err, "a status other than 0 isn't an error")
	assert.Equal(t, 3, code)
	assert.Equal(t, "out s3cret\nerr\nout2\n", both.String(), "both streams, in the order written, with the container's env")

	tail := &TailBuffer{Limit: 1000}
	code, err = engine.ExecTo(ctx, name, []string{"sh", "-c", fmt.Sprintf(`head -c %d /dev/zero | tr '\0' x; echo; echo end`, MaxExecOutput+1)}, tail)
	require.NoError(t, err, "output past Exec's cap isn't an error")
	assert.Equal(t, 0, code)
	assert.Regexp(t, `^\[\d+ earlier bytes dropped\]\nx+\nend\n$`, tail.String())

	short, cancelShort = context.WithTimeout(ctx, time.Second)
	defer cancelShort()
	start = time.Now()
	_, err = engine.ExecTo(short, name, []string{"sleep", "30"}, io.Discard)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 5*time.Second, "a cancelled context ends the exec's stream")
}

func TestEngineExecToWithoutAShell(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cfg := DefaultConfig()
	cfg.UpdateMonitor = 2 * time.Second
	cfg.DeployTimeout = time.Minute
	cfg.PollInterval = 500 * time.Millisecond
	engine, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = engine.Close() })
	require.NoError(t, engine.Check(ctx), "needs a local Swarm manager: run `docker swarm init` (deploy/README.md)")
	ensureNetwork(t, engine, cfg.Network)

	app := fmt.Sprintf("it-noshell-%d", time.Now().UnixNano()%1_000_000)
	name := "echowa-" + app
	t.Cleanup(func() { _ = engine.RemoveService(context.Background(), name) })
	image, err := engine.PullImage(ctx, whoami+":v1.12.0", nil)
	require.NoError(t, err)
	require.NoError(t, engine.DeployService(ctx, ServiceSpec{Name: name, App: app, Image: image, Replicas: 1}))
	result, err := engine.WaitForUpdate(ctx, name)
	require.NoError(t, err)
	require.Equal(t, OutcomeCompleted, result.Outcome, "message: %s", result.Message)
	waitForRunning(t, engine, name, 1)

	var out strings.Builder
	code, err := engine.ExecTo(ctx, name, []string{"sh", "-c", "true"}, &out)
	require.NoError(t, err, "a missing command is the run's failure, not Echowa's")
	assert.Equal(t, 127, code)
	assert.Contains(t, out.String(), `exec: "sh": executable file not found`, "Docker's reason reaches the output")
}

func TestEngineLimitsTasksAndReportsOOMKills(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cfg := DefaultConfig()
	cfg.UpdateMonitor = 2 * time.Second
	cfg.DeployTimeout = time.Minute
	cfg.PollInterval = 500 * time.Millisecond
	engine, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = engine.Close() })
	require.NoError(t, engine.Check(ctx), "needs a local Swarm manager: run `docker swarm init` (deploy/README.md)")
	ensureNetwork(t, engine, cfg.Network)

	host, err := engine.Host(ctx)
	require.NoError(t, err)
	assert.Positive(t, host.CPUs)
	assert.Positive(t, host.MemoryBytes)
	assert.NotEmpty(t, host.DataRoot)

	app := fmt.Sprintf("it-oom-%d", time.Now().UnixNano()%1_000_000)
	name := "echowa-" + app
	t.Cleanup(func() { _ = engine.RemoveService(context.Background(), name) })
	image, err := engine.PullImage(ctx, busybox, nil)
	require.NoError(t, err)
	limits := Limits{NanoCPUs: 500_000_000, MemoryBytes: 16 << 20}
	// tail buffers /dev/zero's endless line until the limit stops it, once
	// the deploy has counted the task as good.
	spec := ServiceSpec{Name: name, App: app, Image: image, Replicas: 1, Limits: limits,
		Args: []string{"sh", "-c", "sleep 6; exec tail /dev/zero"}}
	require.NoError(t, engine.DeployService(ctx, spec))
	result, err := engine.WaitForUpdate(ctx, name)
	require.NoError(t, err)
	require.Equal(t, OutcomeCompleted, result.Outcome, "message: %s", result.Message)

	status := waitForRunning(t, engine, name, 1)
	assert.Equal(t, limits, status.Limits)
	assert.Nil(t, status.OOMKilledAt, "nothing killed yet")

	var killed *time.Time
	require.Eventually(t, func() bool {
		status, err := engine.ServiceStatus(ctx, name)
		killed = status.OOMKilledAt
		return err == nil && killed != nil
	}, time.Minute, time.Second, "the task was never OOM-killed")
	assert.WithinDuration(t, time.Now(), *killed, time.Minute)
}

func TestEngineGivesATaskTheSwapItsSpecAllows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cfg := DefaultConfig()
	cfg.UpdateMonitor = 2 * time.Second
	cfg.DeployTimeout = time.Minute
	cfg.PollInterval = 500 * time.Millisecond
	engine, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = engine.Close() })
	require.NoError(t, engine.Check(ctx), "needs a local Swarm manager: run `docker swarm init` (deploy/README.md)")
	ensureNetwork(t, engine, cfg.Network)

	app := fmt.Sprintf("it-swap-%d", time.Now().UnixNano()%1_000_000)
	name := "echowa-" + app
	t.Cleanup(func() { _ = engine.RemoveService(context.Background(), name) })
	image, err := engine.PullImage(ctx, busybox, nil)
	require.NoError(t, err)
	limits := Limits{NanoCPUs: 500_000_000, MemoryBytes: 64 << 20, SwapBytes: 32 << 20}
	spec := ServiceSpec{Name: name, App: app, Image: image, Replicas: 1, Limits: limits, Args: []string{"sleep", "3600"}}
	require.NoError(t, engine.DeployService(ctx, spec))
	result, err := engine.WaitForUpdate(ctx, name)
	require.NoError(t, err)
	require.Equal(t, OutcomeCompleted, result.Outcome, "message: %s", result.Message)

	status := waitForRunning(t, engine, name, 1)
	assert.Equal(t, limits, status.Limits)

	svc, err := engine.inspect(ctx, name)
	require.NoError(t, err)
	tasks, err := engine.api.TaskList(ctx, client.TaskListOptions{Filters: make(client.Filters).Add("service", svc.ID).Add("desired-state", "running")})
	require.NoError(t, err)
	require.Len(t, tasks.Items, 1)
	container, err := engine.api.ContainerInspect(ctx, tasks.Items[0].Status.ContainerStatus.ContainerID, client.ContainerInspectOptions{})
	require.NoError(t, err)
	assert.EqualValues(t, 96<<20, container.Container.HostConfig.MemorySwap, "Docker's MemorySwap is memory and swap together")
}
