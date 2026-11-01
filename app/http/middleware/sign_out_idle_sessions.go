package middleware

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/support/carbon"

	"goravel/app/facades"
	"goravel/app/inertia"
	"goravel/app/services"
)

// SignOutIdleSessions ends a session that has gone longer than the admin's
// session timeout without activity, and records activity on one that
// hasn't (ADR 0043). It runs before VerifyCsrfToken, so an idle form post
// is sent to log in rather than refused as expired.
type SignOutIdleSessions struct{}

func (m *SignOutIdleSessions) Signature() string {
	return "SignOutIdleSessions"
}

func (m *SignOutIdleSessions) Handle(ctx http.Context) {
	if !facades.Auth(ctx).Check() {
		ctx.Request().Next()
		return
	}
	user, err := services.CurrentUser(ctx)
	if err != nil {
		facades.Log().Error(err)
		ctx.Response().String(http.StatusInternalServerError, "Session unavailable").Abort()
		return
	}
	if user == nil {
		ctx.Request().Next()
		return
	}

	now := carbon.Now().StdTime()
	activity := services.IsActivity(ctx)
	if !services.SessionIdle(ctx, user, now) {
		if activity {
			services.TouchSession(ctx, now)
		}
		ctx.Request().Next()
		return
	}

	if err := facades.Auth(ctx).Logout(); err != nil {
		facades.Log().Error(err)
		ctx.Response().String(http.StatusInternalServerError, "Session unavailable").Abort()
		return
	}
	// A poll's URL isn't somewhere to go back to.
	if activity && ctx.Request().Method() == http.MethodGet {
		services.RememberIntendedURL(ctx)
	}
	inertia.Flash(ctx, "info", "You were signed out after a while with no activity. Log in again.")
	inertia.Redirect(ctx, "/login").Abort()
}
