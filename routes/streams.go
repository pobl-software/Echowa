package routes

import (
	contractsconfiguration "github.com/goravel/framework/contracts/foundation/configuration"
	"github.com/goravel/framework/contracts/route"
	"github.com/goravel/gin"

	"goravel/app/facades"
	"goravel/app/http/controllers"
	"goravel/app/http/middleware"
)

// Server-Sent Events routes, a backup's download, a database export, the
// chunks of an upload to restore from and an app's terminal, a WebSocket.
// They stay open as long as they
// need, so they skip Goravel's global request timeout (GlobalMiddleware)
// and use a session that's never saved (middleware.AuthenticateStream).
// They also go without Goravel's response middleware, which keeps a copy
// of every byte written, so a stream would hold all it had sent in memory.
const (
	appLogsStream    = "/apps/{app}/logs/stream"
	deploymentStream = "/deployments/{deployment}/stream"
	backupDownload   = "/apps/{app}/backups/{backup}/download"
	appExport        = "/apps/{app}/export"
	restoreUpload    = "/apps/{app}/restores/uploads/{upload}"
	appTerminal      = "/apps/{app}/terminal"
)

var streams = []string{appLogsStream, deploymentStream, backupDownload, appExport, restoreUpload, appTerminal}

func Streams() {
	routes := facades.Route().Middleware(&middleware.AuthenticateStream{}).WithoutMiddleware(gin.ResponseMiddleware())
	routes.Group(func(router route.Router) {
		stream := controllers.NewStreamController()
		router.Get(appLogsStream, stream.AppLogs)
		router.Get(deploymentStream, stream.Deployment)
		appBackups := controllers.NewBackupController()
		router.Get(backupDownload, appBackups.Download)
		router.Get(appExport, appBackups.Export)
		// These two change things. A chunk checks the CSRF token as the web
		// group does. A WebSocket can't send one, so the terminal checks
		// its Origin instead.
		uploads := controllers.NewRestoreUploadController()
		router.Middleware(&middleware.VerifyCsrfToken{}).Put(restoreUpload, uploads.Update)
		router.Get(appTerminal, controllers.NewTerminalController().Show)
	})
}

// GlobalMiddleware lets the streams past Goravel's global request timeout.
func GlobalMiddleware(handler contractsconfiguration.Middleware) {
	global := handler.GetGlobalMiddleware()
	for i, m := range global {
		if timeout, ok := m.(*gin.TimeoutMiddleware); ok {
			global[i] = &middleware.SkipTimeout{Timeout: timeout, Paths: streams}
		}
	}
	handler.Use(append(global, &middleware.SecurityHeaders{})...)
}
