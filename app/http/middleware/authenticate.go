package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"goravel/app/facades"
	"goravel/app/inertia"
	"goravel/app/services"
)

// Authenticate sends guests to /login, remembering where they were going.
// Laravel's "auth" middleware.
type Authenticate struct{}

func (m *Authenticate) Signature() string {
	return "Authenticate"
}

func (m *Authenticate) Handle(ctx http.Context) {
	if facades.Auth(ctx).Check() {
		ctx.Request().Next()
		return
	}

	if ctx.Request().Method() == http.MethodGet {
		services.RememberIntendedURL(ctx)
	}
	inertia.Redirect(ctx, "/login").Abort()
}
