package controllers

import (
	"errors"
	"fmt"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/backups"
	"goravel/app/facades"
	"goravel/app/inertia"
	"goravel/app/models"
	"goravel/app/rules"
	"goravel/app/services"
)

// RestoreController restores a template app's database from a backup, in
// place (ADR 0034).
type RestoreController struct{}

func NewRestoreController() *RestoreController {
	return &RestoreController{}
}

// Browse lists, as JSON, the backups in the app's folder of the queried
// location, including ones Echowa has no record of.
func (r *RestoreController) Browse(ctx http.Context) http.Response {
	var app models.App
	if resp := findBackupApp(ctx, &app); resp != nil {
		return resp
	}
	var loc models.StorageLocation
	if err := facades.Orm().WithContext(ctx).Query().Where("id", ctx.Request().QueryInt("location")).FirstOrFail(&loc); err != nil {
		return notFound(ctx)
	}
	list, err := services.ListBucketBackups(ctx, &app, &loc)
	if err != nil {
		return ctx.Response().Json(http.StatusBadGateway, http.Json{
			"error": fmt.Sprintf("Couldn't list %s: %s", loc.Name, backups.Describe(err)),
		})
	}

	return ctx.Response().Header("Cache-Control", "no-store").Json(http.StatusOK, http.Json{
		"folder":  services.AppFolder(&loc, &app),
		"backups": list,
	})
}

// Preview checks the queried source against the app, as JSON, for the
// restore dialog.
func (r *RestoreController) Preview(ctx http.Context) http.Response {
	var app models.App
	if resp := findBackupApp(ctx, &app); resp != nil {
		return resp
	}
	src, err := restoreSource(ctx, &app, ctx.Request().QueryInt("backup"), ctx.Request().QueryInt("location"), ctx.Request().Query("key"))
	var refused *services.RestoreRefusal
	if errors.As(err, &refused) {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{"error": refused.Reason})
	}
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	preview, err := services.PreviewRestore(ctx, &app, src)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}

	return ctx.Response().Header("Cache-Control", "no-store").Json(http.StatusOK, preview)
}

// Store starts the restore, once the app's name is typed. Anything that
// stops it is an error on the dialog.
func (r *RestoreController) Store(ctx http.Context) http.Response {
	var app models.App
	if resp := findBackupApp(ctx, &app); resp != nil {
		return resp
	}
	validator, err := ctx.Request().Validate(rules.Restore(), rules.RestoreMessages())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}
	if ctx.Request().Input("confirm") != app.Name {
		return failed(ctx, map[string]string{"confirm": "Type " + app.Name + " to confirm."})
	}

	src, err := restoreSource(ctx, &app, ctx.Request().InputInt("backup"), ctx.Request().InputInt("location"), ctx.Request().Input("key"))
	var refused *services.RestoreRefusal
	if errors.As(err, &refused) {
		return failed(ctx, map[string]string{"source": refused.Reason})
	}
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	preview, err := services.PreviewRestore(ctx, &app, src)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if preview.Refusal != "" {
		return failed(ctx, map[string]string{"source": preview.Refusal})
	}

	_, err = services.StartRestore(&app, src, ctx.Request().InputBool("safety"))
	if errors.As(err, &refused) {
		return failed(ctx, map[string]string{"source": refused.Reason})
	}
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", fmt.Sprintf("Restoring %s from %s.", app.Name, src.Manifest.Object))

	return inertia.Redirect(ctx, backupsURL(&app))
}

// Show is one restore with its output, as JSON.
func (r *RestoreController) Show(ctx http.Context) http.Response {
	var app models.App
	if resp := findBackupApp(ctx, &app); resp != nil {
		return resp
	}
	var rec models.Restore
	err := facades.Orm().WithContext(ctx).Query().
		Where("id", ctx.Request().RouteInt("restore")).Where("app_id", app.ID).FirstOrFail(&rec)
	if err != nil {
		return notFound(ctx)
	}

	return ctx.Response().Header("Cache-Control", "no-store").Json(http.StatusOK, rec)
}

// restoreSource is the backup with ID backupID, or else the manifest at
// key in the location with ID locationID.
func restoreSource(ctx http.Context, app *models.App, backupID, locationID int, key string) (*services.RestoreSource, error) {
	if backupID > 0 {
		return services.BackupSource(ctx, app, uint(backupID))
	}
	var loc models.StorageLocation
	if err := facades.Orm().WithContext(ctx).Query().Where("id", locationID).First(&loc); err != nil {
		return nil, err
	}
	if loc.ID == 0 {
		return nil, &services.RestoreRefusal{Reason: "Choose a backup to restore."}
	}

	return services.BucketSource(ctx, app, &loc, key)
}
