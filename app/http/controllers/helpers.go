package controllers

import (
	nethttp "net/http"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/facades"
	"goravel/app/inertia"
)

// findOr404 loads the model whose ID is in the route parameter param. When
// there's no such row it returns a 404 response to send instead.
func findOr404(ctx http.Context, param string, model any, preload ...string) http.Response {
	id := ctx.Request().RouteInt(param)
	if id <= 0 {
		return notFound(ctx)
	}

	query := facades.Orm().WithContext(ctx).Query()
	for _, relation := range preload {
		query = query.With(relation)
	}
	if err := query.Where("id", id).FirstOrFail(model); err != nil {
		return notFound(ctx)
	}

	return nil
}

func notFound(ctx http.Context) http.Response {
	return ctx.Response().String(nethttp.StatusNotFound, "404 page not found")
}

// failed sends the user back to the form with field errors, the way
// Inertia's useForm expects.
func failed(ctx http.Context, errors map[string]string) http.Response {
	inertia.WithErrors(ctx, errors)

	return inertia.Back(ctx)
}
