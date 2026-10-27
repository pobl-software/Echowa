package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

// M20261026011230AddExternalPortToApps adds the host port a template
// database is published on (ADR 0026). It's nullable, so SQLite adds it in
// place, and unique, since two apps can't listen on one host port.
type M20261026011230AddExternalPortToApps struct{}

// Signature The unique signature for the migration.
func (r *M20261026011230AddExternalPortToApps) Signature() string {
	return "20261026011230_add_external_port_to_apps"
}

// Up Run the migrations.
func (r *M20261026011230AddExternalPortToApps) Up() error {
	if facades.Schema().HasColumn("apps", "external_port") {
		return nil
	}

	return facades.Schema().Table("apps", func(table schema.Blueprint) {
		// Null when the app isn't reachable from outside the server.
		table.Integer("external_port").Nullable()
		table.Unique("external_port")
	})
}

// Down Reverse the migrations.
func (r *M20261026011230AddExternalPortToApps) Down() error {
	return facades.Schema().Table("apps", func(table schema.Blueprint) {
		table.DropUnique("external_port")
		table.DropColumn("external_port")
	})
}
