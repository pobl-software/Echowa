package middleware

import (
	"context"
	"fmt"
	nethttp "net/http"

	"github.com/goravel/framework/contracts/http"
	gonertia "github.com/romsar/gonertia/v3"

	"goravel/app/facades"
	"goravel/app/inertia"
	"goravel/app/services"
)

// AuthUser is the signed-in user as every page sees it (props.auth.user).
// Keep it to what the UI needs: shared props are sent on every visit.
type AuthUser struct {
	ID    uint   `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type HandleInertiaRequests struct{}

func (m *HandleInertiaRequests) Signature() string {
	return "HandleInertiaRequests"
}

// Share defines the props that are available to every page by default.
func (m *HandleInertiaRequests) Share(ctx http.Context) gonertia.Props {
	user := m.authUser(ctx)
	props := gonertia.Props{
		"appName": facades.Config().GetString("app.name"),
		"auth": map[string]any{
			"user": user,
		},
		"dockerProblem":   nil,
		"serverAttention": false,
		"unreadAlarms":    0,
	}
	if user != nil {
		// A closure, so gonertia only asks Docker when a page renders, not
		// on redirects. Null when Docker is fine; the layout shows a banner
		// otherwise.
		props["dockerProblem"] = func(c context.Context) any {
			if problem := services.DockerProblem(c); problem != "" {
				return problem
			}
			return nil
		}
		// The rail's dot (ADR 0041). It reads files and the last apt run
		// only, so a page never waits for apt; polls don't ask for it.
		props["serverAttention"] = func(context.Context) any {
			return services.HostAttention()
		}
		// The rail's dot on Alarms.
		props["unreadAlarms"] = func(c context.Context) any {
			n, err := services.UnreadAlarms(c)
			if err != nil {
				facades.Log().Error(fmt.Errorf("unread alarms: %w", err))
			}
			return n
		}
	}

	return props
}

func (m *HandleInertiaRequests) authUser(ctx http.Context) *AuthUser {
	if ctx.Request().Session() == nil {
		return nil
	}

	user, err := services.CurrentUser(ctx)
	if err != nil || user == nil {
		return nil
	}

	return &AuthUser{ID: user.ID, Name: user.Name, Email: user.Email}
}

func (m *HandleInertiaRequests) Handle(ctx http.Context) {
	ctx.Response().Header("Vary", "X-Inertia")
	inertia.Share(ctx, m.Share(ctx))

	request := ctx.Request()
	if request.Header("X-Inertia") == "" {
		request.Next()
		return
	}

	// The assets changed since the client loaded the page (e.g. after a
	// deploy), so tell it to do a full page visit instead.
	if request.Method() == nethttp.MethodGet && request.Header("X-Inertia-Version") != inertia.Version() {
		ctx.Response().Header("X-Inertia-Location", request.FullUrl())
		request.Abort(nethttp.StatusConflict)
		return
	}

	request.Next()
}
