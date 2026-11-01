package routes

import (
	"fmt"
	"strings"

	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/http/limit"
	"github.com/spf13/cast"

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

	// A code has a million values, so guesses are counted per user as
	// well as per IP: spreading them over many addresses doesn't help.
	// The user is the one signed in, or the one whose password was right.
	// The hourly cap keeps a month of guessing, with the password, at a
	// few percent.
	facades.RateLimiter().ForWithLimits("two-factor", func(ctx http.Context) []http.Limit {
		ip := services.ClientIP(ctx)
		user := services.PendingTwoFactorUserID(ctx)
		if id, err := facades.Auth(ctx).ID(); err == nil {
			user = cast.ToUint(id)
		}
		return []http.Limit{
			limit.PerMinute(5).By(fmt.Sprintf("two-factor|user|%d", user)).Response(tooManyAttempts("code")),
			limit.PerHour(20).By(fmt.Sprintf("two-factor|user-hour|%d", user)).Response(tooManyAttempts("code")),
			limit.PerMinute(10).By("two-factor|ip|" + ip).Response(tooManyAttempts("code")),
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
