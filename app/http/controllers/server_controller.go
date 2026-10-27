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
		"appIds": func(c context.Context) (any, error) {
			return services.AppIDsByName(c)
		},
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
