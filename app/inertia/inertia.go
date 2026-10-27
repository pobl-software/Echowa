// Package inertia glues gonertia (https://github.com/romsar/gonertia) into
// Goravel, giving controllers a Laravel-style inertia.Render helper.
package inertia

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io/fs"
	nethttp "net/http"
	"sync"

	"github.com/goravel/framework/contracts/http"
	gonertia "github.com/romsar/gonertia/v3"

	"goravel/app/assets"
	"goravel/app/facades"
)

// entryPoint is the manifest key / dev-server path, not a file we open.
const entryPoint = "resources/js/app.tsx"

type sharedPropsKey struct{}

var (
	instance *gonertia.Inertia
	version  string
	bootErr  error
	bootOnce sync.Once
)

// boot builds the gonertia instance on first use, so tests and artisan
// commands that never render a page don't need the root template on disk.
func boot() (*gonertia.Inertia, error) {
	bootOnce.Do(func() {
		var opts []gonertia.Option
		if version = manifestVersion(); version != "" {
			// Not WithVersion: that one md5s whatever string it's given, so the
			// page's version wouldn't match ours.
			opts = append(opts, gonertia.WithVersionFromFileFS(assets.FS(), assets.Manifest))
		}
		instance, bootErr = gonertia.NewFromFileFS(assets.FS(), assets.RootTemplate, opts...)
	})

	return instance, bootErr
}

// Version is the current asset version. Inertia's client compares it on every
// visit and does a full page reload when it changes after a deploy.
func Version() string {
	_, _ = boot()

	return version
}

// Share makes props available to every page rendered during this request,
// like HandleInertiaRequests::share() in Laravel.
func Share(ctx http.Context, props gonertia.Props) {
	shared, _ := ctx.Value(sharedPropsKey{}).(gonertia.Props)
	if shared == nil {
		shared = gonertia.Props{}
	}
	for key, val := range props {
		shared[key] = val
	}
	ctx.WithValue(sharedPropsKey{}, shared)
}

// Render responds with the given page component, as a full HTML document on a
// first visit or as JSON when the Inertia client is navigating.
func Render(ctx http.Context, component string, props ...gonertia.Props) http.Response {
	i, err := boot()
	if err != nil {
		return renderError(ctx, err)
	}

	r := withSessionData(ctx, ctx.Request().Origin())
	rctx := r.Context()

	if shared, ok := ctx.Value(sharedPropsKey{}).(gonertia.Props); ok {
		rctx = gonertia.SetProps(rctx, shared)
	}

	if !gonertia.IsInertiaRequest(r) {
		tags, err := viteTags(entryPoint)
		if err != nil {
			return renderError(ctx, err)
		}
		rctx = gonertia.SetTemplateDatum(rctx, "vite", tags)
	}

	if err := i.Render(ctx.Response().Writer(), r.WithContext(rctx), component, props...); err != nil {
		return renderError(ctx, err)
	}

	// gonertia has already written the response.
	return nil
}

// Redirect sends the Inertia client to another page. PUT, PATCH and DELETE
// requests get a 303 so the browser follows up with a GET. In middleware,
// call .Abort() on the result to stop the chain.
func Redirect(ctx http.Context, url string) http.AbortableResponse {
	status := nethttp.StatusFound
	switch ctx.Request().Method() {
	case nethttp.MethodPut, nethttp.MethodPatch, nethttp.MethodDelete:
		status = nethttp.StatusSeeOther
	}

	return ctx.Response().Redirect(status, url)
}

// ServerError logs err and responds with a plain 500. Use it for unexpected
// failures (database down, mail server unreachable), not validation.
func ServerError(ctx http.Context, err error) http.Response {
	return renderError(ctx, err)
}

func renderError(ctx http.Context, err error) http.Response {
	facades.Log().Error(fmt.Errorf("inertia: %w", err))

	return ctx.Response().String(nethttp.StatusInternalServerError, "Internal Server Error")
}

func manifestVersion() string {
	content, err := fs.ReadFile(assets.FS(), assets.Manifest)
	if err != nil {
		return ""
	}
	sum := md5.Sum(content)

	return hex.EncodeToString(sum[:])
}
