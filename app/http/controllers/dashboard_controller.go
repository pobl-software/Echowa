package controllers

import (
	"context"

	"github.com/goravel/framework/contracts/http"
	gonertia "github.com/romsar/gonertia/v3"

	"goravel/app/inertia"
	"goravel/app/services"
)

type DashboardController struct{}

func NewDashboardController() *DashboardController {
	return &DashboardController{}
}

// Index lists every app with its live Swarm status and latest deployment,
// under the server's latest figures.
func (r *DashboardController) Index(ctx http.Context) http.Response {
	apps, err := services.AppSummaries(ctx)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}

	return inertia.Render(ctx, "Dashboard", gonertia.Props{
		"apps": apps,
		// The newest metrics reading, for the strip linking to /server.
		"metrics": func(context.Context) (any, error) {
			return services.LatestMetrics(), nil
		},
	})
}
