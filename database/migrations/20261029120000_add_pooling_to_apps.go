package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

// M20261029120000AddPoolingToApps adds the switch that runs a PostgreSQL
// app's connection pooler (ADR 0035). It has a default, so SQLite adds it
// in place, and an older release after a rollback ignores it.
type M20261029120000AddPoolingToApps struct{}

// Signature The unique signature for the migration.
func (r *M20261029120000AddPoolingToApps) Signature() string {
	return "20261029120000_add_pooling_to_apps"
}

// Up Run the migrations.
func (r *M20261029120000AddPoolingToApps) Up() error {
	if facades.Schema().HasColumn("apps", "pooling") {
		return nil
	}

	return facades.Schema().Table("apps", func(table schema.Blueprint) {
		table.Boolean("pooling").Default(false)
	})
}

// Down Reverse the migrations.
func (r *M20261029120000AddPoolingToApps) Down() error {
	return facades.Schema().Table("apps", func(table schema.Blueprint) {
		table.DropColumn("pooling")
	})
}
