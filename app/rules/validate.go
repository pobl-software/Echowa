package rules

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/validation"

	"goravel/app/facades"
)

// Validate checks the request's input against set, as ctx.Request().Validate
// does, but with the request's own context. Goravel's passes its pooled
// http.Context, which the unique and exists rules query with after the next
// request has taken it over. tests/lint refuses the other one.
func Validate(ctx http.Context, set map[string]any, options ...validation.Option) (validation.Validator, error) {
	return facades.Validation().Make(ctx.Context(), ctx.Request().All(), set, options...)
}
