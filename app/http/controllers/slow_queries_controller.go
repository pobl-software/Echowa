package controllers

import (
	"github.com/goravel/framework/contracts/http"

	"goravel/app/inertia"
	"goravel/app/models"
	"goravel/app/services"
)

// SlowQueriesController switches a template database's slow query list on
// and off, and clears its totals (ADR 0027). Switching redeploys the
// running image, since the flag is part of the server's command.
type SlowQueriesController struct{}

func NewSlowQueriesController() *SlowQueriesController {
	return &SlowQueriesController{}
}

// Store switches the list on.
func (r *SlowQueriesController) Store(ctx http.Context) http.Response {
	return r.set(ctx, true, "Slow queries on")
}

// Destroy switches it off.
func (r *SlowQueriesController) Destroy(ctx http.Context) http.Response {
	return r.set(ctx, false, "Slow queries off")
}

func (r *SlowQueriesController) set(ctx http.Context, on bool, change string) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return resp
	}
	tmpl := appTemplate(&app)
	if tmpl == nil || !tmpl.HasSlowQueries(true) || tmpl.Inspect.SlowQueryFlags == "" {
		return notFound(ctx)
	}
	if app.SlowQueries == on {
		return inertia.Back(ctx)
	}

	if err := services.SetSlowQueries(ctx.Context(), &app, on); err != nil {
		return inertia.ServerError(ctx, err)
	}

	return redeployCurrent(ctx, &app, change)
}

// Reset clears the totals, so the list starts again from now.
func (r *SlowQueriesController) Reset(ctx http.Context) http.Response {
	var app models.App
	if resp := findOr404(ctx, "app", &app); resp != nil {
		return resp
	}

	if err := services.ResetSlowQueries(ctx.Context(), &app); err != nil {
		inertia.Flash(ctx, "error", err.Error())
		return inertia.Back(ctx)
	}
	inertia.Flash(ctx, "success", "Slow query totals cleared.")

	return inertia.Back(ctx)
}
