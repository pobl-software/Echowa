package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

// M20261027150000AddResourceLimitsToApps adds each app's CPU and memory
// limits. Both are nullable, so every app, old or new, starts on the
// defaults and SQLite adds the columns in place.
type M20261027150000AddResourceLimitsToApps struct{}

// Signature The unique signature for the migration.
func (r *M20261027150000AddResourceLimitsToApps) Signature() string {
	return "20261027150000_add_resource_limits_to_apps"
}

// Up Run the migrations.
func (r *M20261027150000AddResourceLimitsToApps) Up() error {
	if facades.Schema().HasColumn("apps", "cpu_millicores") {
		return nil
	}

	return facades.Schema().Table("apps", func(table schema.Blueprint) {
		table.Integer("cpu_millicores").Nullable()
		table.Integer("memory_mib").Nullable()
	})
}

// Down Reverse the migrations.
func (r *M20261027150000AddResourceLimitsToApps) Down() error {
	return facades.Schema().Table("apps", func(table schema.Blueprint) {
		table.DropColumn("cpu_millicores", "memory_mib")
	})
}
