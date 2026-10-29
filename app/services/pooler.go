package services

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/templates"
)

// ErrNoPooler means the app's template has no connection pooler.
var ErrNoPooler = errors.New("this app has no connection pooler")

// PoolerSpec is the Swarm service that runs the app's connection pooler,
// from env, the app's env vars. Only the vars the pooler reads go into it.
func PoolerSpec(app *models.App, env map[string]string) (docker.ServiceSpec, error) {
	if app.Template == nil {
		return docker.ServiceSpec{}, ErrNoPooler
	}
	tmpl, ok := templates.Find(*app.Template)
	if !ok || tmpl.Pooler == nil {
		return docker.ServiceSpec{}, ErrNoPooler
	}
	pooler := tmpl.Pooler
	poolerEnv, err := pooler.EnvFrom(app.ServiceName(), env)
	if err != nil {
		return docker.ServiceSpec{}, err
	}

	return docker.ServiceSpec{
		Name:        app.PoolerServiceName(),
		App:         app.Name,
		Role:        docker.RolePooler,
		Image:       pooler.Image,
		Env:         poolerEnv,
		Replicas:    1,
		Entrypoint:  pooler.Command,
		Healthcheck: &docker.Healthcheck{Command: pooler.Healthcheck},
		Limits:      pooler.Limits,
	}, nil
}

// HasPooler reports whether the app's template runs a connection pooler.
func HasPooler(app *models.App) bool {
	if app.Template == nil {
		return false
	}
	tmpl, ok := templates.Find(*app.Template)

	return ok && tmpl.Pooler != nil
}

// SetPooling stores the app's switch and syncs its pooler in the
// background, since a pull and Swarm's monitor window outlast a request.
func SetPooling(ctx context.Context, app *models.App, on bool) error {
	if _, err := facades.Orm().WithContext(ctx).Query().Model(app).Update("pooling", on); err != nil {
		return err
	}
	app.Pooling = on
	StartPoolerSync(app)

	return nil
}

// PoolerNameTaken reports whether an app called <app>-pooler exists. Its
// service has the name the app's pooler needs, so pooling can't go on.
func PoolerNameTaken(ctx context.Context, app *models.App) (bool, error) {
	return facades.Orm().WithContext(ctx).Query().Model(&models.App{}).Where("name", app.Name+models.PoolerSuffix).Exists()
}

// poolerSyncs tracks each app's pooler syncs, in memory. An app's entry
// goes once its syncs have finished and the last one succeeded.
var poolerSyncs = struct {
	sync.Mutex
	apps map[uint]*poolerSync
	wg   sync.WaitGroup
}{apps: map[uint]*poolerSync{}}

type poolerSync struct {
	run     sync.Mutex // held by the sync that's running
	pending int        // syncs started and not finished
	err     string     // why the last one to finish failed
}

// SyncPooler makes the app's pooler service match its row: the pinned
// image with the app's current credentials while pooling is on, and no
// service while it's off or the app is gone. A pooler whose spec hasn't
// changed is left alone, so its clients stay connected. An app's syncs
// run one at a time, each reading the row afresh, so the last change
// wins. The error is kept for the Connection tab until a sync succeeds.
func SyncPooler(ctx context.Context, app *models.App) error {
	return enterPoolerSync(app.ID).sync(ctx, app)
}

// StartPoolerSync runs SyncPooler in the background. It counts as running
// from now, so a page rendered straight after shows it.
func StartPoolerSync(app *models.App) {
	state := enterPoolerSync(app.ID)
	target := *app
	go func() { _ = state.sync(context.Background(), &target) }()
}

func poolerSyncing(appID uint) bool {
	poolerSyncs.Lock()
	defer poolerSyncs.Unlock()
	state := poolerSyncs.apps[appID]

	return state != nil && state.pending > 0
}

// WaitForPoolers waits until no pooler sync is running. For tests.
func WaitForPoolers() {
	poolerSyncs.wg.Wait()
}

func enterPoolerSync(appID uint) *poolerSync {
	poolerSyncs.Lock()
	defer poolerSyncs.Unlock()
	state := poolerSyncs.apps[appID]
	if state == nil {
		state = &poolerSync{}
		poolerSyncs.apps[appID] = state
	}
	state.pending++
	poolerSyncs.wg.Add(1)

	return state
}

func (s *poolerSync) sync(ctx context.Context, app *models.App) error {
	s.run.Lock()
	err := syncPooler(ctx, app)
	s.run.Unlock()
	if err != nil {
		facades.Log().Error(fmt.Errorf("connection pooler of %s: %w", app.Name, err))
	}

	poolerSyncs.Lock()
	defer poolerSyncs.Unlock()
	s.pending--
	s.err = ""
	if err != nil {
		s.err = err.Error()
	}
	if s.pending == 0 && s.err == "" {
		delete(poolerSyncs.apps, app.ID)
	}
	poolerSyncs.wg.Done()

	return err
}

func syncPooler(ctx context.Context, app *models.App) error {
	var current models.App
	if err := facades.Orm().WithContext(ctx).Query().Where("id", app.ID).First(&current); err != nil {
		return err
	}
	if current.ID == 0 || !current.Pooling {
		return removePooler(ctx, app.PoolerServiceName())
	}
	taken, err := PoolerNameTaken(ctx, &current)
	if err != nil {
		return err
	}
	if taken {
		return fmt.Errorf("the app %s%s has the service name the pooler needs, %s", current.Name, models.PoolerSuffix, current.PoolerServiceName())
	}

	env, err := EnvVars(ctx, current.ID)
	if err != nil {
		return err
	}
	spec, err := PoolerSpec(&current, env)
	if err != nil {
		return err
	}
	same, err := Docker().ServiceMatches(ctx, spec)
	if err != nil || same {
		return err
	}
	if _, err := Docker().PullImage(ctx, spec.Image, nil); err != nil {
		return err
	}
	if err := Docker().DeployService(ctx, spec); err != nil {
		return err
	}
	result, err := Docker().WaitForUpdate(ctx, spec.Name)
	if err != nil {
		return err
	}
	message := strings.TrimSuffix(result.Message, ".")
	if message == "" {
		message = "no reason given"
	}
	switch result.Outcome {
	case docker.OutcomeCompleted:
		return nil
	case docker.OutcomeRolledBack:
		return fmt.Errorf("Swarm rolled the pooler back: %s", message)
	default:
		return fmt.Errorf("Swarm paused the pooler's update: %s", message)
	}
}

// removePooler removes the service called name only if it's labelled as a
// pooler: an app called <app>-pooler has the same name.
func removePooler(ctx context.Context, name string) error {
	poolers, err := Docker().ServicesWithRole(ctx, docker.RolePooler)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(poolers, func(p docker.ServiceRef) bool { return p.Name == name }) {
		return nil
	}

	return Docker().RemoveService(ctx, name)
}

// RemovePooler removes the app's pooler, if it has one, before the app
// goes. DeleteApp then syncs once more, so a sync that was already running
// can't leave one behind.
func RemovePooler(ctx context.Context, app *models.App) error {
	if !HasPooler(app) {
		return nil
	}

	return removePooler(ctx, app.PoolerServiceName())
}

// SyncPoolers syncs the pooler of every app with pooling on, and of every
// app whose pooler still runs, and removes poolers whose app is gone. Boot
// runs it, so a release with a newer pinned image rolls the poolers, a
// pooler removed by hand comes back, and one an older release left behind
// after deleting its app goes.
func SyncPoolers(ctx context.Context) error {
	var apps []models.App
	if err := facades.Orm().WithContext(ctx).Query().Order("name").Get(&apps); err != nil {
		return err
	}
	running, err := Docker().ServicesWithRole(ctx, docker.RolePooler)
	if err != nil {
		return err
	}

	var errs []error
	synced := map[string]bool{}
	for _, app := range apps {
		if app.Pooling || slices.Contains(running, docker.ServiceRef{Name: app.PoolerServiceName(), App: app.Name}) {
			synced[app.PoolerServiceName()] = true
			// Logged, and kept for the Connection tab.
			_ = SyncPooler(ctx, &app)
		}
	}
	for _, pooler := range running {
		if !synced[pooler.Name] {
			errs = append(errs, Docker().RemoveService(ctx, pooler.Name))
		}
	}

	return errors.Join(errs...)
}

// PoolerStatus is the app's connection pooler as its Connection tab shows
// it.
type PoolerStatus struct {
	// Syncing is set while Echowa starts, updates or removes the pooler.
	Syncing bool `json:"syncing"`
	// Error is why the last sync failed, until one succeeds.
	Error string `json:"error"`
	// Service is the pooler's Swarm service as Docker reports it now.
	Service LiveStatus `json:"service"`
}

// AppPoolerStatus is the app's pooler's status. Ask only while pooling is
// on: otherwise an app called <app>-pooler could own the service name.
func AppPoolerStatus(ctx context.Context, app *models.App) PoolerStatus {
	var status PoolerStatus
	poolerSyncs.Lock()
	if state := poolerSyncs.apps[app.ID]; state != nil {
		status.Syncing = state.pending > 0
		status.Error = state.err
	}
	poolerSyncs.Unlock()
	status.Service = serviceStatus(ctx, app.PoolerServiceName())

	return status
}
