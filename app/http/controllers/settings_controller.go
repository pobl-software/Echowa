package controllers

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/goravel/framework/contracts/http"
	gonertia "github.com/romsar/gonertia/v3"

	"goravel/app/facades"
	"goravel/app/inertia"
	"goravel/app/models"
	"goravel/app/release"
	"goravel/app/services"
)

// SettingsController is the Settings page: updates, S3 storage and image
// pruning.
type SettingsController struct{}

func NewSettingsController() *SettingsController {
	return &SettingsController{}
}

func (r *SettingsController) Index(ctx http.Context) http.Response {
	cfg := services.PruneSettings()
	current, last := services.PruneState()
	locations := []models.StorageLocation{}
	if err := facades.Orm().WithContext(ctx).Query().Order("name").Get(&locations); err != nil {
		return inertia.ServerError(ctx, err)
	}

	return inertia.Render(ctx, "Settings/Index", gonertia.Props{
		"update":           updateProps(),
		"storageLocations": locations,
		"storagePrefix":    services.SuggestedStoragePrefix(),
		"prune": map[string]any{
			"keep":    cfg.Keep,
			"min_age": humanDuration(cfg.MinAge),
			"running": current,
			"last":    last,
		},
	})
}

// Prune starts a prune in the background. Removing images can take longer
// than the request timeout, so the page polls until it's done.
func (r *SettingsController) Prune(ctx http.Context) http.Response {
	err := services.StartPrune(services.PruneManual)
	switch {
	case errors.Is(err, services.ErrPruneRunning):
		inertia.Flash(ctx, "info", "A prune is already running.")
	case err != nil:
		return inertia.ServerError(ctx, err)
	default:
		inertia.Flash(ctx, "success", "Pruning images.")
	}

	return inertia.Redirect(ctx, "/settings")
}

func updateProps() map[string]any {
	requested, status, busy := services.UpdateInProgress()
	check := services.UpdateCheckState()

	return map[string]any{
		"current":   release.Version,
		"dev":       !release.IsRelease(release.Version),
		"check":     check,
		"available": check.Latest != nil && release.Newer(check.Latest.Version, release.Version),
		"requested": requested,
		"busy":      busy,
		"status":    status,
	}
}

// CheckForUpdate checks the release channel in the background; the page
// polls until it's done.
func (r *SettingsController) CheckForUpdate(ctx http.Context) http.Response {
	services.StartUpdateCheck()

	return inertia.Redirect(ctx, "/settings")
}

// Update asks the root updater to install the newest release (ADR 0021).
// The page then follows the updater's progress.
func (r *SettingsController) Update(ctx http.Context) http.Response {
	err := services.RequestUpdate(ctx, ctx.Request().Input("version"))
	switch {
	case errors.Is(err, services.ErrDevBuild), errors.Is(err, services.ErrNotNewest),
		errors.Is(err, services.ErrDeploysRunning), errors.Is(err, services.ErrUpdating):
		inertia.Flash(ctx, "error", capitalise(err.Error())+".")
	case err != nil:
		return inertia.ServerError(ctx, err)
	default:
		inertia.Flash(ctx, "success", "Update requested. Echowa restarts when it's ready.")
	}

	return inertia.Redirect(ctx, "/settings")
}

func capitalise(s string) string {
	if s == "" {
		return s
	}

	return strings.ToUpper(s[:1]) + s[1:]
}

// humanDuration writes whole days or hours as words ("3 days"), and
// anything else as Go does ("90m0s").
func humanDuration(d time.Duration) string {
	plural := func(n int64, unit string) string {
		if n == 1 {
			return "1 " + unit
		}
		return fmt.Sprintf("%d %ss", n, unit)
	}
	switch {
	case d%(24*time.Hour) == 0:
		return plural(int64(d/(24*time.Hour)), "day")
	case d%time.Hour == 0:
		return plural(int64(d/time.Hour), "hour")
	default:
		return d.String()
	}
}
