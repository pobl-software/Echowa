package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"goravel/app/services"
)

// AuthenticateFromRememberCookie signs a guest in from a valid "remember me"
// cookie. It runs before VerifyCsrfToken because signing in rotates the CSRF
// token, which then has to be checked against and sent in the new cookie.
type AuthenticateFromRememberCookie struct{}

func (m *AuthenticateFromRememberCookie) Signature() string {
	return "AuthenticateFromRememberCookie"
}

func (m *AuthenticateFromRememberCookie) Handle(ctx http.Context) {
	services.LoginFromRememberCookie(ctx)
	ctx.Request().Next()
}
