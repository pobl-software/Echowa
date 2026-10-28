package controllers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/backups"
	"goravel/app/facades"
	"goravel/app/inertia"
	"goravel/app/models"
	"goravel/app/services"
)

// BackupController is a template app's Backups section (ADR 0034). Apps
// whose template has no Backup, custom apps and Redis, answer 404.
type BackupController struct{}

func NewBackupController() *BackupController {
	return &BackupController{}
}

// Store is Back up now, to the posted location.
func (r *BackupController) Store(ctx http.Context) http.Response {
	var app models.App
	if resp := findBackupApp(ctx, &app); resp != nil {
		return resp
	}
	var loc models.StorageLocation
	err := facades.Orm().WithContext(ctx).Query().Where("id", ctx.Request().InputInt("location")).FirstOrFail(&loc)
	if err != nil {
		return failed(ctx, map[string]string{"location": "Choose a storage location."})
	}
	_, err = services.BackUpNow(&app, &loc)
	switch {
	case errors.Is(err, services.ErrBackupRunning):
		inertia.Flash(ctx, "info", "A backup of "+app.Name+" is already running.")
	case err != nil:
		return inertia.ServerError(ctx, err)
	default:
		inertia.Flash(ctx, "success", fmt.Sprintf("Backing up %s to %s.", app.Name, loc.Name))
	}

	return inertia.Redirect(ctx, backupsURL(&app))
}

// Show is one backup with its output, as JSON.
func (r *BackupController) Show(ctx http.Context) http.Response {
	var app models.App
	var rec models.Backup
	if resp := findBackup(ctx, &app, &rec); resp != nil {
		return resp
	}

	return ctx.Response().Header("Cache-Control", "no-store").Json(http.StatusOK, rec)
}

// Destroy deletes the backup's objects from the bucket, then its record.
func (r *BackupController) Destroy(ctx http.Context) http.Response {
	var app models.App
	var rec models.Backup
	if resp := findBackup(ctx, &app, &rec); resp != nil {
		return resp
	}
	if rec.Status == models.BackupRunning {
		inertia.Flash(ctx, "info", "This backup is still running. Delete it once it has finished.")
		return inertia.Redirect(ctx, backupsURL(&app))
	}
	if err := services.DeleteBackup(ctx, &rec, rec.StorageLocation); err != nil {
		inertia.Flash(ctx, "error", fmt.Sprintf("Couldn't delete the backup from %s, so it was kept: %s", rec.StorageLocation.Name, backups.Describe(err)))
		return inertia.Redirect(ctx, backupsURL(&app))
	}
	inertia.Flash(ctx, "success", fmt.Sprintf("Deleted the backup of %s from %s.", app.Name, rec.StorageLocation.Name))

	return inertia.Redirect(ctx, backupsURL(&app))
}

// Download streams the dump from the bucket through Echowa, so the bucket
// needn't be reachable from the browser and no link to it is handed out.
// It's in routes/streams.go, past the request timeout.
func (r *BackupController) Download(ctx http.Context) http.Response {
	var app models.App
	var rec models.Backup
	if resp := findBackup(ctx, &app, &rec); resp != nil {
		return resp
	}
	if rec.Status != models.BackupSucceeded {
		return notFound(ctx)
	}
	body, err := backups.Open(rec.StorageLocation).Get(ctx, rec.Key)
	if errors.Is(err, backups.ErrNotFound) {
		return ctx.Response().String(http.StatusNotFound, "The dump isn't in "+rec.StorageLocation.Name+" any more.")
	}
	if err != nil {
		return ctx.Response().String(http.StatusBadGateway, "Couldn't read the dump from "+rec.StorageLocation.Name+": "+backups.Describe(err))
	}

	name := path.Base(rec.Key)
	ctx.Response().Header("Content-Type", "application/octet-stream")
	ctx.Response().Header("Content-Disposition", `attachment; filename="`+name+`"`)
	ctx.Response().Header("Content-Length", strconv.FormatInt(rec.SizeBytes, 10))
	ctx.Response().Header("Cache-Control", "no-store")

	return ctx.Response().Stream(http.StatusOK, func(w http.StreamWriter) error {
		defer func() { _ = body.Close() }()
		_, err := io.Copy(w, body)
		if err != nil && !errors.Is(err, context.Canceled) {
			facades.Log().Error(fmt.Errorf("download backup %d: %w", rec.ID, err))
		}
		return nil
	})
}

// findBackupApp loads the {app} route's app, or answers 404 for a missing
// one or one whose template has no backups.
func findBackupApp(ctx http.Context, app *models.App) http.Response {
	if resp := findOr404(ctx, "app", app); resp != nil {
		return resp
	}
	if services.AppBackup(app) == nil {
		return notFound(ctx)
	}

	return nil
}

// findBackup loads the {app} route's app and its {backup}, with the
// backup's location.
func findBackup(ctx http.Context, app *models.App, rec *models.Backup) http.Response {
	if resp := findBackupApp(ctx, app); resp != nil {
		return resp
	}
	err := facades.Orm().WithContext(ctx).Query().With("StorageLocation").
		Where("id", ctx.Request().RouteInt("backup")).Where("app_id", app.ID).FirstOrFail(rec)
	if err != nil || rec.StorageLocation == nil {
		return notFound(ctx)
	}

	return nil
}

func backupsURL(app *models.App) string {
	return fmt.Sprintf("/apps/%d?tab=backups", app.ID)
}
