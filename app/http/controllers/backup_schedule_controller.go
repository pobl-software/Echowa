package controllers

import (
	"fmt"
	"strings"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/facades"
	"goravel/app/inertia"
	"goravel/app/models"
	"goravel/app/rules"
	"goravel/app/services"
)

// BackupScheduleController is the Schedules panel of a template app's
// Backups section (ADR 0034).
type BackupScheduleController struct{}

func NewBackupScheduleController() *BackupScheduleController {
	return &BackupScheduleController{}
}

func (r *BackupScheduleController) Store(ctx http.Context) http.Response {
	var app models.App
	if resp := findBackupApp(ctx, &app); resp != nil {
		return resp
	}

	return r.save(ctx, &app, &models.BackupSchedule{AppID: app.ID}, "Added the schedule.")
}

func (r *BackupScheduleController) Update(ctx http.Context) http.Response {
	var app models.App
	var schedule models.BackupSchedule
	if resp := findBackupSchedule(ctx, &app, &schedule); resp != nil {
		return resp
	}

	return r.save(ctx, &app, &schedule, "Saved the schedule.")
}

func (r *BackupScheduleController) save(ctx http.Context, app *models.App, schedule *models.BackupSchedule, done string) http.Response {
	validator, err := ctx.Request().Validate(rules.BackupSchedule(), rules.BackupScheduleFilters(), rules.BackupScheduleMessages())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}
	var in services.BackupScheduleInput
	if err := validator.Bind(&in); err != nil {
		return inertia.ServerError(ctx, err)
	}
	if err := services.SaveBackupSchedule(ctx.Context(), schedule, in); err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", done)

	return inertia.Redirect(ctx, backupsURL(app))
}

// Enabled turns the schedule on or off.
func (r *BackupScheduleController) Enabled(ctx http.Context) http.Response {
	var app models.App
	var schedule models.BackupSchedule
	if resp := findBackupSchedule(ctx, &app, &schedule); resp != nil {
		return resp
	}
	validator, err := ctx.Request().Validate(map[string]any{"enabled": "required|boolean"})
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}
	enabled := ctx.Request().InputBool("enabled")
	if _, err := facades.Orm().WithContext(ctx.Context()).Query().Model(&schedule).Update("enabled", enabled); err != nil {
		return inertia.ServerError(ctx, err)
	}
	if enabled {
		inertia.Flash(ctx, "success", "The schedule backs "+app.Name+" up again.")
	} else {
		inertia.Flash(ctx, "success", "The schedule won't back "+app.Name+" up until you turn it back on.")
	}

	return inertia.Redirect(ctx, backupsURL(&app))
}

// Destroy deletes the schedule. Its backups stay, and retention no longer
// touches them. One that's running finishes.
func (r *BackupScheduleController) Destroy(ctx http.Context) http.Response {
	var app models.App
	var schedule models.BackupSchedule
	if resp := findBackupSchedule(ctx, &app, &schedule); resp != nil {
		return resp
	}
	if _, err := facades.Orm().WithContext(ctx.Context()).Query().Delete(&schedule); err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", "Deleted the schedule. Its backups are kept.")

	return inertia.Redirect(ctx, backupsURL(&app))
}

// Dismiss hides the Overview's alert for the posted backup, which must be
// this schedule's, and for the schedule's failures before it.
func (r *BackupScheduleController) Dismiss(ctx http.Context) http.Response {
	var app models.App
	var schedule models.BackupSchedule
	if resp := findBackupSchedule(ctx, &app, &schedule); resp != nil {
		return resp
	}
	var rec models.Backup
	err := facades.Orm().WithContext(ctx.Context()).Query().
		Where("id", ctx.Request().InputInt("backup")).Where("backup_schedule_id", schedule.ID).FirstOrFail(&rec)
	if err != nil {
		return notFound(ctx)
	}
	if err := services.DismissBackupFailure(ctx.Context(), &schedule, &rec); err != nil {
		return inertia.ServerError(ctx, err)
	}

	return inertia.Back(ctx)
}

// findBackupSchedule loads the {app} route's app, which must have backups,
// and its {schedule}.
func findBackupSchedule(ctx http.Context, app *models.App, schedule *models.BackupSchedule) http.Response {
	if resp := findBackupApp(ctx, app); resp != nil {
		return resp
	}
	err := facades.Orm().WithContext(ctx.Context()).Query().
		Where("id", ctx.Request().RouteInt("schedule")).Where("app_id", app.ID).FirstOrFail(schedule)
	if err != nil {
		return notFound(ctx)
	}

	return nil
}

// joinNames writes names as "a", "a and b" or "a, b and c".
func joinNames(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}

	return fmt.Sprintf("%s and %s", strings.Join(names[:len(names)-1], ", "), names[len(names)-1])
}
