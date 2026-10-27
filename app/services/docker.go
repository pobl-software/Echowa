package services

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"goravel/app/docker"
	"goravel/app/facades"
)

// Docker returns the Docker client, built once from config/echowa.go.
// Tests replace it with one returning a *docker.Fake.
var Docker = sync.OnceValue(func() docker.Client {
	config := facades.Config()
	cfg := docker.DefaultConfig()
	cfg.Host = config.GetString("echowa.docker.host")
	cfg.Network = config.GetString("echowa.docker.network", cfg.Network)
	cfg.CertResolver = config.GetString("echowa.docker.cert_resolver", cfg.CertResolver)
	cfg.UpdateMonitor = configDuration("echowa.docker.update_monitor", cfg.UpdateMonitor)
	cfg.DeployTimeout = configDuration("echowa.docker.deploy_timeout", cfg.DeployTimeout)

	engine, err := docker.New(cfg)
	if err != nil {
		// Only a malformed ECHOWA_DOCKER_HOST or DOCKER_HOST gets here.
		panic(fmt.Errorf("docker client: %w", err))
	}

	return engine
})

func configDuration(key string, fallback time.Duration) time.Duration {
	d, err := time.ParseDuration(facades.Config().GetString(key))
	if err != nil || d <= 0 {
		return fallback
	}

	return d
}

// DockerProblem explains, for the page banner, why Echowa can't manage
// Docker right now. It's empty when Docker is a reachable Swarm manager.
func DockerProblem(ctx context.Context) string {
	err := Docker().Check(ctx)
	switch {
	case err == nil:
		return ""
	case errors.Is(err, docker.ErrNotSwarm):
		return "Docker isn't in Swarm mode, so apps can't be deployed. Run docker swarm init on the server (see deploy/README.md)."
	case errors.Is(err, docker.ErrNotManager):
		return "This Docker node isn't a Swarm manager, so Echowa can't manage services. Run Echowa on a manager node."
	default:
		return "Echowa can't reach the Docker daemon, so app status is unknown and deploys will fail. Is Docker running?"
	}
}
