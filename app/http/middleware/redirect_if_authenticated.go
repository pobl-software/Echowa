package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"goravel/app/facades"
	"goravel/app/inertia"
)

// RedirectIfAuthenticated keeps signed-in users away from the login and
// register pages. Laravel's "guest" middleware.
type RedirectIfAuthenticated struct{}

func (m *RedirectIfAuthenticated) Signature() string {
	return "RedirectIfAuthenticated"
}

func (m *RedirectIfAuthenticated) Handle(ctx http.Context) {
	if facades.Auth(ctx).Check() {
		inertia.Redirect(ctx, "/dashboard").Abort()
		return
	}

	ctx.Request().Next()
}
