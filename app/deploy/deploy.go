// Package deploy runs deployments: it pulls the requested image, resolves
// it to a digest, updates the app's Swarm service and waits for Swarm to
// finish or roll back (docs/mvp.md, "Deploying"; ADR 0017).
//
// A request writes a queued deployment and enqueues a Job for its app. The
// Job runs the app's queued deployments oldest first, one at a time, so
// requests for the same app queue behind each other while different apps
// deploy in parallel.
package deploy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/goravel/framework/contracts/queue"
	"github.com/goravel/framework/support/carbon"

	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/rules"
	"goravel/app/services"
	"goravel/app/templates"
)

// ErrBusy means another call is already running this app's deployments.
var ErrBusy = errors.New("a deployment of this app is already running")

// retryDelay is how long a Job for a busy app waits before trying again.
const retryDelay = 3 * time.Second

// Dispatch enqueues a Job for the app, after delay. Tests replace it.
var Dispatch = func(appID uint, delay time.Duration) error {
	pending := facades.Queue().Job(&Job{}, []queue.Arg{{Type: "uint", Value: appID}})
	if delay > 0 {
		pending = pending.Delay(time.Now().Add(delay))
	}

	return pending.Dispatch()
}

// Request records a queued deployment of ref (a tag or a sha256 digest,
// validated by rules.Deploy) and enqueues its app's Job.
func Request(ctx context.Context, app *models.App, trigger models.DeploymentTrigger, ref string) (*models.Deployment, error) {
	deployment, err := services.CreateDeployment(ctx, app, trigger, ref)
	if err != nil {
		return nil, err
	}
	if err := Dispatch(app.ID, 0); err != nil {
		return nil, fmt.Errorf("enqueue deployment #%d: %w", deployment.ID, err)
	}

	return deployment, nil
}

// Job is the queued work for one app. Its only argument is the app ID.
type Job struct{}

func (j *Job) Signature() string {
	return "deploy_app"
}

func (j *Job) Handle(args ...any) error {
	if len(args) != 1 {
		return fmt.Errorf("deploy_app: want 1 argument, got %d", len(args))
	}
	var appID uint
	switch id := args[0].(type) {
	case uint:
		appID = id
	case int:
		appID = uint(id)
	case float64:
		appID = uint(id)
	default:
		return fmt.Errorf("deploy_app: app ID has type %T", args[0])
	}

	err := RunQueued(context.Background(), appID)
	if errors.Is(err, ErrBusy) {
		// The running call may already have finished its last check for
		// queued deployments, so come back rather than drop the request.
		return Dispatch(appID, retryDelay)
	}

	return err
}

var (
	claimsMu sync.Mutex
	claims   = map[uint]bool{}
)

// claim makes this process the only runner of the app's deployments. One
// Echowa process runs per server (ADR 0013), so an in-memory claim is
// enough.
func claim(appID uint) bool {
	claimsMu.Lock()
	defer claimsMu.Unlock()
	if claims[appID] {
		return false
	}
	claims[appID] = true

	return true
}

func release(appID uint) {
	claimsMu.Lock()
	defer claimsMu.Unlock()
	delete(claims, appID)
}

// RunQueued runs the app's queued deployments, oldest first, until none are
// left. It returns ErrBusy at once if another call is running them. A
// deployment that fails is recorded as failed; only database errors are
// returned.
func RunQueued(ctx context.Context, appID uint) error {
	if !claim(appID) {
		return ErrBusy
	}
	defer release(appID)

	for {
		var deployment models.Deployment
		err := facades.Orm().WithContext(ctx).Query().
			Where("app_id", appID).Where("status", models.DeploymentQueued).Order("id").First(&deployment)
		if err != nil {
			return err
		}
		if deployment.ID == 0 {
			return nil
		}
		if err := run(ctx, &deployment); err != nil {
			return err
		}
	}
}

// run takes one deployment from queued to a final status.
func run(ctx context.Context, d *models.Deployment) error {
	var app models.App
	if err := facades.Orm().WithContext(ctx).Query().With("Registry").With("Volumes").Where("id", d.AppID).FirstOrFail(&app); err != nil {
		return err
	}
	r := &recorder{ctx: ctx, d: d}

	now := carbon.NewDateTime(carbon.Now())
	if err := r.move(models.DeploymentPulling, map[string]any{"started_at": now}); err != nil {
		return err
	}
	r.log("Deployment #%d started (%s, ref %s).", d.ID, d.Trigger, d.Ref)

	ref := pullRef(&app, d.Ref)
	if app.Registry != nil {
		r.log("Pulling %s with registry %s (%s as %s).", ref, app.Registry.Name, app.Registry.Host, app.Registry.Username)
	} else {
		r.log("Pulling %s (public image).", ref)
	}
	digest, err := services.Docker().PullImage(ctx, ref, services.RegistryAuth(app.Registry))
	if err != nil {
		return r.fail(err)
	}
	if err := r.update(map[string]any{"digest": digest}); err != nil {
		return err
	}
	d.Digest = &digest
	r.log("Resolved to %s.", digest)

	env, err := services.EnvVars(ctx, app.ID)
	if err != nil {
		return err
	}
	if externalTLS(&app) != nil {
		for _, key := range templates.TLSEnvKeys {
			if env[key] == "" {
				return r.fail(fmt.Errorf("%s is missing from the environment, so the server can't start with TLS. Turn External access off and on again to make a new certificate", key))
			}
		}
		r.log("External access is on: serving TLS with the app's certificate.")
	}
	host, err := services.DockerHost(ctx)
	if err != nil {
		return r.fail(err)
	}
	spec := ServiceSpec(&app, digest, env, services.AppLimits(&app, host))
	if err := r.move(models.DeploymentDeploying, nil); err != nil {
		return err
	}
	r.log("Updating service %s: %s.", spec.Name, describe(spec))
	if err := services.Docker().DeployService(ctx, spec); err != nil {
		return r.fail(err)
	}

	switch {
	case len(spec.Volumes) > 0:
		r.log("Waiting for Swarm to stop the old task and start the new one (stop-first, since a volume has one writer; rollback on failure).")
	case spec.HostPort != nil:
		r.log("Waiting for Swarm to stop the old task and start the new one (stop-first, since one task at a time holds the host port; rollback on failure).")
	default:
		r.log("Waiting for Swarm to start the new task and retire the old one (start-first, rollback on failure).")
	}
	result, err := services.Docker().WaitForUpdate(ctx, spec.Name)
	if err != nil {
		return r.fail(err)
	}

	switch result.Outcome {
	case docker.OutcomeCompleted:
		// The final status goes last, after the log: a deployment stream
		// stops reading once it sees it (services.FollowDeployment).
		r.log("Deployment succeeded: %s is now running %s.", spec.Name, digest)
		if err := r.finish(models.DeploymentSucceeded, ""); err != nil {
			return err
		}
		if _, err := facades.Orm().WithContext(ctx).Query().Model(&app).Update("current_deployment_id", d.ID); err != nil {
			return err
		}
	case docker.OutcomeRolledBack:
		message := "Swarm rolled the update back: " + swarmMessage(result)
		r.log("%s The previous version is still running.", message)
		if err := r.finish(models.DeploymentRolledBack, message); err != nil {
			return err
		}
	default:
		message := "Swarm paused the update: " + swarmMessage(result) + ". Check the service with `docker service ps " + spec.Name + "`."
		r.log("%s", message)
		if err := r.finish(models.DeploymentFailed, message); err != nil {
			return err
		}
	}

	return nil
}

// pullRef is what to pull for ref: repository:tag, or repository@digest.
func pullRef(app *models.App, ref string) string {
	if rules.IsDigest(ref) {
		return app.Repository + "@" + ref
	}

	return app.Repository + ":" + ref
}

// ServiceSpec is the Swarm service an app's deployment of digest runs.
func ServiceSpec(app *models.App, digest string, env map[string]string, limits services.ResourceLimits) docker.ServiceSpec {
	spec := docker.ServiceSpec{
		Name:     app.ServiceName(),
		App:      app.Name,
		Image:    digest,
		Env:      env,
		Replicas: uint64(app.Replicas),
		Args:     app.Args,
		Limits:   limits.Docker(),
	}
	for _, v := range app.Volumes {
		spec.Volumes = append(spec.Volumes, docker.Volume{Name: v.Name, Target: v.Target})
	}
	// The same app always gives the same spec, whatever order the rows load in.
	slices.SortFunc(spec.Volumes, func(a, b docker.Volume) int { return strings.Compare(a.Target, b.Target) })
	if app.Domain != nil {
		spec.Route = &docker.Route{Domain: *app.Domain, Port: app.Port, TLS: app.TLS}
	}
	if app.HealthcheckCmd != nil {
		spec.Healthcheck = &docker.Healthcheck{Command: *app.HealthcheckCmd}
	}
	// A template app's row keeps its args only for an older release after a
	// rollback (ADR 0027).
	if tmpl, ok := appTemplate(app); ok {
		spec.Args = tmpl.Args(templates.Options{TLS: app.ExternalPort != nil, SlowQueries: app.SlowQueries})
	}
	if app.ExternalPort != nil {
		spec.HostPort = &docker.HostPort{Published: *app.ExternalPort, Target: app.Port}
		if tls := externalTLS(app); tls != nil && tls.Port != 0 {
			spec.HostPort.Target = tls.Port
		}
	}

	return spec
}

func appTemplate(app *models.App) (templates.Template, bool) {
	if app.Template == nil {
		return templates.Template{}, false
	}

	return templates.Find(*app.Template)
}

// externalTLS is how the app's template serves TLS while external access
// is on (ADR 0026), or nil.
func externalTLS(app *models.App) *templates.TLS {
	tmpl, ok := appTemplate(app)
	if app.ExternalPort == nil || !ok {
		return nil
	}

	return tmpl.TLS
}

// describe summarises a spec for the log. Env values never appear.
func describe(spec docker.ServiceSpec) string {
	parts := []string{fmt.Sprintf("%d replica(s)", spec.Replicas), fmt.Sprintf("%d env var(s)", len(spec.Env))}
	if spec.Route != nil {
		scheme := "http"
		if spec.Route.TLS {
			scheme = "https"
		}
		parts = append(parts, fmt.Sprintf("%s://%s to port %d", scheme, spec.Route.Domain, spec.Route.Port))
	} else {
		parts = append(parts, "no route")
	}
	if spec.Healthcheck != nil {
		parts = append(parts, "healthcheck on")
	}
	if l := spec.Limits; l != (docker.Limits{}) {
		limit := fmt.Sprintf("at most %s CPU and %d MiB of memory a task", strconv.FormatFloat(float64(l.NanoCPUs)/1e9, 'f', -1, 64), l.MemoryBytes>>20)
		if l.SwapBytes > 0 {
			limit += fmt.Sprintf(" with %d MiB of swap past it", l.SwapBytes>>20)
		}
		parts = append(parts, limit)
	}
	if spec.Args != nil {
		parts = append(parts, "custom command")
	}
	for _, v := range spec.Volumes {
		parts = append(parts, fmt.Sprintf("volume %s at %s", v.Name, v.Target))
	}
	if spec.HostPort != nil {
		parts = append(parts, fmt.Sprintf("host port %d to port %d", spec.HostPort.Published, spec.HostPort.Target))
	}

	return strings.Join(parts, ", ")
}

func swarmMessage(result docker.UpdateResult) string {
	if result.Message == "" {
		return "no reason given"
	}

	return strings.TrimSuffix(result.Message, ".")
}

// recorder writes a deployment's status changes and log lines.
type recorder struct {
	ctx context.Context
	d   *models.Deployment
}

// log appends one timestamped line. The append happens in SQL, so readers
// never see a half-written log.
func (r *recorder) log(format string, args ...any) {
	line := time.Now().UTC().Format(time.RFC3339) + " " + fmt.Sprintf(format, args...) + "\n"
	_, err := facades.Orm().WithContext(r.ctx).Query().
		Exec("UPDATE deployments SET log = log || ? WHERE id = ?", line, r.d.ID)
	if err != nil {
		facades.Log().Error(fmt.Errorf("deployment #%d log: %w", r.d.ID, err))
		return
	}
	r.d.Log += line
}

func (r *recorder) update(fields map[string]any) error {
	_, err := facades.Orm().WithContext(r.ctx).Query().Model(r.d).Update(fields)

	return err
}

// move changes status, refusing transitions the lifecycle doesn't allow.
func (r *recorder) move(next models.DeploymentStatus, fields map[string]any) error {
	if !r.d.Status.CanBecome(next) {
		return fmt.Errorf("deployment #%d: can't go from %s to %s", r.d.ID, r.d.Status, next)
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["status"] = next
	if err := r.update(fields); err != nil {
		return err
	}
	r.d.Status = next

	return nil
}

func (r *recorder) finish(status models.DeploymentStatus, message string) error {
	fields := map[string]any{"finished_at": carbon.NewDateTime(carbon.Now())}
	if message != "" {
		fields["error"] = message
	}

	return r.move(status, fields)
}

// fail records err and ends the deployment as failed.
func (r *recorder) fail(err error) error {
	r.log("Failed: %v", err)

	return r.finish(models.DeploymentFailed, err.Error())
}
