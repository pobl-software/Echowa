package inertia

import (
	nethttp "net/http"
	"net/url"
	"strings"

	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/validation"
	gonertia "github.com/romsar/gonertia/v3"
)

// Session keys holding data for the next request. Values are stored with
// Session().Flash, so Goravel drops them after that request, as Laravel does.
const (
	errorsKey = "_inertia_errors"
	flashKey  = "_inertia_flash"
)

// WithErrors stores validation errors for the next page render, where they
// become props.errors (what useForm reads). Call it before a redirect.
func WithErrors(ctx http.Context, errors map[string]string) {
	session := ctx.Request().Session()
	if session == nil || len(errors) == 0 {
		return
	}

	merged := map[string]any{}
	if pending, ok := session.Get(errorsKey).(map[string]any); ok {
		for key, val := range pending {
			merged[key] = val
		}
	}
	for key, val := range errors {
		merged[key] = val
	}
	session.Flash(errorsKey, merged)
}

// WithValidationErrors stores a failed validator's errors, keeping the first
// message per field, which is the shape Inertia expects.
func WithValidationErrors(ctx http.Context, errors validation.Errors) {
	if errors == nil {
		return
	}
	WithErrors(ctx, ValidationMessages(errors))
}

// ValidationMessages is each field's first message, the shape useForm's
// errors have. Plain JSON endpoints answer with it too.
func ValidationMessages(errors validation.Errors) map[string]string {
	messages := map[string]string{}
	for field := range errors.All() {
		messages[field] = errors.One(field)
	}

	return messages
}

// Flash stores a value for the next page render, where it appears on
// usePage().flash and fires Inertia's "flash" event. AppLayout shows the
// success, error, info and warning keys as toasts.
func Flash(ctx http.Context, key string, value any) {
	session := ctx.Request().Session()
	if session == nil {
		return
	}

	merged := map[string]any{}
	if pending, ok := session.Get(flashKey).(map[string]any); ok {
		for k, v := range pending {
			merged[k] = v
		}
	}
	merged[key] = value
	session.Flash(flashKey, merged)
}

// Back redirects to the previous page (the Referer), or "/" when there isn't
// a same-origin one. Use it after a failed validation.
func Back(ctx http.Context) http.AbortableResponse {
	return Redirect(ctx, BackURL(ctx))
}

// BackURL is the Referer's path and query when it's same-origin, else "/".
func BackURL(ctx http.Context) string {
	referer := ctx.Request().Header("Referer")
	if referer == "" {
		return "/"
	}

	u, err := url.Parse(referer)
	if err != nil || (u.Host != "" && u.Host != ctx.Request().Host()) {
		return "/"
	}
	// "//evil.example" or "/\evil.example" would leave the site.
	if !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") || strings.HasPrefix(u.Path, "/\\") {
		return "/"
	}
	if u.RawQuery != "" {
		return u.Path + "?" + u.RawQuery
	}

	return u.Path
}

// withSessionData moves errors and flash data from the session onto the
// request context, where gonertia puts them on the page object.
func withSessionData(ctx http.Context, r *nethttp.Request) *nethttp.Request {
	session := ctx.Request().Session()
	if session == nil {
		return r
	}

	rctx := r.Context()
	if errors, ok := session.Get(errorsKey).(map[string]any); ok && len(errors) > 0 {
		rctx = gonertia.SetValidationErrors(rctx, gonertia.ValidationErrors(errors))
	}
	if flash, ok := session.Get(flashKey).(map[string]any); ok && len(flash) > 0 {
		rctx = gonertia.SetFlash(rctx, gonertia.Flash(flash))
	}

	return r.WithContext(rctx)
}
