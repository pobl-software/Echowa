package auth

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/validation"
	gonertia "github.com/romsar/gonertia/v3"

	"goravel/app/inertia"
	"goravel/app/services"
)

// AuthenticatedSessionController signs users in and out.
type AuthenticatedSessionController struct{}

func NewAuthenticatedSessionController() *AuthenticatedSessionController {
	return &AuthenticatedSessionController{}
}

// Create shows the login form, or sends the first visitor to set up the
// admin.
func (r *AuthenticatedSessionController) Create(ctx http.Context) http.Response {
	if needed, err := services.NeedsSetup(); err != nil {
		return inertia.ServerError(ctx, err)
	} else if needed {
		return inertia.Redirect(ctx, "/setup")
	}
	return inertia.Render(ctx, "Auth/Login", gonertia.Props{})
}

// Store signs the user in. Rate limited by the "login" limiter.
func (r *AuthenticatedSessionController) Store(ctx http.Context) http.Response {
	validator, err := ctx.Request().Validate(map[string]any{
		"email":    "required|string|email",
		"password": "required|string",
	}, validation.Filters(map[string]any{"email": "trim|lower"}))
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}

	user, err := services.Attempt(ctx, ctx.Request().Input("email"), ctx.Request().Input("password"))
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if user == nil {
		inertia.WithErrors(ctx, map[string]string{"email": "These credentials do not match our records."})
		return inertia.Back(ctx)
	}

	if err := services.Login(ctx, user, ctx.Request().InputBool("remember")); err != nil {
		return inertia.ServerError(ctx, err)
	}

	return inertia.Redirect(ctx, services.IntendedURL(ctx, "/dashboard"))
}

// Destroy signs the user out.
func (r *AuthenticatedSessionController) Destroy(ctx http.Context) http.Response {
	if err := services.Logout(ctx); err != nil {
		return inertia.ServerError(ctx, err)
	}

	return inertia.Redirect(ctx, "/")
}
