package services

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/goravel/framework/contracts/http"
	"github.com/spf13/cast"

	"goravel/app/facades"
	"goravel/app/models"
)

// lastActiveKey is when the session last did something the admin asked
// for, as a Unix time (ADR 0043).
const lastActiveKey = "auth.last_active"

// SessionTimeouts are how long a session may go without activity before
// it signs out: the choices Settings, Security offers, shortest first.
// The first is the default. config/session.go's lifetime must cover the
// last, or Goravel forgets the session first.
var SessionTimeouts = []time.Duration{
	2 * time.Hour,
	8 * time.Hour,
	24 * time.Hour,
	7 * 24 * time.Hour,
	30 * 24 * time.Hour,
}

// SessionTimeout is how long user's sessions last without activity. A
// value that isn't one of the choices, from a newer release, is the
// default.
func SessionTimeout(user *models.User) time.Duration {
	if user.SessionTimeoutMinutes != nil {
		timeout := time.Duration(*user.SessionTimeoutMinutes) * time.Minute
		if slices.Contains(SessionTimeouts, timeout) {
			return timeout
		}
	}

	return SessionTimeouts[0]
}

// SetSessionTimeout saves user's choice, which must be one of
// SessionTimeouts (rules.SessionTimeout).
func SetSessionTimeout(ctx context.Context, user *models.User, timeout time.Duration) error {
	minutes := int(timeout / time.Minute)
	if _, err := facades.Orm().WithContext(ctx).Query().Model(user).Update("session_timeout_minutes", minutes); err != nil {
		return err
	}
	user.SessionTimeoutMinutes = &minutes

	return nil
}

// SessionIdle reports whether the signed-in session has gone longer than
// user's timeout without activity. One with no activity recorded is from
// before timeouts, when it would have expired after two hours, so it's
// idle too.
func SessionIdle(ctx http.Context, user *models.User, now time.Time) bool {
	last := cast.ToInt64(ctx.Request().Session().Get(lastActiveKey))

	return last == 0 || now.Unix()-last > int64(SessionTimeout(user)/time.Second)
}

// TouchSession records activity now.
func TouchSession(ctx http.Context, now time.Time) {
	ctx.Request().Session().Put(lastActiveKey, now.Unix())
}

// IsActivity reports whether the request is something the admin did, a
// visit or a change, rather than an open page refreshing itself. Polls
// are Inertia partial reloads or plain JSON reads, and prefetches are
// guesses, so none of them keep a session alive.
func IsActivity(ctx http.Context) bool {
	req := ctx.Request()
	if method := req.Method(); method != http.MethodGet && method != http.MethodHead {
		return true
	}
	if req.Header("X-Inertia-Partial-Data") != "" || req.Header("X-Inertia-Partial-Except") != "" ||
		req.Header("Purpose") == "prefetch" {
		return false
	}

	return req.Header("X-Inertia") != "" || !strings.Contains(req.Header("Accept"), "application/json")
}
