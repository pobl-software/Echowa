package routes

import (
	"fmt"
	"io/fs"
	nethttp "net/http"
	"runtime/debug"

	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/route"
	frameworkmiddleware "github.com/goravel/framework/http/middleware"
	sessionmiddleware "github.com/goravel/framework/session/middleware"

	"goravel/app/assets"
	"goravel/app/facades"
	"goravel/app/http/controllers"
	"goravel/app/http/controllers/auth"
	"goravel/app/http/middleware"
	"goravel/app/inertia"
	"goravel/app/release"
	"goravel/app/services"
)

func Web() {
	// A panic logs what was asked for, the panic and the stack. Goravel's
	// default logs the request's headers and body too, which carry the
	// session cookie and any password, token or env value being submitted.
	facades.Route().Recover(func(ctx http.Context, err any) {
		// A stream that can't finish cuts the connection off this way,
		// so the browser sees a failed download rather than a whole one.
		if err == nethttp.ErrAbortHandler {
			panic(err)
		}
		facades.Log().Error(fmt.Errorf("panic serving %s %s: %v\n%s", ctx.Request().Method(), ctx.Request().OriginPath(), err, debug.Stack()))
		ctx.Request().Abort(http.StatusInternalServerError)
	})

	// Built frontend assets, from the binary in a release build.
	facades.Route().StaticFS("build", assets.Build())
	facades.Route().Get("/favicon.ico", func(ctx http.Context) http.Response {
		icon, err := fs.ReadFile(assets.FS(), assets.Favicon)
		if err != nil {
			return inertia.NotFound(ctx)
		}
		return ctx.Response().Header("Cache-Control", "public, max-age=86400").Data(http.StatusOK, "image/x-icon", icon)
	})

	// Health check, outside the session stack. The version is only for
	// this machine: echowa setup and the updater wait for the new one.
	facades.Route().Get("/up", func(ctx http.Context) http.Response {
		body := map[string]any{"status": "ok"}
		if services.IsLocalRequest(ctx) {
			body["version"] = release.Version
		}
		return ctx.Response().Header("Cache-Control", "no-store").Json(http.StatusOK, body)
	})

	// The "web" stack: session, idle timeout, CSRF, then Inertia. Every
	// page and form goes in here. Webhooks (signed, not session-authenticated)
	// live outside it. Order matters; see ADR 0008.
	facades.Route().Middleware(
		sessionmiddleware.StartSession(),
		&middleware.SignOutIdleSessions{},
		&middleware.VerifyCsrfToken{},
		&middleware.HandleInertiaRequests{},
	).Group(func(router route.Router) {
		router.Get("/", func(ctx http.Context) http.Response {
			return ctx.Response().Redirect(http.StatusFound, "/dashboard")
		})

		// Guests only.
		router.Middleware(&middleware.RedirectIfAuthenticated{}).Group(func(router route.Router) {
			session := auth.NewAuthenticatedSessionController()
			router.Get("/login", session.Create)
			router.Middleware(frameworkmiddleware.Throttle("login")).Post("/login", session.Store)
			setup := auth.NewSetupController()
			router.Get("/setup", setup.Create)
			router.Middleware(frameworkmiddleware.Throttle("login")).Post("/setup", setup.Store)
			challenge := auth.NewTwoFactorChallengeController()
			router.Get("/two-factor-challenge", challenge.Create)
			router.Middleware(frameworkmiddleware.Throttle("two-factor")).Post("/two-factor-challenge", challenge.Store)
		})

		// Signed-in users only.
		router.Middleware(&middleware.Authenticate{}).Group(func(router route.Router) {
			router.Get("/dashboard", controllers.NewDashboardController().Index)
			router.Get("/server", controllers.NewServerController().Show)
			alarms := controllers.NewAlarmController()
			router.Get("/alarms", alarms.Index)
			router.Post("/alarms/read", alarms.ReadAll)
			router.Post("/alarms/{alarm}/read", alarms.Read)
			router.Post("/logout", auth.NewAuthenticatedSessionController().Destroy)

			apps := controllers.NewAppController()
			router.Get("/apps/create", apps.Create)
			router.Post("/apps", apps.Store)
			router.Get("/apps/{app}", apps.Show)
			router.Put("/apps/{app}", apps.Update)
			router.Put("/apps/{app}/domain", apps.UpdateDomain)
			router.Put("/apps/{app}/resources", apps.UpdateResources)
			router.Delete("/apps/{app}/domain", apps.DestroyDomain)
			router.Post("/apps/{app}/domain/check", apps.CheckDomain)
			router.Delete("/apps/{app}", apps.Destroy)
			router.Put("/apps/{app}/env", apps.EnvUpdate)
			router.Post("/apps/{app}/env/import", apps.EnvImport)
			router.Post("/apps/{app}/env/reveal", apps.EnvReveal)
			router.Post("/apps/{app}/connect/reveal", apps.ConnectReveal)
			router.Post("/apps/{app}/deploy", apps.Deploy)
			webhook := controllers.NewAppWebhookController()
			router.Post("/apps/{app}/webhook/reveal", webhook.Reveal)
			router.Post("/apps/{app}/webhook/rotate", webhook.Rotate)
			appStop := controllers.NewAppStopController()
			router.Post("/apps/{app}/stop", appStop.Stop)
			router.Post("/apps/{app}/start", appStop.Start)
			appDatabases := controllers.NewAppDatabasesController()
			router.Post("/apps/{app}/databases", appDatabases.Store)
			router.Delete("/apps/{app}/databases/{database}", appDatabases.Destroy)
			appServices := controllers.NewAppServicesController()
			router.Post("/apps/{app}/services", appServices.Store)
			router.Post("/apps/{app}/services/link", appServices.Link)
			router.Delete("/apps/{app}/services/{service}", appServices.Destroy)
			appVolumes := controllers.NewAppVolumeController()
			router.Post("/apps/{app}/volumes", appVolumes.Store)
			router.Put("/apps/{app}/volumes/{volume}", appVolumes.Update)
			router.Delete("/apps/{app}/volumes/{volume}", appVolumes.Destroy)
			externalAccess := controllers.NewExternalAccessController()
			router.Post("/apps/{app}/external-access", externalAccess.Store)
			router.Delete("/apps/{app}/external-access", externalAccess.Destroy)
			router.Get("/apps/{app}/external-access/ca.crt", externalAccess.CA)
			slowQueries := controllers.NewSlowQueriesController()
			router.Post("/apps/{app}/slow-queries", slowQueries.Store)
			router.Delete("/apps/{app}/slow-queries", slowQueries.Destroy)
			router.Post("/apps/{app}/slow-queries/reset", slowQueries.Reset)
			pooling := controllers.NewPoolingController()
			router.Post("/apps/{app}/pooling", pooling.Store)
			router.Delete("/apps/{app}/pooling", pooling.Destroy)
			tasks := controllers.NewScheduledTaskController()
			router.Post("/apps/{app}/tasks", tasks.Store)
			router.Put("/apps/{app}/tasks/{task}", tasks.Update)
			router.Put("/apps/{app}/tasks/{task}/enabled", tasks.Enabled)
			router.Delete("/apps/{app}/tasks/{task}", tasks.Destroy)
			router.Post("/apps/{app}/tasks/{task}/run", tasks.Run)
			router.Post("/apps/{app}/tasks/{task}/dismiss", tasks.Dismiss)
			router.Get("/apps/{app}/tasks/{task}/runs", tasks.Runs)
			router.Get("/apps/{app}/tasks/{task}/runs/{run}", tasks.ShowRun)
			router.Get("/scheduled-tasks/preview", tasks.Preview)
			appBackups := controllers.NewBackupController()
			router.Post("/apps/{app}/backups", appBackups.Store)
			router.Get("/apps/{app}/backups/{backup}", appBackups.Show)
			router.Delete("/apps/{app}/backups/{backup}", appBackups.Destroy)
			backupSchedules := controllers.NewBackupScheduleController()
			router.Post("/apps/{app}/backup-schedules", backupSchedules.Store)
			router.Put("/apps/{app}/backup-schedules/{schedule}", backupSchedules.Update)
			router.Put("/apps/{app}/backup-schedules/{schedule}/enabled", backupSchedules.Enabled)
			router.Delete("/apps/{app}/backup-schedules/{schedule}", backupSchedules.Destroy)
			router.Post("/apps/{app}/backup-schedules/{schedule}/dismiss", backupSchedules.Dismiss)
			restores := controllers.NewRestoreController()
			router.Post("/apps/{app}/restores", restores.Store)
			router.Get("/apps/{app}/restores/browse", restores.Browse)
			router.Get("/apps/{app}/restores/preview", restores.Preview)
			router.Get("/apps/{app}/restores/{restore}", restores.Show)
			// The chunks of an upload are in routes/streams.go.
			uploads := controllers.NewRestoreUploadController()
			router.Post("/apps/{app}/restores/uploads", uploads.Store)
			router.Get("/apps/{app}/restores/uploads/{upload}", uploads.Show)
			router.Delete("/apps/{app}/restores/uploads/{upload}", uploads.Destroy)
			router.Post("/apps/restore", restores.NewApp)
			router.Get("/backups/browse", restores.BrowseLocation)
			router.Get("/backups/preview", restores.PreviewNewApp)
			deployments := controllers.NewDeploymentController()
			router.Get("/apps/{app}/deployments/{deployment}", deployments.Show)
			router.Post("/apps/{app}/deployments/{deployment}/redeploy", deployments.Redeploy)

			// Settings: an overview, then a page per section.
			settings := controllers.NewSettingsController()
			router.Get("/settings", settings.Index)
			router.Get("/settings/updates", settings.Updates)
			router.Post("/settings/update/check", settings.CheckForUpdate)
			router.Post("/settings/update", settings.Update)
			router.Get("/settings/pruning", settings.Pruning)
			router.Post("/settings/prune", settings.Prune)
			router.Get("/settings/storage", settings.Storage)
			router.Get("/settings/notifications", settings.Notifications)
			router.Get("/settings/alerts", settings.Alerts)
			security := controllers.NewSecurityController()
			router.Get("/settings/security", security.Show)
			router.Put("/settings/security/session-timeout", security.UpdateSessionTimeout)
			router.Post("/settings/security/two-factor/setup", security.Setup)
			router.Middleware(frameworkmiddleware.Throttle("two-factor")).Post("/settings/security/two-factor", security.Confirm)
			router.Middleware(frameworkmiddleware.Throttle("two-factor")).Delete("/settings/security/two-factor", security.Destroy)

			registries := controllers.NewRegistryController()
			router.Get("/settings/registries", registries.Index)
			router.Get("/settings/registries/create", registries.Create)
			router.Post("/settings/registries", registries.Store)
			router.Get("/settings/registries/{registry}/edit", registries.Edit)
			router.Put("/settings/registries/{registry}", registries.Update)
			router.Delete("/settings/registries/{registry}", registries.Destroy)
			router.Post("/settings/registries/{registry}/test", registries.Test)

			storage := controllers.NewStorageLocationController()
			router.Post("/settings/storage", storage.Store)
			router.Post("/settings/storage/test", storage.Check)
			router.Put("/settings/storage/{location}", storage.Update)
			router.Delete("/settings/storage/{location}", storage.Destroy)

			notifications := controllers.NewNotificationProviderController()
			router.Post("/settings/notifications", notifications.Store)
			router.Post("/settings/notifications/test", notifications.Test)
			router.Get("/settings/notifications/test/{test}", notifications.TestResult)
			router.Put("/settings/notifications/{provider}", notifications.Update)
			router.Put("/settings/notifications/{provider}/enabled", notifications.Enabled)
			router.Delete("/settings/notifications/{provider}", notifications.Destroy)

			// Alerts (ADR 0040): an app's own in its Alerts section, the
			// server's and every app's on Settings. One controller, which
			// tells them apart by {app}.
			alertRules := controllers.NewAlertRuleController()
			for _, prefix := range []string{"/apps/{app}/alerts", "/settings/alerts"} {
				router.Post(prefix, alertRules.Store)
				router.Post(prefix+"/suggested", alertRules.Suggested)
				router.Put(prefix+"/{rule}", alertRules.Update)
				router.Put(prefix+"/{rule}/enabled", alertRules.Enabled)
				router.Delete(prefix+"/{rule}", alertRules.Destroy)
			}
		})
	})

	Streams()
	Hooks()

	// Gin's built-in 404 runs after the global Timeout middleware has already
	// written a 200 from its buffer, so unknown URLs came back as empty 200s.
	// A fallback route sets the status itself.
	facades.Route().Fallback(inertia.NotFound)
}
