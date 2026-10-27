package bootstrap

import (
	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/foundation"

	"goravel/config"
	"goravel/routes"
)

func Boot() contractsfoundation.Application {
	return foundation.Setup().
		WithRules(Rules).
		WithCommands(Commands).
		WithMigrations(Migrations).
		WithRouting(routes.Web).
		WithMiddleware(routes.GlobalMiddleware).
		WithProviders(Providers).
		WithConfig(config.Boot).
		WithJobs(Jobs).
		WithRunners(Runners).
		WithSchedule(Schedule).
		WithCallback(routes.RateLimiters).
		Create()
}
