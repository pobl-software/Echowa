package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

// M20261027090000AddSlowQueriesToApps adds the switch that has a template
// database collect its slow queries (ADR 0027). It has a default, so
// SQLite adds it in place.
type M20261027090000AddSlowQueriesToApps struct{}

// Signature The unique signature for the migration.
func (r *M20261027090000AddSlowQueriesToApps) Signature() string {
	return "20261027090000_add_slow_queries_to_apps"
}

// Up Run the migrations.
func (r *M20261027090000AddSlowQueriesToApps) Up() error {
	if facades.Schema().HasColumn("apps", "slow_queries") {
		return nil
	}

	return facades.Schema().Table("apps", func(table schema.Blueprint) {
		table.Boolean("slow_queries").Default(false)
	})
}

// Down Reverse the migrations.
func (r *M20261027090000AddSlowQueriesToApps) Down() error {
	return facades.Schema().Table("apps", func(table schema.Blueprint) {
		table.DropColumn("slow_queries")
	})
}
