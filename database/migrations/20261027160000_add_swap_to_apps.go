package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

// M20261027160000AddSwapToApps adds the swap each app's tasks may use past
// their memory limit. It defaults to none, so SQLite adds it in place.
type M20261027160000AddSwapToApps struct{}

// Signature The unique signature for the migration.
func (r *M20261027160000AddSwapToApps) Signature() string {
	return "20261027160000_add_swap_to_apps"
}

// Up Run the migrations.
func (r *M20261027160000AddSwapToApps) Up() error {
	if facades.Schema().HasColumn("apps", "swap_mib") {
		return nil
	}

	return facades.Schema().Table("apps", func(table schema.Blueprint) {
		table.Integer("swap_mib").Default(0)
	})
}

// Down Reverse the migrations.
func (r *M20261027160000AddSwapToApps) Down() error {
	return facades.Schema().Table("apps", func(table schema.Blueprint) {
		table.DropColumn("swap_mib")
	})
}
