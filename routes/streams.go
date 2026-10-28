package routes

import (
	contractsconfiguration "github.com/goravel/framework/contracts/foundation/configuration"
	"github.com/goravel/framework/contracts/route"
	"github.com/goravel/gin"

	"goravel/app/facades"
	"goravel/app/http/controllers"
	"goravel/app/http/middleware"
)

// Server-Sent Events routes, and a backup's download. They stay open as
// long as they need, so they skip Goravel's global request timeout
// (GlobalMiddleware) and use a session that's never saved
// (middleware.AuthenticateStream).
const (
	appLogsStream    = "/apps/{app}/logs/stream"
	deploymentStream = "/deployments/{deployment}/stream"
	backupDownload   = "/apps/{app}/backups/{backup}/download"
)

var streams = []string{appLogsStream, deploymentStream, backupDownload}

func Streams() {
	facades.Route().Middleware(&middleware.AuthenticateStream{}).Group(func(router route.Router) {
		stream := controllers.NewStreamController()
		router.Get(appLogsStream, stream.AppLogs)
		router.Get(deploymentStream, stream.Deployment)
		router.Get(backupDownload, controllers.NewBackupController().Download)
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
