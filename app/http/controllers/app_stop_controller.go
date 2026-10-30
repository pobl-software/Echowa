package controllers

import (
	"errors"
	"fmt"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/docker"
	"goravel/app/inertia"
	"goravel/app/models"
	"goravel/app/services"
)

// AppStopController stops an app, so it's unavailable for a while without
// being deleted, and starts it again. A stopped app is a service Swarm
// runs 0 replicas of, with nothing stored in Echowa's database.
type AppStopController struct{}

func NewAppStopController() *AppStopController {
	return &AppStopController{}
}

// Stop scales the app to 0 replicas. Swarm ends its tasks within their
// grace period and removes them, with their logs.
func (r *AppStopController) Stop(ctx http.Context) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return resp
	}

	err := services.StopApp(ctx.Context(), &app)
	var active *services.DeploymentActive
	var held *services.AppHeld
	switch {
	case errors.As(err, &active):
		inertia.Flash(ctx, "error", fmt.Sprintf("Deployment #%d of %s is running. Stop it once that has finished.", active.ID, app.Name))
	case errors.As(err, &held):
		inertia.Flash(ctx, "error", fmt.Sprintf("%s of %s is running. Stop it once that has finished.", capitalise(held.Holder.Describe()), app.Name))
	case errors.Is(err, docker.ErrServiceNotFound):
		inertia.Flash(ctx, "error", app.Name+" isn't deployed.")
	case err != nil:
		return inertia.ServerError(ctx, err)
	default:
		inertia.Flash(ctx, "success", "Stopping "+app.Name+".")
	}

	return inertia.Back(ctx)
}

// Start deploys the image the app ran, which starts its replicas again.
func (r *AppStopController) Start(ctx http.Context) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return resp
	}

	switch services.AppLiveStatus(ctx.Context(), &app).State {
	case services.StateStopped:
	case services.StateStopping:
		inertia.Flash(ctx, "error", app.Name+" is still stopping.")
		return inertia.Back(ctx)
	default:
		inertia.Flash(ctx, "error", app.Name+" isn't stopped.")
		return inertia.Back(ctx)
	}
	active, err := services.ActiveDeployment(ctx.Context(), &app)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if active != nil {
		inertia.Flash(ctx, "error", fmt.Sprintf("Deployment #%d of %s is running. Start it once that has finished.", active.ID, app.Name))
		return inertia.Back(ctx)
	}

	deployment, err := requestCurrent(ctx, &app)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", fmt.Sprintf("Starting %s. Deployment #%d deploys the image it ran.", app.Name, deployment.ID))

	return inertia.Back(ctx)
}
