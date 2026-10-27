package middleware

import (
	"crypto/subtle"
	nethttp "net/http"

	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/support/carbon"

	"goravel/app/facades"
	"goravel/app/inertia"
)

// XsrfCookie is read by Inertia's HTTP client, which sends it back as the
// X-XSRF-TOKEN header on every request, as with Laravel.
const XsrfCookie = "XSRF-TOKEN"

// VerifyCsrfToken rejects state-changing requests whose token doesn't match
// the session's. It replaces Goravel's middleware.VerifyCsrfToken so that it
// can accept Inertia's X-XSRF-TOKEN header and turn a mismatch (usually an
// expired session) into a toast instead of a bare 419. Needs StartSession
// before it. See ADR 0008.
type VerifyCsrfToken struct{}

func (m *VerifyCsrfToken) Signature() string {
	return "VerifyCsrfToken"
}

func (m *VerifyCsrfToken) Handle(ctx http.Context) {
	request := ctx.Request()
	session := request.Session()
	if session == nil {
		facades.Log().Error("VerifyCsrfToken: no session; add StartSession before it")
		request.Abort(nethttp.StatusInternalServerError)
		return
	}

	if isReading(request.Method()) || tokensMatch(ctx, session.Token()) {
		// Set before Next: Inertia's Render writes the body itself, after
		// which headers can't change. Login rotates the token, but it always
		// redirects, and the follow-up GET carries the new cookie.
		writeXsrfCookie(ctx, session.Token())
		request.Next()
		return
	}

	if request.Header("X-Inertia") != "" {
		inertia.Flash(ctx, "error", "This page expired. Please try again.")
		writeXsrfCookie(ctx, session.Token())
		ctx.Response().Redirect(nethttp.StatusSeeOther, inertia.BackURL(ctx)).Abort()
		return
	}

	request.Abort(http.StatusTokenMismatch)
}

func isReading(method string) bool {
	return method == nethttp.MethodGet || method == nethttp.MethodHead || method == nethttp.MethodOptions
}

func tokensMatch(ctx http.Context, sessionToken string) bool {
	request := ctx.Request()
	token := request.Header("X-CSRF-TOKEN")
	if token == "" {
		token = request.Header("X-XSRF-TOKEN")
	}
	if token == "" {
		token = request.Input("_token")
	}

	return token != "" && sessionToken != "" &&
		subtle.ConstantTimeCompare([]byte(token), []byte(sessionToken)) == 1
}

func writeXsrfCookie(ctx http.Context, token string) {
	config := facades.Config()
	ctx.Response().Cookie(http.Cookie{
		Name:     XsrfCookie,
		Value:    token,
		Expires:  carbon.Now().AddMinutes(config.GetInt("session.lifetime", 120)).StdTime(),
		Path:     config.GetString("session.path", "/"),
		Domain:   config.GetString("session.domain"),
		Secure:   config.GetBool("session.secure"),
		HttpOnly: false, // the client has to read it
		SameSite: config.GetString("session.same_site", "lax"),
	})
}
