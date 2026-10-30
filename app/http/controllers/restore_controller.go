package controllers

import (
	"errors"
	"fmt"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/backups"
	"goravel/app/deploy"
	"goravel/app/facades"
	"goravel/app/inertia"
	"goravel/app/models"
	"goravel/app/rules"
	"goravel/app/services"
	"goravel/app/templates"
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
	if err := facades.Orm().WithContext(ctx.Context()).Query().Where("id", ctx.Request().QueryInt("location")).FirstOrFail(&loc); err != nil {
		return notFound(ctx)
	}
	list, err := services.ListBucketBackups(ctx.Context(), &app, &loc)
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
	src, err := restoreSource(ctx, &app, ctx.Request().QueryInt("backup"), ctx.Request().QueryInt("location"),
		ctx.Request().Query("key"), ctx.Request().Query("upload"))
	var refused *services.RestoreRefusal
	if errors.As(err, &refused) {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{"error": refused.Reason})
	}
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	preview, err := services.PreviewRestore(ctx.Context(), &app, src)
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
	validator, err := rules.Validate(ctx, rules.Restore(), rules.RestoreMessages())
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

	src, err := restoreSource(ctx, &app, ctx.Request().InputInt("backup"), ctx.Request().InputInt("location"),
		ctx.Request().Input("key"), ctx.Request().Input("upload"))
	var refused *services.RestoreRefusal
	if errors.As(err, &refused) {
		return failed(ctx, map[string]string{"source": refused.Reason})
	}
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	preview, err := services.PreviewRestore(ctx.Context(), &app, src)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if preview.Refusal != "" {
		return failed(ctx, map[string]string{"source": preview.Refusal})
	}
	// A backup in a bucket is backed up to the same location first. An
	// upload's safety backup goes where the form says.
	var safety *models.StorageLocation
	if ctx.Request().InputBool("safety") {
		safety = src.Location
		if src.Upload != nil {
			var loc models.StorageLocation
			err := facades.Orm().WithContext(ctx.Context()).Query().Where("id", ctx.Request().InputInt("safety_location")).FirstOrFail(&loc)
			if err != nil {
				return failed(ctx, map[string]string{"safety_location": "Choose where the safety backup goes."})
			}
			safety = &loc
		}
	}

	_, err = services.StartRestore(&app, src, safety)
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
	err := facades.Orm().WithContext(ctx.Context()).Query().
		Where("id", ctx.Request().RouteInt("restore")).Where("app_id", app.ID).FirstOrFail(&rec)
	if err != nil {
		return notFound(ctx)
	}

	return ctx.Response().Header("Cache-Control", "no-store").Json(http.StatusOK, rec)
}

// BrowseLocation lists, as JSON, the app folders in the queried location, or with
// a folder, the backups in it, for restoring into a new app.
func (r *RestoreController) BrowseLocation(ctx http.Context) http.Response {
	var loc models.StorageLocation
	if err := facades.Orm().WithContext(ctx.Context()).Query().Where("id", ctx.Request().QueryInt("location")).FirstOrFail(&loc); err != nil {
		return notFound(ctx)
	}
	var body http.Json
	var err error
	if folder := ctx.Request().Query("folder"); folder != "" {
		var list []services.BucketBackup
		list, err = services.FolderBackups(ctx.Context(), &loc, folder)
		body = http.Json{"backups": list}
	} else {
		var folders []string
		folders, err = services.LocationFolders(ctx.Context(), &loc)
		body = http.Json{"folders": folders}
	}
	if err != nil {
		return ctx.Response().Json(http.StatusBadGateway, http.Json{
			"error": fmt.Sprintf("Couldn't list %s: %s", loc.Name, backups.Describe(err)),
		})
	}

	return ctx.Response().Header("Cache-Control", "no-store").Json(http.StatusOK, body)
}

// PreviewNewApp describes, as JSON, the queried backup for a restore into
// a new app.
func (r *RestoreController) PreviewNewApp(ctx http.Context) http.Response {
	src, err := newAppSource(ctx, ctx.Request().QueryInt("backup"), ctx.Request().QueryInt("location"), ctx.Request().Query("key"))
	var refused *services.RestoreRefusal
	if errors.As(err, &refused) {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{"error": refused.Reason})
	}
	if err != nil {
		return inertia.ServerError(ctx, err)
	}

	return ctx.Response().Header("Cache-Control", "no-store").Json(http.StatusOK, services.PreviewNewApp(src))
}

// NewApp creates an app from the backup's template with the posted name
// and tag, queues its first deploy, and restores the backup into it once
// it's running.
func (r *RestoreController) NewApp(ctx http.Context) http.Response {
	validator, err := rules.Validate(ctx, rules.NewAppRestore(), rules.NewAppRestoreFilters())
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	if validator.Fails() {
		inertia.WithValidationErrors(ctx, validator.Errors())
		return inertia.Back(ctx)
	}
	// Bound from the validator, which has the filtered values.
	var form struct {
		Name string `form:"name" json:"name"`
		Tag  string `form:"tag" json:"tag"`
	}
	if err := validator.Bind(&form); err != nil {
		return inertia.ServerError(ctx, err)
	}
	name, tag := form.Name, form.Tag

	src, err := newAppSource(ctx, ctx.Request().InputInt("backup"), ctx.Request().InputInt("location"), ctx.Request().Input("key"))
	var refused *services.RestoreRefusal
	if errors.As(err, &refused) {
		return failed(ctx, map[string]string{"source": refused.Reason})
	}
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	tmpl, _ := templates.Find(src.Manifest.Template)
	if err := services.CheckRestoreTag(tmpl, src.Manifest, tag); errors.As(err, &refused) {
		return failed(ctx, map[string]string{"tag": refused.Reason})
	}
	leftover, err := services.LeftoverVolume(ctx.Context(), templates.VolumeNames(&tmpl, name))
	if err != nil {
		return failed(ctx, map[string]string{"name": "Couldn't check Docker for this app's volume: " + err.Error()})
	}
	if leftover != "" {
		return failed(ctx, map[string]string{"name": fmt.Sprintf(
			"The volume %s is left from an earlier app called %s and may hold its data. Pick another name, or remove it on the server with docker volume rm %s.",
			leftover, name, leftover)})
	}

	in := services.TemplateInput(tmpl, name)
	in.DefaultTag = tag
	app, err := services.CreateApp(ctx.Context(), in, &tmpl)
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	_, err = services.StartNewAppRestore(app, src, func() (uint, error) {
		d, err := deploy.Request(ctx.Context(), app, models.TriggerManual, tag)
		if err != nil {
			return 0, err
		}
		return d.ID, nil
	})
	if err != nil {
		return inertia.ServerError(ctx, err)
	}
	inertia.Flash(ctx, "success", fmt.Sprintf("Created %s. Deploying %s %s, then restoring %s into it.",
		app.Name, tmpl.Name, tag, src.Manifest.Object))

	return inertia.Redirect(ctx, backupsURL(app))
}

// newAppSource is the successful backup with ID backupID, of any app, or
// else the manifest at key in the location with ID locationID.
func newAppSource(ctx http.Context, backupID, locationID int, key string) (*services.RestoreSource, error) {
	if backupID > 0 {
		var rec models.Backup
		err := facades.Orm().WithContext(ctx.Context()).Query().With("StorageLocation").
			Where("id", backupID).Where("status", models.BackupSucceeded).First(&rec)
		if err != nil {
			return nil, err
		}
		if rec.ID == 0 || rec.StorageLocation == nil {
			return nil, &services.RestoreRefusal{Reason: "That backup isn't there any more."}
		}
		return services.FolderSource(ctx.Context(), rec.StorageLocation, backups.ManifestKey(rec.Key))
	}
	var loc models.StorageLocation
	if err := facades.Orm().WithContext(ctx.Context()).Query().Where("id", locationID).First(&loc); err != nil {
		return nil, err
	}
	if loc.ID == 0 {
		return nil, &services.RestoreRefusal{Reason: "Choose a backup to restore."}
	}

	return services.FolderSource(ctx.Context(), &loc, key)
}

// restoreSource is the app's upload with ID upload, the backup with ID
// backupID, or else the manifest at key in the location with ID
// locationID.
func restoreSource(ctx http.Context, app *models.App, backupID, locationID int, key, upload string) (*services.RestoreSource, error) {
	if upload != "" {
		return services.UploadSource(app, upload)
	}
	if backupID > 0 {
		return services.BackupSource(ctx.Context(), app, uint(backupID))
	}
	var loc models.StorageLocation
	if err := facades.Orm().WithContext(ctx.Context()).Query().Where("id", locationID).First(&loc); err != nil {
		return nil, err
	}
	if loc.ID == 0 {
		return nil, &services.RestoreRefusal{Reason: "Choose a backup to restore."}
	}

	return services.BucketSource(ctx.Context(), app, &loc, key)
}
