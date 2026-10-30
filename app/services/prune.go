package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"goravel/app/alerts"
	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/notify"
)

// Who started a prune.
const (
	PruneScheduled = "schedule"
	PruneManual    = "manual"
)

// ErrPruneRunning means a prune is already in progress.
var ErrPruneRunning = errors.New("a prune is already running")

// PruneConfig is how pruning is set up (config/echowa.go).
type PruneConfig struct {
	// Keep is how many of each app's latest successful deployments keep
	// their images.
	Keep int
	// MinAge protects images created more recently than this.
	MinAge time.Duration
}

// PruneSettings reads ECHOWA_PRUNE_KEEP and ECHOWA_PRUNE_MIN_AGE, falling
// back to the defaults when they don't make sense.
func PruneSettings() PruneConfig {
	keep := facades.Config().GetInt("echowa.prune.keep", 5)
	if keep < 1 {
		keep = 5
	}

	return PruneConfig{Keep: keep, MinAge: configDuration("echowa.prune.min_age", 72*time.Hour)}
}

// PruneRun is one prune, for the Settings page.
type PruneRun struct {
	Trigger    string     `json:"trigger"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	// Kept lists the digest references protected for rollbacks.
	Kept           []string `json:"kept"`
	Removed        []string `json:"removed"`
	SpaceReclaimed int64    `json:"space_reclaimed"`
	Error          string   `json:"error"`
}

// pruning holds the prune in progress, if any, and the last one to finish.
// One Echowa process runs per server (ADR 0013), so memory is enough; the
// log keeps the history.
var pruning = struct {
	sync.Mutex
	current *PruneRun
	last    *PruneRun
}{}

// PruneState returns the prune in progress (nil if none) and the last one
// that finished (nil if none has since Echowa started).
func PruneState() (current, last *PruneRun) {
	pruning.Lock()
	defer pruning.Unlock()

	return pruning.current, pruning.last
}

// RunPrune removes unused images of Echowa's apps older than the configured
// age, keeping each app's recent successful deployments (docs/mvp.md,
// "Image pruning"), and logs what it removed.
func RunPrune(ctx context.Context, trigger string) (PruneRun, error) {
	run, err := beginPrune(trigger)
	if err != nil {
		return PruneRun{}, err
	}

	return finishPrune(prune(ctx, run)), nil
}

// StartPrune starts a prune in the background and returns at once.
func StartPrune(trigger string) error {
	run, err := beginPrune(trigger)
	if err != nil {
		return err
	}
	go func() { finishPrune(prune(context.Background(), run)) }()

	return nil
}

func beginPrune(trigger string) (*PruneRun, error) {
	pruning.Lock()
	defer pruning.Unlock()
	if pruning.current != nil {
		return nil, ErrPruneRunning
	}
	pruning.current = &PruneRun{Trigger: trigger, StartedAt: time.Now(), Kept: []string{}, Removed: []string{}}

	return pruning.current, nil
}

func finishPrune(run PruneRun) PruneRun {
	now := time.Now()
	run.FinishedAt = &now
	pruning.Lock()
	pruning.current = nil
	pruning.last = &run
	pruning.Unlock()
	if len(run.Removed) > 0 {
		notifyPrune(run)
	}

	return run
}

// notifyPrune tells the server's rules what a prune removed.
func notifyPrune(run PruneRun) {
	title := fmt.Sprintf("Image prune removed %d images", len(run.Removed))
	if len(run.Removed) == 1 {
		title = "Image prune removed 1 image"
	}
	startedBy := "The daily prune"
	if run.Trigger == PruneManual {
		startedBy = "Prune now"
	}
	Notify(alerts.Event{
		Trigger: alerts.ImagesPruned,
		Title:   title,
		Summary: "It freed " + sizeText(run.SpaceReclaimed) + ".",
		Fields:  []notify.Field{{Label: "Started by", Value: startedBy}},
		Path:    "/settings",
	})
}

func prune(ctx context.Context, started *PruneRun) PruneRun {
	run := *started
	cfg := PruneSettings()
	opts, err := PruneOptions(ctx, cfg)
	if err != nil {
		run.Error = err.Error()
		facades.Log().Error(fmt.Errorf("prune (%s): %w", run.Trigger, err))
		return run
	}
	run.Kept = opts.Keep
	if len(opts.Repositories) == 0 {
		facades.Log().Infof("prune (%s): no app images to prune", run.Trigger)
		return run
	}

	report, err := Docker().PruneImages(ctx, opts)
	run.Removed = append(run.Removed, report.Removed...)
	run.SpaceReclaimed = report.SpaceReclaimed
	for _, ref := range report.Removed {
		facades.Log().Infof("prune (%s): removed %s", run.Trigger, ref)
	}
	if err != nil {
		run.Error = err.Error()
		facades.Log().Error(fmt.Errorf("prune (%s): %w", run.Trigger, err))
	}
	facades.Log().Infof("prune (%s): removed %d image(s), %d bytes reclaimed; kept the images of each app's last %d successful deployments (%d digests) and anything newer than %s",
		run.Trigger, len(report.Removed), report.SpaceReclaimed, cfg.Keep, len(opts.Keep), cfg.MinAge)

	return run
}

// PruneOptions decides what a prune may touch: the repositories of every
// app and of every image Echowa deployed, minus the digests of each app's
// last cfg.Keep successful deployments, its current deployment and any
// deployment still running.
func PruneOptions(ctx context.Context, cfg PruneConfig) (docker.PruneOptions, error) {
	query := facades.Orm().WithContext(ctx).Query()
	var repos []struct{ Repository string }
	err := query.Raw(`
		SELECT repository FROM apps
		UNION
		SELECT substr(digest, 1, instr(digest, '@') - 1) FROM deployments WHERE digest LIKE '%@sha256:%'`).Scan(&repos)
	if err != nil {
		return docker.PruneOptions{}, err
	}
	var kept []struct{ Digest string }
	err = query.Raw(`
		SELECT digest FROM (
			SELECT digest, ROW_NUMBER() OVER (PARTITION BY app_id ORDER BY id DESC) AS n
			FROM deployments WHERE status = ? AND digest IS NOT NULL
		) WHERE n <= ?
		UNION
		SELECT digest FROM deployments WHERE status IN (?, ?, ?) AND digest IS NOT NULL
		UNION
		SELECT d.digest FROM apps a JOIN deployments d ON d.id = a.current_deployment_id WHERE d.digest IS NOT NULL
		ORDER BY digest`,
		models.DeploymentSucceeded, cfg.Keep, models.DeploymentQueued, models.DeploymentPulling, models.DeploymentDeploying,
	).Scan(&kept)
	if err != nil {
		return docker.PruneOptions{}, err
	}

	opts := docker.PruneOptions{OlderThan: cfg.MinAge, Repositories: []string{}, Keep: []string{}}
	for _, r := range repos {
		if r.Repository = strings.TrimSpace(r.Repository); r.Repository != "" {
			opts.Repositories = append(opts.Repositories, r.Repository)
		}
	}
	for _, k := range kept {
		opts.Keep = append(opts.Keep, k.Digest)
	}

	return opts, nil
}
