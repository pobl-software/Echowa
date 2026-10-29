package docker

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/distribution/reference"
	"github.com/moby/moby/api/pkg/authconfig"
	"github.com/moby/moby/api/types/registry"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
)

// Engine implements Client against a Docker daemon.
type Engine struct {
	api *client.Client
	cfg Config
}

var _ Client = (*Engine)(nil)

// New connects to cfg.Host, or to DOCKER_HOST and the default socket. The
// SDK negotiates the API version with the daemon on the first request. It
// doesn't contact the daemon yet: call Check for that.
func New(cfg Config) (*Engine, error) {
	opts := []client.Opt{client.FromEnv}
	if cfg.Host != "" {
		opts = append(opts, client.WithHost(cfg.Host))
	}
	api, err := client.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}

	return &Engine{api: api, cfg: cfg}, nil
}

// Close releases the client's connections.
func (e *Engine) Close() error {
	return e.api.Close()
}

func (e *Engine) Host(ctx context.Context) (Host, error) {
	info, err := e.api.Info(ctx, client.InfoOptions{})
	if err != nil {
		return Host{}, fmt.Errorf("%w: %v", ErrDaemonUnreachable, err)
	}

	return Host{DataRoot: info.Info.DockerRootDir, CPUs: info.Info.NCPU, MemoryBytes: info.Info.MemTotal}, nil
}

func (e *Engine) Check(ctx context.Context) error {
	if _, err := e.api.Ping(ctx, client.PingOptions{}); err != nil {
		return fmt.Errorf("%w: %v", ErrDaemonUnreachable, err)
	}
	info, err := e.api.Info(ctx, client.InfoOptions{})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDaemonUnreachable, err)
	}
	if state := info.Info.Swarm.LocalNodeState; state != swarm.LocalNodeStateActive {
		return fmt.Errorf("%w (node state %q)", ErrNotSwarm, state)
	}
	if !info.Info.Swarm.ControlAvailable {
		return ErrNotManager
	}

	return nil
}

func (e *Engine) PullImage(ctx context.Context, ref string, auth *RegistryAuth) (string, error) {
	named, err := normalizeRef(ref)
	if err != nil {
		return "", err
	}

	var encoded string
	if auth != nil {
		// The daemon sends the login to whichever registry the image is on.
		if auth.ServerAddress != "" && !SameRegistry(auth.ServerAddress, named.String()) {
			return "", fmt.Errorf("%s is on %s, but the registry login is for %s, so it wasn't sent", reference.FamiliarString(named), reference.Domain(named), auth.ServerAddress)
		}
		server := auth.ServerAddress
		if server == "" {
			server = reference.Domain(named)
		}
		encoded, err = authconfig.Encode(registry.AuthConfig{
			Username: auth.Username, Password: auth.Password, ServerAddress: server,
		})
		if err != nil {
			return "", fmt.Errorf("encode registry auth: %w", err)
		}
	}

	pull, err := e.api.ImagePull(ctx, named.String(), client.ImagePullOptions{RegistryAuth: encoded})
	if err != nil {
		return "", fmt.Errorf("pull %s: %w", reference.FamiliarString(named), err)
	}
	if err := pull.Wait(ctx); err != nil {
		return "", fmt.Errorf("pull %s: %w", reference.FamiliarString(named), err)
	}

	inspect, err := e.api.ImageInspect(ctx, named.String())
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", reference.FamiliarString(named), err)
	}

	return digestRef(named, inspect.RepoDigests)
}

func (e *Engine) RegistryLogin(ctx context.Context, auth RegistryAuth) error {
	_, err := e.api.RegistryLogin(ctx, client.RegistryLoginOptions{
		Username: auth.Username, Password: auth.Password, ServerAddress: auth.ServerAddress,
	})
	if err != nil {
		return fmt.Errorf("log in to %s: %w", auth.ServerAddress, err)
	}

	return nil
}

// DeployService doesn't send registry credentials: on a single node the
// image was just pulled by digest, so Swarm finds it locally, and nothing
// is stored in the Swarm.
func (e *Engine) DeployService(ctx context.Context, spec ServiceSpec) error {
	// Swarm bumps the service version whenever an update's status changes,
	// so an update can race with one in progress. Inspect and retry.
	for attempt := 1; ; attempt++ {
		current, err := e.inspect(ctx, spec.Name)
		if errors.Is(err, ErrServiceNotFound) {
			desired, err := swarmSpec(spec, e.cfg, 0)
			if err != nil {
				return err
			}
			if _, err := e.api.ServiceCreate(ctx, client.ServiceCreateOptions{Spec: desired}); err != nil {
				return fmt.Errorf("create service %s: %w", spec.Name, err)
			}
			return nil
		}
		if err != nil {
			return err
		}
		if !managed(current) {
			return fmt.Errorf("%s: %w", spec.Name, ErrNotManaged)
		}

		next := current.Spec.TaskTemplate.ForceUpdate
		if current.PreviousSpec != nil {
			next = max(next, current.PreviousSpec.TaskTemplate.ForceUpdate)
		}
		desired, err := swarmSpec(spec, e.cfg, next+1)
		if err != nil {
			return err
		}
		_, err = e.api.ServiceUpdate(ctx, current.ID, client.ServiceUpdateOptions{
			Version: current.Version,
			Spec:    desired,
		})
		if err == nil {
			return nil
		}
		if attempt < 3 && strings.Contains(err.Error(), "update out of sequence") {
			continue
		}
		return fmt.Errorf("update service %s: %w", spec.Name, err)
	}
}

func (e *Engine) ServiceStatus(ctx context.Context, name string) (ServiceStatus, error) {
	svc, err := e.inspect(ctx, name)
	if err != nil {
		return ServiceStatus{}, err
	}
	// Only the list endpoint reports task counts.
	list, err := e.api.ServiceList(ctx, client.ServiceListOptions{
		Filters: make(client.Filters).Add("id", svc.ID),
		Status:  true,
	})
	if err != nil {
		return ServiceStatus{}, fmt.Errorf("list service %s: %w", name, err)
	}

	status := ServiceStatus{Name: svc.Spec.Name}
	if svc.Spec.TaskTemplate.ContainerSpec != nil {
		status.Image = svc.Spec.TaskTemplate.ContainerSpec.Image
	}
	if svc.UpdateStatus != nil {
		status.UpdateState = UpdateState(svc.UpdateStatus.State)
		status.UpdateMessage = svc.UpdateStatus.Message
	}
	if r := svc.Spec.TaskTemplate.Resources; r != nil && r.Limits != nil {
		status.Limits = Limits{NanoCPUs: r.Limits.NanoCPUs, MemoryBytes: r.Limits.MemoryBytes}
		if r.SwapBytes != nil {
			status.Limits.SwapBytes = *r.SwapBytes
		}
	}
	for _, item := range list.Items {
		if item.ID == svc.ID && item.ServiceStatus != nil {
			status.Desired = item.ServiceStatus.DesiredTasks
			status.Running = item.ServiceStatus.RunningTasks
		}
	}
	status.OOMKilledAt, err = e.lastOOMKill(ctx, svc.ID)
	if err != nil {
		return ServiceStatus{}, fmt.Errorf("tasks of %s: %w", name, err)
	}

	return status, nil
}

// lastOOMKill is when the newest of the service's OOM-killed containers
// stopped. Only Docker's container state says why a task exited, so only
// tasks that exited with 137, SIGKILL's status, are inspected.
func (e *Engine) lastOOMKill(ctx context.Context, serviceID string) (*time.Time, error) {
	tasks, err := e.api.TaskList(ctx, client.TaskListOptions{Filters: make(client.Filters).Add("service", serviceID)})
	if err != nil {
		return nil, err
	}
	var last *time.Time
	for _, id := range killedContainers(tasks.Items) {
		result, err := e.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
		if cerrdefs.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		state := result.Container.State
		if state == nil || !state.OOMKilled {
			continue
		}
		finished, err := time.Parse(time.RFC3339Nano, state.FinishedAt)
		if err == nil && (last == nil || finished.After(*last)) {
			last = &finished
		}
	}

	return last, nil
}

func (e *Engine) WaitForUpdate(ctx context.Context, name string) (UpdateResult, error) {
	ctx, cancel := context.WithTimeout(ctx, e.cfg.DeployTimeout)
	defer cancel()
	ticker := time.NewTicker(e.cfg.PollInterval)
	defer ticker.Stop()

	var hint string
	for {
		done, result, latest, err := e.updateProgress(ctx, name)
		if err != nil && ctx.Err() == nil {
			return UpdateResult{}, err
		}
		if done {
			return result, nil
		}
		if latest != "" {
			hint = latest
		}

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				if hint != "" {
					return UpdateResult{}, fmt.Errorf("%w after %s (last task error: %s)", ErrTimeout, e.cfg.DeployTimeout, hint)
				}
				return UpdateResult{}, fmt.Errorf("%w after %s", ErrTimeout, e.cfg.DeployTimeout)
			}
			return UpdateResult{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (e *Engine) updateProgress(ctx context.Context, name string) (bool, UpdateResult, string, error) {
	svc, err := e.inspect(ctx, name)
	if err != nil {
		return false, UpdateResult{}, "", err
	}
	tasks, err := e.api.TaskList(ctx, client.TaskListOptions{Filters: make(client.Filters).Add("service", svc.ID)})
	if err != nil {
		return false, UpdateResult{}, "", fmt.Errorf("list tasks of %s: %w", name, err)
	}
	done, result, hint := updateProgress(svc, tasks.Items)

	return done, result, hint, nil
}

func (e *Engine) StreamServiceLogs(ctx context.Context, name string, opts LogOptions) (iter.Seq2[LogLine, error], error) {
	svc, err := e.inspect(ctx, name)
	if err != nil {
		return nil, err
	}

	// Details puts the task's ID on each line (parseLogLine).
	options := client.ServiceLogsOptions{ShowStdout: true, ShowStderr: true, Timestamps: true, Details: true, Follow: opts.Follow}
	if !opts.Since.IsZero() {
		options.Since = opts.Since.UTC().Format(time.RFC3339Nano)
	}
	if opts.Tail > 0 {
		options.Tail = strconv.Itoa(opts.Tail)
	}
	stream, err := e.api.ServiceLogs(ctx, svc.ID, options)
	if err != nil {
		return nil, fmt.Errorf("logs of %s: %w", name, err)
	}

	return readLogs(ctx, stream), nil
}

func (e *Engine) ServiceTasks(ctx context.Context, name string) ([]Task, error) {
	svc, err := e.inspect(ctx, name)
	if err != nil {
		return nil, err
	}
	list, err := e.api.TaskList(ctx, client.TaskListOptions{Filters: make(client.Filters).Add("service", svc.ID)})
	if err != nil {
		return nil, fmt.Errorf("tasks of %s: %w", name, err)
	}
	tasks := make([]Task, 0, len(list.Items))
	for _, t := range list.Items {
		task := Task{
			ID:         t.ID,
			Generation: t.Spec.ForceUpdate,
			CreatedAt:  t.CreatedAt,
			Running:    t.DesiredState == swarm.TaskStateRunning,
		}
		if t.Spec.ContainerSpec != nil {
			task.Image = t.Spec.ContainerSpec.Image
		}
		tasks = append(tasks, task)
	}

	return tasks, nil
}

func (e *Engine) RemoveService(ctx context.Context, name string) error {
	svc, err := e.inspect(ctx, name)
	if errors.Is(err, ErrServiceNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !managed(svc) {
		return fmt.Errorf("%s: %w", name, ErrNotManaged)
	}
	if _, err := e.api.ServiceRemove(ctx, svc.ID, client.ServiceRemoveOptions{}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove service %s: %w", name, err)
	}

	return nil
}

func (e *Engine) ServicesWithRole(ctx context.Context, role string) ([]ServiceRef, error) {
	if role == "" {
		return nil, errors.New("list services: no role given")
	}
	list, err := e.api.ServiceList(ctx, client.ServiceListOptions{
		Filters: make(client.Filters).Add("label", LabelManaged+"=true", LabelRole+"="+role),
	})
	if err != nil {
		return nil, fmt.Errorf("list %s services: %w", role, err)
	}
	refs := make([]ServiceRef, 0, len(list.Items))
	for _, svc := range list.Items {
		refs = append(refs, ServiceRef{Name: svc.Spec.Name, App: svc.Spec.Labels[LabelApp]})
	}

	return refs, nil
}

// RemoveVolume never forces: Docker refuses while a container uses the
// volume, which is the signal to wait for Swarm to finish removing tasks.
func (e *Engine) RemoveVolume(ctx context.Context, name string) error {
	result, err := e.api.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect volume %s: %w", name, err)
	}
	if result.Volume.Labels[LabelManaged] != "true" {
		return fmt.Errorf("volume %s: %w", name, ErrNotManaged)
	}
	_, err = e.api.VolumeRemove(ctx, name, client.VolumeRemoveOptions{})
	if cerrdefs.IsConflict(err) {
		return fmt.Errorf("volume %s: %w", name, ErrVolumeInUse)
	}
	if err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove volume %s: %w", name, err)
	}

	return nil
}

func (e *Engine) VolumeExists(ctx context.Context, name string) (bool, error) {
	_, err := e.api.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect volume %s: %w", name, err)
	}

	return true, nil
}

// PruneImages removes candidates one by one without force. An image that a
// container (even a stopped one) still uses makes Docker answer with a
// conflict, and is skipped.
func (e *Engine) PruneImages(ctx context.Context, opts PruneOptions) (PruneReport, error) {
	list, err := e.api.ImageList(ctx, client.ImageListOptions{})
	if err != nil {
		return PruneReport{}, fmt.Errorf("list images: %w", err)
	}

	var report PruneReport
	for _, img := range pruneCandidates(list.Items, opts, time.Now()) {
		_, err := e.api.ImageRemove(ctx, img.ID, client.ImageRemoveOptions{PruneChildren: true})
		if cerrdefs.IsConflict(err) || cerrdefs.IsNotFound(err) {
			continue
		}
		if err != nil {
			return report, fmt.Errorf("remove image %s: %w", imageLabel(img), err)
		}
		report.Removed = append(report.Removed, imageLabel(img))
		report.SpaceReclaimed += img.Size
	}

	return report, nil
}

func (e *Engine) inspect(ctx context.Context, name string) (swarm.Service, error) {
	result, err := e.api.ServiceInspect(ctx, name, client.ServiceInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return swarm.Service{}, fmt.Errorf("%s: %w", name, ErrServiceNotFound)
	}
	if err != nil {
		return swarm.Service{}, fmt.Errorf("inspect service %s: %w", name, err)
	}
	// The API falls back to matching an ID prefix. Only an exact name or ID
	// counts.
	if result.Service.Spec.Name != name && result.Service.ID != name {
		return swarm.Service{}, fmt.Errorf("%s: %w", name, ErrServiceNotFound)
	}

	return result.Service, nil
}

func managed(svc swarm.Service) bool {
	return svc.Spec.Labels[LabelManaged] == "true"
}
