package controllers

import (
	"context"
	"time"

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
// beside the server's latest figures and facts.
func (r *DashboardController) Index(ctx http.Context) http.Response {
	apps, err := services.AppSummaries(ctx.Context())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}

	return inertia.Render(ctx, "Dashboard", gonertia.Props{
		"apps": apps,
		// The newest metrics reading, for the sidebar and each app's figures.
		"metrics": func(context.Context) (any, error) {
			return services.LatestMetrics(), nil
		},
		"server": func(c context.Context) (any, error) {
			return services.CurrentServerFacts(c), nil
		},
		// Each app's CPU over the last hour, for its sparkline; polled with metrics.
		"cpu": func(c context.Context) (any, error) {
			return services.RecentAppsCPU(c, time.Now())
		},
		"activity": func(c context.Context) (any, error) {
			return services.RecentActivity(c, 10)
		},
		// The sidebar shows the Server page's last read of what the server
		// needs, so the dashboard never reads the host itself (ADR 0041),
		// and its Refresh asks for a read of its own.
		"maintenance": func(context.Context) (any, error) {
			return services.LastMaintenance(), nil
		},
		"freshMaintenance": gonertia.Optional(func(c context.Context) (any, error) {
			return services.HostMaintenance(c), nil
		}),
	})
}
