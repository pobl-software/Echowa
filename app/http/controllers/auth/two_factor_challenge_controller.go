package auth

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/support/carbon"
	gonertia "github.com/romsar/gonertia/v3"

	"goravel/app/inertia"
	"goravel/app/rules"
	"goravel/app/services"
)

// TwoFactorChallengeController is login's second step: the code from the
// admin's authenticator app, after the right password (ADR 0042).
type TwoFactorChallengeController struct{}

func NewTwoFactorChallengeController() *TwoFactorChallengeController {
	return &TwoFactorChallengeController{}
}

// Create asks for the code, or sends a browser that hasn't given the
// password, or took too long, back to log in.
func (r *TwoFactorChallengeController) Create(ctx http.Context) http.Response {
	user, _, err := services.PendingTwoFactorLogin(ctx)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if user == nil {
		return inertia.Redirect(ctx, "/login")
	}

	return inertia.Render(ctx, "Auth/TwoFactorChallenge", gonertia.Props{})
}

// Store signs the user in if the code is right. Rate limited by the
// "two-factor" limiter.
func (r *TwoFactorChallengeController) Store(ctx http.Context) http.Response {
	user, remember, err := services.PendingTwoFactorLogin(ctx)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if user == nil {
		inertia.Flash(ctx, "info", "That took too long. Log in again.")
		return inertia.Redirect(ctx, "/login")
	}

	validator, err := rules.Validate(ctx, rules.TwoFactorCode(), rules.TwoFactorMessages())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}
	ok, err := services.CheckTwoFactorCode(ctx.Context(), user, ctx.Request().Input("code"), carbon.Now().StdTime())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if !ok {
		inertia.WithErrors(ctx, map[string]string{"code": rules.WrongTwoFactorCode})
		return inertia.Back(ctx)
	}

	if err := services.FinishTwoFactorLogin(ctx, user, remember); err != nil {
		return inertia.ServerError(ctx, err)
	}

	return inertia.Redirect(ctx, services.IntendedURL(ctx, "/dashboard"))
}
