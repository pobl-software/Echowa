package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/goravel/framework/support/color"
	"github.com/goravel/framework/support/env"

	"goravel/app/facades"
	"goravel/app/http/controllers"
	"goravel/app/services"
)

// Hooks are called by CI, not a browser (ADR 0047). They sit outside the
// web group, with no session or CSRF token: the request's signature
// authenticates it (ADR 0008). The handler only queues, so the global
// request timeout stays.
func Hooks() {
	facades.Route().Post("/hooks/deploy/{app}", controllers.NewHookController().Deploy)
}

// HooksOnly passes on requests under /hooks/ and answers 404 to the rest,
// so a listener behind it serves the deploy webhooks and nothing else:
// not the UI, its login or /up. A path that cleans to something else
// (/hooks/../login, in any encoding) is refused rather than trusted to
// match no route.
//
// It also reads the body first, at most services.HookMaxBody of it.
// Goravel's gin driver reads a JSON body whole, in each middleware,
// before any Echowa code, so without this a stranger could make Echowa
// hold as large a body as they sent.
func HooksOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if !strings.HasPrefix(p, "/hooks/") || path.Clean(p) != p {
			hookError(w, http.StatusNotFound, "not found")
			return
		}
		if r.ContentLength > services.HookMaxBody {
			hookError(w, http.StatusRequestEntityTooLarge, "body is over 8 KiB")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, services.HookMaxBody))
		var tooLarge *http.MaxBytesError
		switch {
		case errors.As(err, &tooLarge):
			hookError(w, http.StatusRequestEntityTooLarge, "body is over 8 KiB")
			return
		case err != nil:
			hookError(w, http.StatusBadRequest, "couldn't read the body")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		next.ServeHTTP(w, r)
	})
}

// hookError answers as the hook handler does, with {"error": message}.
func hookError(w http.ResponseWriter, status int, message string) {
	data, _ := json.Marshal(map[string]string{"error": message})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}

// HooksListener is the second listener on ECHOWA_HOOKS_ADDR (ADR 0020),
// for a tunnel that maps a hostname without Cloudflare Access to it. It
// serves the same router as the UI's, routes and middleware included,
// behind HooksOnly. It starts and stops with the server, beside Goravel's
// own HTTP runner.
type HooksListener struct {
	server *http.Server
}

func NewHooksListener() *HooksListener {
	return &HooksListener{server: &http.Server{
		// The router is read per request: it's built after the runners.
		Handler: HooksOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			facades.Route().ServeHTTP(w, r)
		})),
		// The other end is anyone on the internet, through the tunnel,
		// and a hook is a few headers and at most 8 KiB.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}}
}

func (l *HooksListener) Signature() string {
	return "echowa:hooks"
}

// ShouldRun is the server with ECHOWA_HOOKS_ADDR set. Artisan commands
// serve nothing.
func (l *HooksListener) ShouldRun() bool {
	return !env.IsArtisan() && facades.Config().GetString("echowa.hooks_addr") != ""
}

// Run listens until Shutdown. An address it can't listen on stops Echowa,
// as the UI's would: CI would otherwise fail with no sign of why.
func (l *HooksListener) Run() error {
	ln, err := net.Listen("tcp", facades.Config().GetString("echowa.hooks_addr"))
	if err != nil {
		return err
	}
	color.Green().Println("[Hooks] Listening on: http://" + ln.Addr().String())
	if err := l.server.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

// Shutdown lets a hook being answered finish. Each only queues, so a few
// seconds is plenty.
func (l *HooksListener) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return l.server.Shutdown(ctx)
}
