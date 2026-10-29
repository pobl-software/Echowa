package controllers

import (
	"fmt"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/inertia"
	"goravel/app/models"
	"goravel/app/services"
)

// PoolingController turns a PostgreSQL app's connection pooler on and off
// (ADR 0035). The pooler starts or goes in the background, and the
// Connection tab shows how that went.
type PoolingController struct{}

func NewPoolingController() *PoolingController {
	return &PoolingController{}
}

// Store turns pooling on.
func (r *PoolingController) Store(ctx http.Context) http.Response {
	return r.set(ctx, true)
}

// Destroy turns it off.
func (r *PoolingController) Destroy(ctx http.Context) http.Response {
	return r.set(ctx, false)
}

func (r *PoolingController) set(ctx http.Context, on bool) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return resp
	}
	if !services.HasPooler(&app) {
		return notFound(ctx)
	}
	if on {
		taken, err := services.PoolerNameTaken(ctx, &app)
		if err != nil {
			return inertia.ServerError(ctx, err)
		}
		if taken {
			inertia.Flash(ctx, "error", fmt.Sprintf("The app %s%s has the service name the pooler needs, %s. Delete that app to turn pooling on.",
				app.Name, models.PoolerSuffix, app.PoolerServiceName()))
			return inertia.Back(ctx)
		}
	}

	if err := services.SetPooling(ctx, &app, on); err != nil {
		return inertia.ServerError(ctx, err)
	}
	if on {
		inertia.Flash(ctx, "success", "Starting the connection pooler.")
	} else {
		inertia.Flash(ctx, "success", "Removing the connection pooler.")
	}

	return inertia.Redirect(ctx, fmt.Sprintf("/apps/%d?tab=connection", app.ID))
}
