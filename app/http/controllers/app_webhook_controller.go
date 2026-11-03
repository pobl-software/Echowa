package controllers

import (
	"github.com/goravel/framework/contracts/http"

	"goravel/app/inertia"
	"goravel/app/models"
	"goravel/app/services"
)

// AppWebhookController is the admin's side of a custom app's deploy
// webhook (ADR 0047): its secret, revealed or rotated in the CI section.
type AppWebhookController struct{}

func NewAppWebhookController() *AppWebhookController {
	return &AppWebhookController{}
}

// Reveal returns the secret as plain JSON, never a page prop, like
// EnvReveal.
func (r *AppWebhookController) Reveal(ctx http.Context) http.Response {
	var app models.App
	if resp := findCustomApp(ctx, &app); resp != nil {
		return resp
	}

	return ctx.Response().Header("Cache-Control", "no-store").Json(http.StatusOK, http.Json{"secret": string(app.WebhookSecret)})
}

// Rotate replaces the secret. CI signing with the old one gets 401 until
// its ECHOWA_SECRET is updated.
func (r *AppWebhookController) Rotate(ctx http.Context) http.Response {
	var app models.App
	if resp := findCustomApp(ctx, &app); resp != nil {
		return resp
	}
	if err := services.RotateWebhookSecret(ctx.Context(), &app); err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", "New webhook secret for "+app.Name+". Update ECHOWA_SECRET in your CI.")

	return inertia.Back(ctx)
}
