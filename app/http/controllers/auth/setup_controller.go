package auth

import (
	"errors"
	"strings"

	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/validation"
	gonertia "github.com/romsar/gonertia/v3"

	"goravel/app/inertia"
	"goravel/app/services"
)

// setupTokenKey holds the setup link's token in the session, so the link's
// token leaves the address bar after the first page load.
const setupTokenKey = "setup.token"

// SetupController creates the admin on the first run (ADR 0023).
type SetupController struct{}

func NewSetupController() *SetupController {
	return &SetupController{}
}

// Create shows the setup form, if the browser came from the setup link.
func (r *SetupController) Create(ctx http.Context) http.Response {
	needed, err := services.NeedsSetup()
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if !needed {
		return inertia.Redirect(ctx, "/login")
	}
	session := ctx.Request().Session()
	if token := ctx.Request().Query("token"); token != "" {
		if services.ValidSetupToken(token) {
			session.Put(setupTokenKey, token)
		}
		return inertia.Redirect(ctx, "/setup")
	}
	token, _ := session.Get(setupTokenKey, "").(string)

	return inertia.Render(ctx, "Auth/Setup", gonertia.Props{"ready": services.ValidSetupToken(token)})
}

// Store creates the admin and signs them in. Rate limited by the "login"
// limiter.
func (r *SetupController) Store(ctx http.Context) http.Response {
	validator, err := ctx.Request().Validate(map[string]any{
		"name":     "required|string|max:255",
		"email":    "required|string|email",
		"password": "required|string",
	}, validation.Filters(map[string]any{"name": "trim", "email": "trim|lower"}))
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}
	password := ctx.Request().Input("password")
	if err := services.ValidatePassword(password); err != nil {
		message := err.Error()
		inertia.WithErrors(ctx, map[string]string{"password": strings.ToUpper(message[:1]) + message[1:] + "."})
		return inertia.Back(ctx)
	}

	session := ctx.Request().Session()
	token, _ := session.Get(setupTokenKey, "").(string)
	user, err := services.CreateFirstUser(token, ctx.Request().Input("name"), ctx.Request().Input("email"), password)
	switch {
	case errors.Is(err, services.ErrSetupDone):
		return inertia.Redirect(ctx, "/login")
	case errors.Is(err, services.ErrBadSetupToken):
		return inertia.Redirect(ctx, "/setup")
	case err != nil:
		return inertia.ServerError(ctx, err)
	}
	session.Forget(setupTokenKey)
	if err := services.Login(ctx, user, false); err != nil {
		return inertia.ServerError(ctx, err)
	}

	return inertia.Redirect(ctx, "/dashboard")
}
