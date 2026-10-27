package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"goravel/app/facades"
)

// AuthenticateStream is the session and sign-in check for Server-Sent
// Events routes, which sit outside the web group (routes/streams.go).
//
// It reads the session to find the signed-in user but never saves it. A
// stream stays open as long as its page does, and the web group's
// StartSession saves the session when the request ends: a stream closing
// would write back its stale copy over whatever other requests changed in
// the meantime, such as a flash message or a logout.
//
// Guests get a 401, which makes the browser's EventSource stop instead of
// reconnecting.
type AuthenticateStream struct{}

func (m *AuthenticateStream) Signature() string {
	return "AuthenticateStream"
}

func (m *AuthenticateStream) Handle(ctx http.Context) {
	driver, err := facades.Session().Driver()
	if err != nil {
		facades.Log().Error(err)
		ctx.Response().String(http.StatusInternalServerError, "Session unavailable").Abort()
		return
	}
	sess, err := facades.Session().BuildSession(driver)
	if err != nil {
		facades.Log().Error(err)
		ctx.Response().String(http.StatusInternalServerError, "Session unavailable").Abort()
		return
	}
	defer facades.Session().ReleaseSession(sess)

	sess.SetID(ctx.Request().Cookie(sess.GetName()))
	sess.Start()
	ctx.Request().SetSession(sess)

	if !facades.Auth(ctx).Check() {
		ctx.Response().String(http.StatusUnauthorized, "Sign in to follow this stream").Abort()
		return
	}

	ctx.Request().Next()
}
