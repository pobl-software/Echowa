package models

import (
	"slices"

	"github.com/goravel/framework/database/orm"
	"github.com/goravel/framework/support/carbon"
)

// DeploymentStatus is where a deployment is in its lifecycle (docs/mvp.md,
// "Deploying").
type DeploymentStatus string

const (
	DeploymentQueued     DeploymentStatus = "queued"
	DeploymentPulling    DeploymentStatus = "pulling"
	DeploymentDeploying  DeploymentStatus = "deploying"
	DeploymentSucceeded  DeploymentStatus = "succeeded"
	DeploymentFailed     DeploymentStatus = "failed"
	DeploymentRolledBack DeploymentStatus = "rolled_back"
)

// deploymentTransitions lists where each status may go next. Any running
// status can fail, e.g. when Echowa restarts mid-deploy.
var deploymentTransitions = map[DeploymentStatus][]DeploymentStatus{
	DeploymentQueued:    {DeploymentPulling, DeploymentFailed},
	DeploymentPulling:   {DeploymentDeploying, DeploymentFailed},
	DeploymentDeploying: {DeploymentSucceeded, DeploymentFailed, DeploymentRolledBack},
}

// Running reports whether the deployment hasn't finished yet.
func (s DeploymentStatus) Running() bool {
	return s == DeploymentQueued || s == DeploymentPulling || s == DeploymentDeploying
}

// CanBecome reports whether s may move to next.
func (s DeploymentStatus) CanBecome(next DeploymentStatus) bool {
	return slices.Contains(deploymentTransitions[s], next)
}

// DeploymentTrigger is what started a deployment.
type DeploymentTrigger string

const (
	TriggerManual   DeploymentTrigger = "manual"
	TriggerWebhook  DeploymentTrigger = "webhook"
	TriggerRollback DeploymentTrigger = "rollback"
)

// Deployment is one attempt to run a version of an app.
type Deployment struct {
	orm.Model
	AppID   uint              `json:"app_id"`
	Status  DeploymentStatus  `json:"status"`
	Trigger DeploymentTrigger `json:"trigger"`
	// Ref is what was asked for: a tag, or a "sha256:..." digest.
	Ref string `json:"ref"`
	// Digest is what Ref resolved to: "repository@sha256:...". Nil until
	// the pull finishes.
	Digest *string `json:"digest"`
	// Log holds timestamped progress lines. It never contains secrets.
	Log        string           `json:"log"`
	Error      *string          `json:"error"`
	StartedAt  *carbon.DateTime `json:"started_at"`
	FinishedAt *carbon.DateTime `json:"finished_at"`
}
