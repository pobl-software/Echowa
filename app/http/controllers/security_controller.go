package controllers

import (
	"errors"

	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/support/carbon"
	gonertia "github.com/romsar/gonertia/v3"

	"goravel/app/inertia"
	"goravel/app/models"
	"goravel/app/rules"
	"goravel/app/services"
)

// SecurityController is Settings, Security: the admin's two-factor
// authentication (ADR 0042).
type SecurityController struct{}

func NewSecurityController() *SecurityController {
	return &SecurityController{}
}

func (r *SecurityController) Show(ctx http.Context) http.Response {
	user, resp := r.user(ctx)
	if resp != nil {
		return resp
	}

	return inertia.Render(ctx, "Settings/Security", gonertia.Props{"twoFactor": twoFactorProps(user)})
}

// user is the signed-in user. A session can outlive its user's row, and
// then it's sent to log in.
func (r *SecurityController) user(ctx http.Context) (*models.User, http.Response) {
	user, err := services.CurrentUser(ctx)
	if err != nil {
		return nil, inertia.ServerError(ctx, err)
	}
	if user == nil {
		return nil, inertia.Redirect(ctx, "/login")
	}

	return user, nil
}

// twoFactorProps is whether two-factor authentication is on, and since
// when. The secret is never a prop.
func twoFactorProps(user *models.User) map[string]any {
	if user == nil {
		return map[string]any{"enabled": false, "confirmed_at": nil}
	}
	return map[string]any{
		"enabled":      services.TwoFactorOn(user),
		"confirmed_at": user.TwoFactorConfirmedAt,
	}
}

// Setup gives the admin a new secret for their authenticator app, as
// plain JSON for the setup dialog.
func (r *SecurityController) Setup(ctx http.Context) http.Response {
	user, resp := r.user(ctx)
	if resp != nil {
		return resp
	}
	setup, err := services.StartTwoFactor(ctx.Context(), user)
	if errors.Is(err, services.ErrTwoFactorOn) {
		return ctx.Response().Json(http.StatusConflict, http.Json{"message": "Two-factor authentication is already on."})
	}
	if err != nil {
		return inertia.ServerError(ctx, err)
	}

	return ctx.Response().Header("Cache-Control", "no-store").Json(http.StatusOK, setup)
}

// Confirm turns two-factor authentication on with a code from the app.
// Rate limited by the "two-factor" limiter.
func (r *SecurityController) Confirm(ctx http.Context) http.Response {
	user, resp := r.userWithCode(ctx)
	if resp != nil {
		return resp
	}
	ok, err := services.ConfirmTwoFactor(ctx.Context(), user, ctx.Request().Input("code"), carbon.Now().StdTime())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if !ok {
		inertia.WithErrors(ctx, map[string]string{"code": rules.WrongTwoFactorCode})
		return inertia.Back(ctx)
	}

	inertia.Flash(ctx, "success", "Two-factor authentication is on. You'll need a code each time you log in.")
	return inertia.Redirect(ctx, "/settings/security")
}

// Destroy turns two-factor authentication off. It takes a code, so a
// browser left signed in isn't enough. Rate limited by the "two-factor"
// limiter.
func (r *SecurityController) Destroy(ctx http.Context) http.Response {
	user, resp := r.userWithCode(ctx)
	if resp != nil {
		return resp
	}
	ok, err := services.CheckTwoFactorCode(ctx.Context(), user, ctx.Request().Input("code"), carbon.Now().StdTime())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if !ok {
		inertia.WithErrors(ctx, map[string]string{"code": rules.WrongTwoFactorCode})
		return inertia.Back(ctx)
	}
	if err := services.TurnOffTwoFactor(ctx.Context(), user); err != nil {
		return inertia.ServerError(ctx, err)
	}

	inertia.Flash(ctx, "success", "Two-factor authentication is off.")
	return inertia.Redirect(ctx, "/settings/security")
}

// userWithCode is the signed-in user, once the form's code passes
// validation.
func (r *SecurityController) userWithCode(ctx http.Context) (*models.User, http.Response) {
	validator, err := rules.Validate(ctx, rules.TwoFactorCode(), rules.TwoFactorMessages())
	if err != nil {
		return nil, inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return nil, inertia.Back(ctx)
	}
	return r.user(ctx)
}
