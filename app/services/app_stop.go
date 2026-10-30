package services

import (
	"context"
	"fmt"

	"goravel/app/facades"
	"goravel/app/models"
)

// DeploymentActive means a deployment of the app hasn't finished, so it
// can't be stopped or started: a queued one would start the app again
// right after a stop.
type DeploymentActive struct {
	ID uint
}

func (e *DeploymentActive) Error() string {
	return fmt.Sprintf("deployment #%d is running", e.ID)
}

// AppHeld means other work holds the app's lock, so it can't be stopped.
// A restore must never be cut, and a backup or export would fail.
type AppHeld struct {
	Holder AppWork
}

func (e *AppHeld) Error() string {
	return e.Holder.Describe() + " of the app is running"
}

// ActiveDeployment is the app's oldest unfinished deployment, or nil.
func ActiveDeployment(ctx context.Context, app *models.App) (*models.Deployment, error) {
	var d models.Deployment
	err := facades.Orm().WithContext(ctx).Query().Where("app_id", app.ID).
		WhereIn("status", []any{models.DeploymentQueued, models.DeploymentPulling, models.DeploymentDeploying}).
		Order("id").First(&d)
	if err != nil {
		return nil, err
	}
	if d.ID == 0 {
		return nil, nil
	}

	return &d, nil
}

// StopApp scales the app's service to 0 replicas, which Swarm does within
// the tasks' grace period. The error is a *DeploymentActive or *AppHeld, or
// wraps docker.ErrServiceNotFound if the app isn't deployed. The pooler of
// a PostgreSQL app stays up and reconnects once the database is back.
func StopApp(ctx context.Context, app *models.App) error {
	unlock, holder, ok := LockApp(app.ID, WorkStop)
	if !ok {
		return &AppHeld{Holder: holder}
	}
	defer unlock()
	// Under the lock, so no queued deploy can start between this check
	// and the stop.
	active, err := ActiveDeployment(ctx, app)
	if err != nil {
		return err
	}
	if active != nil {
		return &DeploymentActive{ID: active.ID}
	}

	return Docker().StopService(ctx, app.ServiceName())
}
