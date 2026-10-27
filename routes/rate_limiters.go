package routes

import (
	"strings"

	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/http/limit"

	"goravel/app/facades"
	"goravel/app/inertia"
	"goravel/app/services"
)

// RateLimiters registers the limiters used with middleware.Throttle(name).
// Called from bootstrap/app.go.
func RateLimiters() {
	// Per email and IP, so one attacker can't lock the admin out from
	// elsewhere, and per IP across emails, so one address can't spray
	// guesses. The IP is the real client even behind cloudflared
	// (services.ClientIP).
	facades.RateLimiter().ForWithLimits("login", func(ctx http.Context) []http.Limit {
		ip := services.ClientIP(ctx)
		return []http.Limit{
			limit.PerMinute(5).By("email|" + strings.ToLower(ctx.Request().Input("email")) + "|" + ip).Response(tooManyAttempts("email")),
			limit.PerMinute(20).By("ip|" + ip).Response(tooManyAttempts("email")),
		}
	})
}

// tooManyAttempts sends the user back to the form with an error on field,
// instead of a bare 429 that Inertia would show as an error modal.
func tooManyAttempts(field string) func(ctx http.Context) {
	return func(ctx http.Context) {
		inertia.WithErrors(ctx, map[string]string{
			field: "Too many attempts. Please wait a minute and try again.",
		})
		_ = inertia.Back(ctx).Abort()
	}
}
