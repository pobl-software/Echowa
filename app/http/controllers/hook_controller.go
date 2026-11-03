package controllers

import (
	"errors"
	"fmt"
	"io"
	"math"
	nethttp "net/http"
	"strconv"
	"time"

	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/support/carbon"

	"goravel/app/deploy"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/rules"
	"goravel/app/services"
)

// HookController takes deploy webhooks from CI (ADR 0047). It sits outside
// the web group: the signature is its authentication, and it has no
// session or CSRF token.
type HookController struct{}

func NewHookController() *HookController {
	return &HookController{}
}

// rejection is a hook answered with an error. Every refusal goes through
// one, so each is logged and answered the same way.
type rejection struct {
	status     int
	reason     services.HookRefusal
	message    string
	retryAfter time.Duration
}

func (e *rejection) Error() string {
	return e.message
}

// verifyRejections are VerifyDeployHook's errors as answers. All three
// count against the caller's IP.
var verifyRejections = map[error]*rejection{
	services.ErrHookMalformed: {status: http.StatusBadRequest, reason: services.HookRefusedMalformed, message: services.ErrHookMalformed.Error()},
	services.ErrHookClock:     {status: http.StatusUnauthorized, reason: services.HookRefusedClock, message: services.ErrHookClock.Error()},
	services.ErrHookSignature: {status: http.StatusUnauthorized, reason: services.HookRefusedSignature, message: services.ErrHookSignature.Error()},
}

// Deploy queues a deployment of the app named in the URL, of the ref in
// the body or the app's default tag. It only queues, so CI hears back
// well inside the global request timeout.
func (r *HookController) Deploy(ctx http.Context) http.Response {
	now := carbon.Now().StdTime()
	name := ctx.Request().Route("app")
	logName := loggedName(name)
	ip := services.ClientIP(ctx)
	reject := func(rej *rejection) http.Response {
		return r.reject(ctx, name, ip, now, rej)
	}

	failures := "failures|" + ip
	if wait, limited := services.HookLimited(failures, services.HookFailureLimit, services.HookFailureWindow, now); limited {
		return reject(tooMany(wait))
	}
	body, rej := hookBody(ctx)
	if rej != nil {
		return reject(rej)
	}
	signature := ctx.Request().Header("X-Echowa-Signature")
	app, err := services.VerifyDeployHook(ctx.Context(), name, ctx.Request().Header("X-Echowa-Timestamp"), signature, body, now)
	if err != nil {
		rej, ok := verifyRejections[err]
		if !ok {
			facades.Log().Error(fmt.Errorf("hook: deploy %q: %w", logName, err))
			return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "server error"})
		}
		services.HookHit(failures, services.HookFailureWindow, now)
		return reject(rej)
	}

	id, repeat, err := services.DeployHookOnce(signature, func() (uint, error) {
		return r.request(ctx, app, body, now)
	})
	switch {
	case errors.As(err, &rej):
		return reject(rej)
	case err != nil:
		facades.Log().Error(fmt.Errorf("hook: deploy %q: %w", logName, err))
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "server error"})
	case repeat:
		facades.Log().Infof("hook: deploy %q from %s: repeat of deployment #%d", logName, ip, id)
		return ctx.Response().Json(http.StatusOK, http.Json{"deployment_id": id})
	}
	facades.Log().Infof("hook: deploy %q from %s: queued deployment #%d", logName, ip, id)

	return ctx.Response().Json(http.StatusAccepted, http.Json{"deployment_id": id})
}

// hookBody is the body as the client signed it. Goravel's gin driver has
// read it already, before any middleware: a JSON object it puts back byte
// for byte, but anything else sent as JSON, or a form, it reads and keeps,
// leaving nothing to check the signature against.
func hookBody(ctx http.Context) ([]byte, *rejection) {
	req := ctx.Request().Origin()
	if req.ContentLength > services.HookMaxBody {
		return nil, &rejection{status: http.StatusRequestEntityTooLarge, reason: services.HookRefusedTooLarge, message: "body is over 8 KiB"}
	}
	body, err := io.ReadAll(nethttp.MaxBytesReader(ctx.Response().Writer(), req.Body, services.HookMaxBody))
	var tooLarge *nethttp.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		return nil, &rejection{status: http.StatusRequestEntityTooLarge, reason: services.HookRefusedTooLarge, message: "body is over 8 KiB"}
	case err != nil:
		return nil, &rejection{status: http.StatusBadRequest, reason: services.HookRefusedBody, message: "couldn't read the body"}
	case len(body) == 0 && req.ContentLength > 0:
		return nil, &rejection{status: http.StatusBadRequest, reason: services.HookRefusedBody, message: `body must be empty or a JSON object, {"ref": "..."}, sent as Content-Type: application/json`}
	}

	return body, nil
}

// request queues the deployment a verified hook asks for. Its errors are
// a *rejection or a server error.
func (r *HookController) request(ctx http.Context, app *models.App, body []byte, now time.Time) (uint, error) {
	ref, err := services.ParseHookBody(body)
	if err != nil {
		return 0, &rejection{status: http.StatusBadRequest, reason: services.HookRefusedBody, message: err.Error()}
	}
	if ref == "" {
		ref = app.DefaultTag
	}
	if !rules.IsDeployRef(ref) {
		return 0, &rejection{status: http.StatusUnprocessableEntity, reason: services.HookRefusedRef, message: "ref must be an image tag or a sha256: digest"}
	}
	if services.AppStopped(app.ID) {
		return 0, &rejection{status: http.StatusConflict, reason: services.HookRefusedStopped, message: "app is stopped; start it from Echowa"}
	}
	deploys := fmt.Sprintf("deploys|%d", app.ID)
	if wait, limited := services.HookLimited(deploys, services.HookDeployLimit, services.HookDeployWindow, now); limited {
		return 0, tooMany(wait)
	}

	deployment, err := deploy.Request(ctx.Context(), app, models.TriggerWebhook, ref)
	if err != nil {
		return 0, err
	}
	services.HookHit(deploys, services.HookDeployWindow, now)

	return deployment.ID, nil
}

func tooMany(wait time.Duration) *rejection {
	return &rejection{status: http.StatusTooManyRequests, reason: services.HookRefusedLimited, message: "too many requests", retryAfter: wait}
}

// reject logs a refused hook, keeps it for the app's CI section and
// answers it. The log has the app's name as sent, the client's IP and why:
// never the signature or the body.
func (r *HookController) reject(ctx http.Context, name, ip string, now time.Time, rej *rejection) http.Response {
	facades.Log().Warningf("hook: deploy %q from %s refused (%d): %s", loggedName(name), ip, rej.status, rej.message)
	services.RecordHookRefusal(ctx.Context(), name, services.HookCall{
		At: carbon.NewDateTime(carbon.FromStdTime(now)), IP: ip, Reason: rej.reason, Message: rej.message,
	})
	resp := ctx.Response()
	if rej.retryAfter > 0 {
		resp = resp.Header("Retry-After", strconv.Itoa(int(math.Ceil(rej.retryAfter.Seconds()))))
	}

	return resp.Json(rej.status, http.Json{"error": rej.message})
}

// loggedName is a name from a hook's URL, cut short: an unknown caller
// picks it, at any length.
func loggedName(name string) string {
	if len(name) > rules.MaxSlugLength {
		return name[:rules.MaxSlugLength] + "..."
	}

	return name
}
