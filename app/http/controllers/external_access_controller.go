package controllers

import (
	"fmt"
	nethttp "net/http"
	"strings"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/deploy"
	"goravel/app/facades"
	"goravel/app/inertia"
	"goravel/app/models"
	"goravel/app/rules"
	"goravel/app/services"
	"goravel/app/templates"
)

// ExternalAccessController publishes a template database on a host port, so
// clients outside the server can reach it, with TLS on (ADR 0026). Turning
// it on or off redeploys the running image, since the port is part of the
// Swarm service.
type ExternalAccessController struct{}

func NewExternalAccessController() *ExternalAccessController {
	return &ExternalAccessController{}
}

// Store turns external access on, or moves it to another port.
func (r *ExternalAccessController) Store(ctx http.Context) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return resp
	}
	tmpl := appTemplate(&app)
	if tmpl == nil || tmpl.HTTP {
		return failed(ctx, map[string]string{"external_port": "Only databases made from a template can be reached from outside the server."})
	}

	validator, err := ctx.Request().Validate(rules.ExternalAccess(app.ID), rules.ExternalAccessMessages())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}
	port := ctx.Request().InputInt("external_port")
	// Scanners try the default ports first.
	if port == tmpl.Port || port == app.Port {
		return failed(ctx, map[string]string{"external_port": fmt.Sprintf(
			"Pick a port other than %d, the one %s uses by default. Bots scan default ports first.", port, tmpl.Name)})
	}
	if (app.ExternalPort == nil || *app.ExternalPort != port) && services.HostPortInUse(port) {
		return failed(ctx, map[string]string{"external_port": fmt.Sprintf("Something on this server already listens on port %d.", port)})
	}

	if err := services.EnableExternalAccess(ctx.Context(), &app, port, tmpl); err != nil {
		return inertia.ServerError(ctx, err)
	}

	return redeployCurrent(ctx, &app, fmt.Sprintf("External access on port %d", port))
}

// Destroy turns external access off.
func (r *ExternalAccessController) Destroy(ctx http.Context) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return resp
	}
	if app.ExternalPort == nil {
		return inertia.Redirect(ctx, fmt.Sprintf("/apps/%d?tab=connection", app.ID))
	}

	if err := services.SetExternalPort(ctx.Context(), &app, nil); err != nil {
		return inertia.ServerError(ctx, err)
	}

	return redeployCurrent(ctx, &app, "External access off")
}

// CA downloads the certificate authority the app's server certificate
// comes from, for clients to verify it with. It isn't secret.
func (r *ExternalAccessController) CA(ctx http.Context) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return resp
	}
	env, err := services.EnvVars(ctx.Context(), app.ID)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	ca := env[templates.EnvTLSCA]
	if ca == "" {
		return notFound(ctx)
	}

	return ctx.Response().
		Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-ca.crt"`, app.Name)).
		Data(nethttp.StatusOK, "application/x-pem-file", []byte(ca))
}

// redeployCurrent queues a deploy of the image the app runs now, so a
// settings change doesn't also pull a newer tag. A stopped app keeps the
// change for when it's started, since a deploy would start it.
func redeployCurrent(ctx http.Context, app *models.App, change string) http.Response {
	switch services.AppLiveStatus(ctx.Context(), app).State {
	case services.StateStopped, services.StateStopping:
		inertia.Flash(ctx, "success", fmt.Sprintf("%s. It applies when you start %s.", change, app.Name))
		return inertia.Back(ctx)
	}

	deployment, err := requestCurrent(ctx, app)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", fmt.Sprintf("%s. Deployment #%d applies it.", change, deployment.ID))

	return inertia.Redirect(ctx, fmt.Sprintf("/apps/%d/deployments/%d", app.ID, deployment.ID))
}

// requestCurrent queues a deploy of the image the app runs now. Before the
// first successful deploy, it deploys the default tag.
func requestCurrent(ctx http.Context, app *models.App) (*models.Deployment, error) {
	ref := app.DefaultTag
	if app.CurrentDeploymentID != nil {
		var current models.Deployment
		err := facades.Orm().WithContext(ctx.Context()).Query().Where("id", *app.CurrentDeploymentID).First(&current)
		if err != nil {
			return nil, err
		}
		if current.Digest != nil {
			_, ref, _ = strings.Cut(*current.Digest, "@")
		}
	}

	return deploy.Request(ctx.Context(), app, models.TriggerManual, ref)
}
