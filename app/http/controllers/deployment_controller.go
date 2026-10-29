package controllers

import (
	"fmt"
	"strings"

	"github.com/goravel/framework/contracts/http"
	gonertia "github.com/romsar/gonertia/v3"

	"goravel/app/deploy"
	"goravel/app/docker"
	"goravel/app/inertia"
	"goravel/app/models"
	"goravel/app/services"
)

// DeploymentController shows one deployment and its log, and redeploys
// earlier versions.
type DeploymentController struct{}

func NewDeploymentController() *DeploymentController {
	return &DeploymentController{}
}

func (r *DeploymentController) Show(ctx http.Context) http.Response {
	app, deployment, resp := r.find(ctx)
	if resp != nil {
		return resp
	}

	return inertia.Render(ctx, "Deployments/Show", gonertia.Props{
		"app": map[string]any{
			"id":                    app.ID,
			"name":                  app.Name,
			"current_deployment_id": app.CurrentDeploymentID,
			// For the sections it has (resources/js/Components/AppNav.tsx).
			"template": app.Template,
			"backups":  services.AppBackup(&app) != nil,
		},
		"serviceName": app.ServiceName(),
		// Why a queued deployment hasn't started, when it's waiting for the
		// app's lock (ADR 0034).
		"waitingFor": waitingFor(&app, &deployment),
		// The log never holds secrets: the deploy job only writes env var
		// counts, never values, and never the registry token.
		"deployment": deployment,
	})
}

// Redeploy rolls the app back (or forward) to the exact image a succeeded
// deployment ran: it queues a deploy of that digest with trigger=rollback
// (docs/mvp.md, "Rollback"). The app's current settings and env apply.
func (r *DeploymentController) Redeploy(ctx http.Context) http.Response {
	app, deployment, resp := r.find(ctx)
	if resp != nil {
		return resp
	}
	if deployment.Status != models.DeploymentSucceeded || deployment.Digest == nil {
		inertia.Flash(ctx, "error", fmt.Sprintf("Deployment #%d didn't succeed, so there's no version to go back to.", deployment.ID))
		return inertia.Back(ctx)
	}
	// Deploys pull from the app's repository, so a version from a
	// repository the app no longer uses can't be redeployed.
	if !docker.SameRepository(*deployment.Digest, app.Repository) {
		inertia.Flash(ctx, "error", fmt.Sprintf("Deployment #%d ran %s, but the app now pulls from %s.", deployment.ID, *deployment.Digest, app.Repository))
		return inertia.Back(ctx)
	}

	_, digest, _ := strings.Cut(*deployment.Digest, "@")
	redeploy, err := deploy.Request(ctx, &app, models.TriggerRollback, digest)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", fmt.Sprintf("Deployment #%d queued: redeploying %s from #%d.", redeploy.ID, services.ShortDigest(digest), deployment.ID))

	return inertia.Redirect(ctx, fmt.Sprintf("/apps/%d/deployments/%d", app.ID, redeploy.ID))
}

// find loads the app and its deployment from the route, or a 404 when
// either is missing or the deployment belongs to another app.
func (r *DeploymentController) find(ctx http.Context) (models.App, models.Deployment, http.Response) {
	var app models.App
	var deployment models.Deployment
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return app, deployment, resp
	}
	if resp := findOr404(ctx, "deployment", &deployment); resp != nil {
		return app, deployment, resp
	}
	if deployment.AppID != app.ID {
		return app, deployment, notFound(ctx)
	}

	return app, deployment, nil
}

// waitingFor is what a queued deployment waits for, or "" when it's only
// waiting for a worker.
func waitingFor(app *models.App, d *models.Deployment) string {
	if d.Status != models.DeploymentQueued {
		return ""
	}
	if work := services.AppBusy(app.ID); work != "" && work != services.WorkDeploy {
		return fmt.Sprintf("Waiting for %s of this app to finish.", work.Describe())
	}

	return ""
}
