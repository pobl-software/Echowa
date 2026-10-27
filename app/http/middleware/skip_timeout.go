package middleware

import (
	"slices"

	"github.com/goravel/framework/contracts/http"
)

// SkipTimeout wraps Goravel's global request Timeout so that requests to
// Paths (route patterns as written in routes/, e.g.
// "/apps/{app}/logs/stream") run without it. The timeout comes from
// gin-contrib/timeout: it buffers the whole response and cancels the
// request after http.request_timeout, so it would hold back every
// Server-Sent Event and then cut the stream off.
type SkipTimeout struct {
	Timeout http.Middleware
	Paths   []string
}

func (m *SkipTimeout) Signature() string {
	return m.Timeout.Signature()
}

func (m *SkipTimeout) Handle(ctx http.Context) {
	if slices.Contains(m.Paths, ctx.Request().OriginPath()) {
		ctx.Request().Next()
		return
	}
	m.Timeout.Handle(ctx)
}
