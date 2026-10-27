package services

import (
	"context"

	"goravel/app/facades"
	"goravel/app/models"
)

// CreateDeployment records a queued deployment of ref (a tag or digest,
// already validated with rules.Deploy). Running it is the deploy job's
// work.
func CreateDeployment(ctx context.Context, app *models.App, trigger models.DeploymentTrigger, ref string) (*models.Deployment, error) {
	deployment := &models.Deployment{AppID: app.ID, Status: models.DeploymentQueued, Trigger: trigger, Ref: ref}
	if err := facades.Orm().WithContext(ctx).Query().Create(deployment); err != nil {
		return nil, err
	}

	return deployment, nil
}
