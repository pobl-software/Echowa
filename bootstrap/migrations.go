package bootstrap

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/database/migrations"
)

func Migrations() []schema.Migration {
	return []schema.Migration{
		&migrations.M20210101000001CreateJobsTable{},
		&migrations.M20261017000444CreateUsersTable{},
		&migrations.M20261023210657CreateRegistriesTable{},
		&migrations.M20261023210700CreateAppsTable{},
		&migrations.M20261023210703CreateDeploymentsTable{},
		&migrations.M20261023210706CreateAppEnvVarsTable{},
		&migrations.M20261025234540AddTemplatesAndVolumes{},
		&migrations.M20261026011230AddExternalPortToApps{},
		&migrations.M20261027090000AddSlowQueriesToApps{},
		&migrations.M20261027120000CreateAppLinksTable{},
		&migrations.M20261027150000AddResourceLimitsToApps{},
		&migrations.M20261027160000AddSwapToApps{},
		&migrations.M20261027170000CreateScheduledTasksTables{},
		&migrations.M20261027180000UpdateRedisHealthcheck{},
	}
}
