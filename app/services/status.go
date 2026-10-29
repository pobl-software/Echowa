package services

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/goravel/framework/support/carbon"

	"goravel/app/docker"
	"goravel/app/facades"
	"goravel/app/models"
)

// Live states of an app's Swarm service, for the UI.
const (
	StateRunning     = "running"      // all desired replicas running
	StateDegraded    = "degraded"     // some, but not all, running
	StateDown        = "down"         // none running
	StateNotDeployed = "not_deployed" // no Swarm service yet
	StateUnknown     = "unknown"      // Docker didn't answer
)

// LiveStatus is an app's Swarm service as the UI shows it.
type LiveStatus struct {
	State   string `json:"state"`
	Running uint64 `json:"running"`
	Desired uint64 `json:"desired"`
	// Update is Swarm's update state, e.g. "updating" or "rollback_completed".
	Update string `json:"update"`
	// Digest is the short digest the service runs, e.g. "3f1c2a9b8d7e".
	Digest string `json:"digest"`
	// UnhealthySince is set when the service has run fewer replicas than
	// desired for longer than UnhealthyAfter: when that started.
	UnhealthySince *time.Time `json:"unhealthy_since"`
	// Limits are the ones the service runs with, which are the app's
	// saved ones only after a deploy.
	Limits ResourceLimits `json:"limits"`
	// OOMKilledAt is when a task was last killed for going over its
	// memory limit, if Swarm still keeps that task.
	OOMKilledAt *time.Time `json:"oom_killed_at"`
}

// AppLiveStatus asks Docker how the app's service is doing, and marks it
// unhealthy if it's still short of replicas the status poller found it
// short of more than UnhealthyAfter ago.
func AppLiveStatus(ctx context.Context, app *models.App) LiveStatus {
	live := serviceStatus(ctx, app.ServiceName())
	if live.Running < live.Desired {
		live.UnhealthySince = unhealthySince(app.ID, time.Now())
	}

	return live
}

// serviceStatus is the service as Docker reports it right now.
func serviceStatus(ctx context.Context, name string) LiveStatus {
	status, err := Docker().ServiceStatus(ctx, name)
	if errors.Is(err, docker.ErrServiceNotFound) {
		return LiveStatus{State: StateNotDeployed}
	}
	if err != nil {
		return LiveStatus{State: StateUnknown}
	}

	live := LiveStatus{
		Running:     status.Running,
		Desired:     status.Desired,
		Update:      string(status.UpdateState),
		Digest:      ShortDigest(status.Image),
		Limits:      limitsFromDocker(status.Limits),
		OOMKilledAt: status.OOMKilledAt,
	}
	switch {
	case status.Desired > 0 && status.Running >= status.Desired:
		live.State = StateRunning
	case status.Running > 0:
		live.State = StateDegraded
	default:
		live.State = StateDown
	}

	return live
}

// ShortDigest returns the first 12 hex digits of a digest or digest
// reference, or "" if there's none.
func ShortDigest(ref string) string {
	_, hex, found := strings.Cut(ref, "sha256:")
	if !found || len(hex) < 12 {
		return ""
	}

	return hex[:12]
}

// DeploymentSummary is a deployment without its log, for lists.
type DeploymentSummary struct {
	ID         uint                     `json:"id"`
	Status     models.DeploymentStatus  `json:"status"`
	Trigger    models.DeploymentTrigger `json:"trigger"`
	Ref        string                   `json:"ref"`
	Digest     *string                  `json:"digest"`
	CreatedAt  *carbon.DateTime         `json:"created_at"`
	StartedAt  *carbon.DateTime         `json:"started_at"`
	FinishedAt *carbon.DateTime         `json:"finished_at"`
}

// AppSummary is one row of the dashboard.
type AppSummary struct {
	ID             uint               `json:"id"`
	Name           string             `json:"name"`
	Repository     string             `json:"repository"`
	Domain         *string            `json:"domain"`
	TLS            bool               `json:"tls"`
	Template       *string            `json:"template"`
	Status         LiveStatus         `json:"status"`
	LastDeployment *DeploymentSummary `json:"last_deployment"`
	// Databases are the IDs of the apps this one is linked to (ADR 0028),
	// which the dashboard shows under it rather than as their own cards.
	Databases []uint `json:"databases"`
}

// AppSummaries lists every app with its live status, latest deployment
// and links.
func AppSummaries(ctx context.Context) ([]AppSummary, error) {
	var apps []models.App
	if err := facades.Orm().WithContext(ctx).Query().Order("name").Get(&apps); err != nil {
		return nil, err
	}
	latest, err := latestDeployments(ctx)
	if err != nil {
		return nil, err
	}
	links, err := allLinks(ctx)
	if err != nil {
		return nil, err
	}

	summaries := make([]AppSummary, 0, len(apps))
	for _, app := range apps {
		summaries = append(summaries, AppSummary{
			ID:             app.ID,
			Name:           app.Name,
			Repository:     app.Repository,
			Domain:         app.Domain,
			TLS:            app.TLS,
			Template:       app.Template,
			Status:         AppLiveStatus(ctx, &app),
			LastDeployment: latest[app.ID],
			Databases:      append([]uint{}, links[app.ID]...),
		})
	}

	return summaries, nil
}

// summaryColumns is every deployment column except the log, which can be
// long.
var summaryColumns = []string{"id", "app_id", "status", "trigger", "ref", "digest", "created_at", "started_at", "finished_at"}

// RecentDeployments lists the app's latest deployments, newest first.
func RecentDeployments(ctx context.Context, appID uint, limit int) ([]DeploymentSummary, error) {
	deployments := []DeploymentSummary{}
	err := facades.Orm().WithContext(ctx).Query().Model(&models.Deployment{}).Select(summaryColumns...).
		Where("app_id", appID).Order("id desc").Limit(limit).Get(&deployments)

	return deployments, err
}

// LatestDeployment returns the app's most recent deployment, or nil.
func LatestDeployment(ctx context.Context, appID uint) (*DeploymentSummary, error) {
	var deployments []DeploymentSummary
	err := facades.Orm().WithContext(ctx).Query().Model(&models.Deployment{}).Select(summaryColumns...).
		Where("app_id", appID).Order("id desc").Limit(1).Get(&deployments)
	if err != nil || len(deployments) == 0 {
		return nil, err
	}

	return &deployments[0], nil
}

// latestDeployments maps each app to its most recent deployment.
func latestDeployments(ctx context.Context) (map[uint]*DeploymentSummary, error) {
	var rows []struct {
		DeploymentSummary
		AppID uint
	}
	err := facades.Orm().WithContext(ctx).Query().Model(&models.Deployment{}).Select(summaryColumns...).
		Where("id IN (SELECT MAX(id) FROM deployments GROUP BY app_id)").Get(&rows)
	if err != nil {
		return nil, err
	}

	latest := make(map[uint]*DeploymentSummary, len(rows))
	for i := range rows {
		latest[rows[i].AppID] = &rows[i].DeploymentSummary
	}

	return latest, nil
}
