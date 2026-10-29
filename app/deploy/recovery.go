package deploy

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/goravel/framework/support/carbon"
	"github.com/goravel/framework/support/env"

	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/services"
)

// processStart separates this process's work from what a previous one left
// behind. Stored times have second precision, hence the truncation.
var processStart = time.Now().Truncate(time.Second)

// Recover cleans up after an Echowa that stopped mid-deploy:
//
//   - deployments it left pulling or deploying are marked failed, since
//     nobody is watching them any more;
//   - queued deployments get a fresh Job, since the old one may have died
//     with the process (a spare Job finds nothing to do);
//   - queue jobs it had reserved are deleted. Goravel v1.18 never retries a
//     reserved job, so they would sit in the table forever.
//
// Work this process started after processStart is left alone.
func Recover(ctx context.Context) error {
	var running []models.Deployment
	err := facades.Orm().WithContext(ctx).Query().
		WhereIn("status", []any{models.DeploymentPulling, models.DeploymentDeploying}).Get(&running)
	if err != nil {
		return err
	}
	for i := range running {
		d := &running[i]
		if d.StartedAt != nil && !d.StartedAt.StdTime().Before(processStart) {
			continue
		}
		r := &recorder{ctx: ctx, d: d}
		r.log("Echowa restarted while this deployment was %s, so it stopped part way. Check the app's status and deploy again if needed.", d.Status)
		if err := r.finish(models.DeploymentFailed, fmt.Sprintf("Echowa restarted while this deployment was %s.", d.Status)); err != nil {
			return err
		}
	}

	var queued []models.Deployment
	err = facades.Orm().WithContext(ctx).Query().Where("status", models.DeploymentQueued).Order("id").Get(&queued)
	if err != nil {
		return err
	}
	var apps []uint
	for _, d := range queued {
		if d.CreatedAt != nil && d.CreatedAt.StdTime().Before(processStart) && !slices.Contains(apps, d.AppID) {
			apps = append(apps, d.AppID)
		}
	}
	for _, appID := range apps {
		if err := Dispatch(appID, 0); err != nil {
			return err
		}
	}

	var reserved []struct {
		ID         uint
		ReservedAt *carbon.DateTime
	}
	if err := facades.DB().Table("jobs").Select("id", "reserved_at").WhereNotNull("reserved_at").Get(&reserved); err != nil {
		return err
	}
	for _, job := range reserved {
		if job.ReservedAt != nil && job.ReservedAt.StdTime().Before(processStart) {
			if _, err := facades.DB().Table("jobs").Where("id", job.ID).Delete(); err != nil {
				return err
			}
		}
	}

	return nil
}

// RecoveryRunner runs Recover, services.RecoverTaskRuns for scheduled
// tasks (ADR 0032), services.RecoverBackups, RecoverRestores and
// RemoveUploads (ADR 0034), and SyncPoolers (ADR 0035), once when Echowa
// starts serving, alongside Goravel's queue worker.
type RecoveryRunner struct {
	done chan struct{}
}

func NewRecoveryRunner() *RecoveryRunner {
	return &RecoveryRunner{done: make(chan struct{})}
}

func (r *RecoveryRunner) Signature() string {
	return "echowa:deploy-recovery"
}

// ShouldRun skips artisan commands: only the server owns deployments.
func (r *RecoveryRunner) ShouldRun() bool {
	return !env.IsArtisan()
}

// Run recovers, then waits for shutdown like the other runners.
func (r *RecoveryRunner) Run() error {
	if err := Recover(context.Background()); err != nil {
		facades.Log().Error(fmt.Errorf("deployment recovery: %w", err))
	}
	if err := services.RecoverTaskRuns(context.Background()); err != nil {
		facades.Log().Error(fmt.Errorf("scheduled task recovery: %w", err))
	}
	if err := services.RecoverBackups(context.Background()); err != nil {
		facades.Log().Error(fmt.Errorf("backup recovery: %w", err))
	}
	if err := services.RecoverRestores(context.Background()); err != nil {
		facades.Log().Error(fmt.Errorf("restore recovery: %w", err))
	}
	if err := services.RemoveUploads(); err != nil {
		facades.Log().Error(fmt.Errorf("remove uploads: %w", err))
	}
	if err := services.SyncPoolers(context.Background()); err != nil {
		facades.Log().Error(fmt.Errorf("connection poolers: %w", err))
	}
	<-r.done

	return nil
}

func (r *RecoveryRunner) Shutdown() error {
	close(r.done)

	return nil
}
