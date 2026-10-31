package controllers

import (
	"context"
	"time"

	"github.com/goravel/framework/contracts/http"
	gonertia "github.com/romsar/gonertia/v3"

	"goravel/app/inertia"
	"goravel/app/metrics"
	"goravel/app/services"
)

type ServerController struct{}

func NewServerController() *ServerController {
	return &ServerController{}
}

// Show is the Server page: the latest reading, live from memory, and the
// charts for ?range= from the metrics file (ADR 0024). The page polls
// every 15 seconds; each prop is worked out only when it's asked for.
// Maintenance comes after the page, so an apt run never holds it up, and
// the polls leave it out (ADR 0041).
func (r *ServerController) Show(ctx http.Context) http.Response {
	rng := metrics.ParseRange(ctx.Request().Query("range"))

	return inertia.Render(ctx, "Server", gonertia.Props{
		"ranges": metricRanges(),
		"series": func(c context.Context) (any, error) {
			return services.ServerMetrics(c, rng, time.Now())
		},
		"latest": func(c context.Context) (any, error) {
			return services.LatestMetrics(), nil
		},
		"server": func(c context.Context) (any, error) {
			return services.CurrentServerFacts(c), nil
		},
		"appIds": func(c context.Context) (any, error) {
			return services.AppIDsByName(c)
		},
		"maintenance": maintenance(),
	})
}

// maintenance is what the server needs doing, sent after the page. Each
// read is kept for the dashboard (services.LastMaintenance).
func maintenance() gonertia.DeferProp {
	return gonertia.Defer(func(c context.Context) (any, error) {
		return services.HostMaintenance(c), nil
	})
}

// metricRanges are the range picker's choices, shortest first.
func metricRanges() []string {
	ranges := make([]string, len(metrics.Ranges))
	for i, r := range metrics.Ranges {
		ranges[i] = r.Key
	}

	return ranges
}
