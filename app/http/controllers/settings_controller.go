package controllers

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/contracts/http"
	gonertia "github.com/romsar/gonertia/v3"

	"goravel/app/facades"
	"goravel/app/inertia"
	"goravel/app/models"
	"goravel/app/release"
	"goravel/app/services"
)

// SettingsController is Settings: an overview of each section, then a page
// per section. Registries have their own controller.
type SettingsController struct{}

func NewSettingsController() *SettingsController {
	return &SettingsController{}
}

// Index is the overview: the running version, and a line per section
// saying whether it needs anything.
func (r *SettingsController) Index(ctx http.Context) http.Response {
	query := func() orm.Query { return facades.Orm().WithContext(ctx.Context()).Query() }
	registries, err := query().Model(&models.Registry{}).Count()
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	pulling, err := query().Model(&models.App{}).Where("registry_id IS NOT NULL").Count()
	if err != nil {
		return inertia.ServerError(ctx, err)
	}

	locations, err := services.StorageLocations(ctx.Context())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	var backups int64
	for _, loc := range locations {
		backups += loc.Backups
	}
	// The apps whose template Echowa backs up, which need a location.
	var templated []models.App
	if err := query().Where("template IS NOT NULL").Get(&templated); err != nil {
		return inertia.ServerError(ctx, err)
	}
	databases := 0
	for i := range templated {
		if services.AppBackup(&templated[i]) != nil {
			databases++
		}
	}

	providers, err := services.NotificationProviders(ctx.Context())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	enabled, failing := 0, []string{}
	for _, p := range providers {
		if !p.Enabled {
			continue
		}
		enabled++
		if p.Last != nil && p.Last.Status == models.NotificationFailed {
			failing = append(failing, p.Name)
		}
	}

	alertRules, err := services.SettingsAlertsSection(ctx.Context())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	user, err := services.CurrentUser(ctx)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}

	on := 0
	for _, rule := range alertRules.Rules {
		if rule.Enabled {
			on++
		}
	}

	return inertia.Render(ctx, "Settings/Index", gonertia.Props{
		"update":     updateProps(),
		"registries": map[string]any{"count": registries, "apps": pulling},
		"storage": map[string]any{
			"locations": len(locations),
			"backups":   backups,
			"databases": databases,
		},
		"notifications": map[string]any{
			"providers": len(providers),
			"enabled":   enabled,
			"failing":   failing,
		},
		"alerts": map[string]any{
			"rules":       len(alertRules.Rules),
			"enabled":     on,
			"firing":      len(alertRules.Firing),
			"suggestions": len(alertRules.Suggestions),
		},
		"prune": pruneProps(),
		"security": map[string]any{
			"twoFactor":      twoFactorProps(user),
			"sessionTimeout": sessionTimeoutProps(user)["label"],
		},
	})
}

func (r *SettingsController) Updates(ctx http.Context) http.Response {
	return inertia.Render(ctx, "Settings/Updates", gonertia.Props{"update": updateProps()})
}

func (r *SettingsController) Storage(ctx http.Context) http.Response {
	locations, err := services.StorageLocations(ctx.Context())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}

	return inertia.Render(ctx, "Settings/Storage", gonertia.Props{
		"storageLocations": locations,
		"storagePrefix":    services.SuggestedStoragePrefix(),
	})
}

func (r *SettingsController) Notifications(ctx http.Context) http.Response {
	providers, err := services.NotificationProviders(ctx.Context())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}

	return inertia.Render(ctx, "Settings/Notifications", gonertia.Props{"notificationProviders": providers})
}

// Alerts is the server's alerts and every app's (ADR 0040). An app's own
// are in its Alerts section.
func (r *SettingsController) Alerts(ctx http.Context) http.Response {
	providers, err := services.NotificationProviders(ctx.Context())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	alertRules, err := services.SettingsAlertsSection(ctx.Context())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	refs := make([]services.ProviderRef, 0, len(providers))
	for _, p := range providers {
		refs = append(refs, services.ProviderRef{ID: p.ID, Name: p.Name, Kind: p.Kind, Enabled: p.Enabled})
	}

	return inertia.Render(ctx, "Settings/Alerts", gonertia.Props{"alerts": alertRules, "providers": refs})
}

func (r *SettingsController) Pruning(ctx http.Context) http.Response {
	return inertia.Render(ctx, "Settings/Pruning", gonertia.Props{"prune": pruneProps()})
}

func pruneProps() map[string]any {
	cfg := services.PruneSettings()
	current, last := services.PruneState()

	return map[string]any{
		"keep":    cfg.Keep,
		"min_age": humanDuration(cfg.MinAge),
		"running": current,
		"last":    last,
	}
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

	return inertia.Redirect(ctx, "/settings/pruning")
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

	return inertia.Redirect(ctx, "/settings/updates")
}

// Update asks the root updater to install the newest release (ADR 0021).
// The page then follows the updater's progress.
func (r *SettingsController) Update(ctx http.Context) http.Response {
	err := services.RequestUpdate(ctx.Context(), ctx.Request().Input("version"))
	switch {
	case errors.Is(err, services.ErrDevBuild), errors.Is(err, services.ErrNotNewest),
		errors.Is(err, services.ErrDeploysRunning), errors.Is(err, services.ErrUpdating):
		inertia.Flash(ctx, "error", capitalise(err.Error())+".")
	case err != nil:
		return inertia.ServerError(ctx, err)
	default:
		inertia.Flash(ctx, "success", "Update requested. Echowa restarts when it's ready.")
	}

	return inertia.Redirect(ctx, "/settings/updates")
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
