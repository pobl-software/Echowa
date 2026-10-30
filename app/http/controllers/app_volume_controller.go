package controllers

import (
	"fmt"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/inertia"
	"goravel/app/models"
	"goravel/app/rules"
	"goravel/app/services"
	"goravel/app/templates"
)

// AppVolumeController is a custom app's Volumes section: the paths whose
// files survive a redeploy (ADR 0039). Template apps have theirs from the
// template and answer 404.
type AppVolumeController struct{}

func NewAppVolumeController() *AppVolumeController {
	return &AppVolumeController{}
}

// Store adds a volume at a path and deploys the image the app runs now, so
// the volume is mounted at once. The app drops to one replica, since a
// volume has one writer.
func (r *AppVolumeController) Store(ctx http.Context) http.Response {
	var app models.App
	if resp := r.findApp(ctx, &app); resp != nil {
		return resp
	}

	validator, err := rules.Validate(ctx, rules.AppVolume(), rules.AppVolumeFilters(), rules.AppVolumeMessages())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}
	var in struct {
		Name   string `form:"name" json:"name"`
		Target string `form:"target" json:"target"`
	}
	if err := validator.Bind(&in); err != nil {
		return inertia.ServerError(ctx, err)
	}

	dockerName := templates.VolumeName(app.Name, in.Name)
	if clash := services.VolumeClash(app.Volumes, dockerName, in.Target, 0); clash != nil {
		return failed(ctx, clash)
	}
	leftover, err := services.LeftoverVolume(ctx.Context(), []string{dockerName})
	if err != nil {
		return failed(ctx, map[string]string{"name": "Couldn't check Docker for this volume: " + err.Error()})
	}
	if leftover != "" {
		return failed(ctx, map[string]string{"name": fmt.Sprintf(
			"The volume %s already exists in Docker, left from an earlier app or volume, and may hold its data. Pick another name, or remove it on the server with docker volume rm %s.",
			leftover, leftover)})
	}

	if _, err := services.AddVolume(ctx.Context(), &app, in.Name, in.Target); err != nil {
		return inertia.ServerError(ctx, err)
	}

	return r.apply(ctx, &app, "Added a volume at "+in.Target)
}

// Update moves a volume to another path. Its data moves with it, and its
// name stays, since that's the Docker volume.
func (r *AppVolumeController) Update(ctx http.Context) http.Response {
	var app models.App
	var volume models.AppVolume
	if resp := r.findVolume(ctx, &app, &volume); resp != nil {
		return resp
	}
	if volume.Removing() {
		return failed(ctx, map[string]string{"target": "This volume is being removed, so it can't move."})
	}

	validator, err := rules.Validate(ctx, rules.AppVolumeTarget(), rules.AppVolumeFilters(), rules.AppVolumeMessages())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}
	var in struct {
		Target string `form:"target" json:"target"`
	}
	if err := validator.Bind(&in); err != nil {
		return inertia.ServerError(ctx, err)
	}
	if clash := services.VolumeClash(app.Volumes, "", in.Target, volume.ID); clash != nil {
		return failed(ctx, clash)
	}

	if err := services.MoveVolume(ctx.Context(), &volume, in.Target); err != nil {
		return inertia.ServerError(ctx, err)
	}

	return r.apply(ctx, &app, "Moved the volume to "+in.Target)
}

// Destroy marks a volume removed. The deploy it queues leaves the volume
// out, and once that succeeds it deletes the Docker volume and its data
// (services.RemoveDetached).
func (r *AppVolumeController) Destroy(ctx http.Context) http.Response {
	var app models.App
	var volume models.AppVolume
	if resp := r.findVolume(ctx, &app, &volume); resp != nil {
		return resp
	}
	if volume.Removing() {
		inertia.Flash(ctx, "info", fmt.Sprintf("The volume at %s is already being removed.", volume.Target))
		return inertia.Redirect(ctx, volumesURL(&app))
	}

	if err := services.MarkVolumeRemoved(ctx.Context(), &volume); err != nil {
		return inertia.ServerError(ctx, err)
	}

	return r.apply(ctx, &app, "Removed the volume at "+volume.Target)
}

// apply puts a change into effect: a deploy of the running image, as for
// resource limits, since an admin who adds a volume expects their data
// kept from now on. An app that was never deployed has nothing to
// redeploy, so the change waits for its first deploy.
func (r *AppVolumeController) apply(ctx http.Context, app *models.App, change string) http.Response {
	if app.CurrentDeploymentID != nil {
		return redeployCurrent(ctx, app, change)
	}
	inertia.Flash(ctx, "success", change+". It applies on the first deploy.")

	return inertia.Redirect(ctx, volumesURL(app))
}

func volumesURL(app *models.App) string {
	return fmt.Sprintf("/apps/%d?tab=volumes", app.ID)
}

// findApp finds the custom app whose Volumes section this is, with its
// volumes.
func (r *AppVolumeController) findApp(ctx http.Context, app *models.App) http.Response {
	if resp := findOr404(ctx, "app", app, "Volumes"); resp != nil {
		return resp
	}
	if app.Template != nil {
		return notFound(ctx)
	}

	return nil
}

// findVolume also finds the volume, which must be the app's own.
func (r *AppVolumeController) findVolume(ctx http.Context, app *models.App, volume *models.AppVolume) http.Response {
	if resp := r.findApp(ctx, app); resp != nil {
		return resp
	}
	id := uint(ctx.Request().RouteInt("volume"))
	for _, v := range app.Volumes {
		if v.ID == id {
			*volume = v
			return nil
		}
	}

	return notFound(ctx)
}
