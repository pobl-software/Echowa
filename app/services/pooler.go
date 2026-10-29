package services

import (
	"errors"

	"goravel/app/docker"
	"goravel/app/models"
	"goravel/app/templates"
)

// ErrNoPooler means the app's template has no connection pooler.
var ErrNoPooler = errors.New("this app has no connection pooler")

// PoolerSpec is the Swarm service that runs the app's connection pooler,
// from env, the app's env vars. Only the vars the pooler reads go into it.
func PoolerSpec(app *models.App, env map[string]string) (docker.ServiceSpec, error) {
	if app.Template == nil {
		return docker.ServiceSpec{}, ErrNoPooler
	}
	tmpl, ok := templates.Find(*app.Template)
	if !ok || tmpl.Pooler == nil {
		return docker.ServiceSpec{}, ErrNoPooler
	}
	pooler := tmpl.Pooler
	poolerEnv, err := pooler.EnvFrom(app.ServiceName(), env)
	if err != nil {
		return docker.ServiceSpec{}, err
	}

	return docker.ServiceSpec{
		Name:        app.PoolerServiceName(),
		App:         app.Name,
		Role:        docker.RolePooler,
		Image:       pooler.Image,
		Env:         poolerEnv,
		Replicas:    1,
		Entrypoint:  pooler.Command,
		Healthcheck: &docker.Healthcheck{Command: pooler.Healthcheck},
		Limits:      pooler.Limits,
	}, nil
}
