package docker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"slices"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"
)

// Config holds the settings shared by every service Echowa deploys.
type Config struct {
	// Host is the Docker API endpoint. Empty uses DOCKER_HOST or the
	// platform default socket.
	Host string
	// Network is the overlay network Traefik and the apps share.
	Network string
	// CertResolver is the Traefik resolver for TLS routes.
	CertResolver string
	// UpdateMonitor is how long Swarm watches a new task for failure before
	// it counts the update as good.
	UpdateMonitor time.Duration
	// DeployTimeout caps WaitForUpdate.
	DeployTimeout time.Duration
	// PollInterval is how often WaitForUpdate checks the service.
	PollInterval time.Duration
}

// DefaultConfig matches app/setup/files/traefik.yml and docs/mvp.md.
func DefaultConfig() Config {
	return Config{
		Network:       "echowa-public",
		CertResolver:  "le",
		UpdateMonitor: 30 * time.Second,
		DeployTimeout: 10 * time.Minute,
		PollInterval:  time.Second,
	}
}

// swarmSpec translates spec into the Swarm service spec. forceUpdate is the
// TaskTemplate.ForceUpdate counter: bumping it makes Swarm roll the tasks
// even when nothing else changed, and lets WaitForUpdate tell this deploy's
// tasks apart from the previous one's.
func swarmSpec(spec ServiceSpec, cfg Config, forceUpdate uint64) (swarm.ServiceSpec, error) {
	if spec.Name == "" || spec.App == "" || spec.Image == "" {
		return swarm.ServiceSpec{}, fmt.Errorf("service spec: name, app and image are required")
	}

	labels := map[string]string{LabelManaged: "true", LabelApp: spec.App}
	containerLabels := map[string]string{LabelApp: spec.App}
	if spec.Role != "" {
		labels[LabelRole] = spec.Role
		containerLabels[LabelRole] = spec.Role
	}
	if spec.Route != nil {
		traefik, err := TraefikLabels(spec.Name, *spec.Route, cfg.CertResolver)
		if err != nil {
			return swarm.ServiceSpec{}, err
		}
		maps.Copy(labels, traefik)
	}

	mounts, err := volumeMounts(spec)
	if err != nil {
		return swarm.ServiceSpec{}, err
	}
	endpoint, err := endpointSpec(spec)
	if err != nil {
		return swarm.ServiceSpec{}, err
	}
	// Starting the new task first would put two writers on one volume, or
	// two listeners on one host port.
	order := swarm.UpdateOrderStartFirst
	if len(mounts) > 0 || spec.HostPort != nil {
		order = swarm.UpdateOrderStopFirst
	}

	replicas := spec.Replicas
	update := &swarm.UpdateConfig{
		Parallelism:   1,
		Order:         order,
		FailureAction: swarm.UpdateFailureActionRollback,
		Monitor:       cfg.UpdateMonitor,
	}
	rollback := &swarm.UpdateConfig{
		Parallelism:   1,
		Order:         order,
		FailureAction: swarm.UpdateFailureActionPause,
		Monitor:       cfg.UpdateMonitor,
	}

	desired := swarm.ServiceSpec{
		Annotations: swarm.Annotations{Name: spec.Name, Labels: labels},
		TaskTemplate: swarm.TaskSpec{
			ContainerSpec: &swarm.ContainerSpec{
				Image:       spec.Image,
				Labels:      containerLabels,
				Command:     spec.Entrypoint,
				Args:        spec.Args,
				Env:         envList(spec.Env),
				Healthcheck: healthConfig(spec.Healthcheck),
				Mounts:      mounts,
			},
			Networks:    []swarm.NetworkAttachmentConfig{{Target: cfg.Network}},
			Resources:   resources(spec.Limits),
			ForceUpdate: forceUpdate,
		},
		Mode:           swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &replicas}},
		EndpointSpec:   endpoint,
		UpdateConfig:   update,
		RollbackConfig: rollback,
	}
	hash, err := specHash(desired)
	if err != nil {
		return swarm.ServiceSpec{}, err
	}
	labels[LabelSpec] = hash

	return desired, nil
}

// specHash identifies what spec deploys, leaving out the ForceUpdate
// counter that every deploy bumps.
func specHash(spec swarm.ServiceSpec) (string, error) {
	spec.TaskTemplate.ForceUpdate = 0
	encoded, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("service spec: %w", err)
	}
	sum := sha256.Sum256(encoded)

	return hex.EncodeToString(sum[:]), nil
}

// endpointSpec names the VIP mode and publishes the host port, if any.
// Swarm defaults to a VIP anyway, but Traefik's Swarm provider skips
// services whose spec doesn't name the mode, and lbswarm needs the VIP.
func endpointSpec(spec ServiceSpec) (*swarm.EndpointSpec, error) {
	endpoint := &swarm.EndpointSpec{Mode: swarm.ResolutionModeVIP}
	if spec.HostPort == nil {
		return endpoint, nil
	}
	p := *spec.HostPort
	if p.Published < 1 || p.Published > 65535 || p.Target < 1 || p.Target > 65535 {
		return nil, fmt.Errorf("service spec: host port %d for container port %d is out of range", p.Published, p.Target)
	}
	if spec.Replicas > 1 {
		return nil, fmt.Errorf("service spec: %s publishes host port %d, so it can't run %d replicas", spec.Name, p.Published, spec.Replicas)
	}
	endpoint.Ports = []swarm.PortConfig{{
		Protocol:      network.TCP,
		TargetPort:    uint32(p.Target),
		PublishedPort: uint32(p.Published),
		PublishMode:   swarm.PortConfigPublishModeHost,
	}}

	return endpoint, nil
}

// volumeMounts labels each volume as Echowa's, so Swarm creates it with the
// labels RemoveVolume checks.
func volumeMounts(spec ServiceSpec) ([]mount.Mount, error) {
	if len(spec.Volumes) > 0 && spec.Replicas > 1 {
		return nil, fmt.Errorf("service spec: %s has volumes, so it can't run %d replicas", spec.Name, spec.Replicas)
	}
	var mounts []mount.Mount
	for _, v := range spec.Volumes {
		if v.Name == "" || !path.IsAbs(v.Target) {
			return nil, fmt.Errorf("service spec: volume %q needs a name and an absolute target, not %q", v.Name, v.Target)
		}
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeVolume,
			Source: v.Name,
			Target: v.Target,
			VolumeOptions: &mount.VolumeOptions{
				Labels: map[string]string{LabelManaged: "true", LabelApp: spec.App},
			},
		})
	}

	return mounts, nil
}

// maxProcesses caps the processes and threads in each task, which share
// the host's process table: without it, a fork bomb inside the memory limit
// stops everything on the server, Echowa and sshd included, from starting
// one (ADR 0030).
const maxProcesses = 4096

// resources caps the task. With a memory limit, swap is what the spec
// allows and no more, 0 by default: Docker's own default would add as much
// again as the limit.
func resources(limits Limits) *swarm.ResourceRequirements {
	r := &swarm.ResourceRequirements{
		Limits: &swarm.Limit{NanoCPUs: limits.NanoCPUs, MemoryBytes: limits.MemoryBytes, Pids: maxProcesses},
	}
	if limits.MemoryBytes > 0 {
		swap := limits.SwapBytes
		r.SwapBytes = &swap
	}

	return r
}

// envList is sorted, so the same env always gives the same spec.
func envList(env map[string]string) []string {
	list := make([]string, 0, len(env))
	for _, key := range slices.Sorted(maps.Keys(env)) {
		list = append(list, key+"="+env[key])
	}

	return list
}

func healthConfig(h *Healthcheck) *container.HealthConfig {
	if h == nil || h.Command == "" {
		return nil
	}
	config := &container.HealthConfig{
		Test:        []string{"CMD-SHELL", h.Command},
		Interval:    h.Interval,
		Timeout:     h.Timeout,
		StartPeriod: h.StartPeriod,
		Retries:     h.Retries,
	}
	if config.Interval == 0 {
		config.Interval = 10 * time.Second
	}
	if config.Timeout == 0 {
		config.Timeout = 5 * time.Second
	}
	if config.StartPeriod == 0 {
		config.StartPeriod = 30 * time.Second
	}
	if config.Retries == 0 {
		config.Retries = 3
	}

	return config
}
