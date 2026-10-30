package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

// M20261030200000AddClearSinceToAlertStates adds when a firing condition's
// figure stopped breaching (ADR 0040), so a restart while it clears still
// resolves it on time. It's nullable, so SQLite adds it in place.
type M20261030200000AddClearSinceToAlertStates struct{}

// Signature The unique signature for the migration.
func (r *M20261030200000AddClearSinceToAlertStates) Signature() string {
	return "20261030200000_add_clear_since_to_alert_states"
}

// Up Run the migrations.
func (r *M20261030200000AddClearSinceToAlertStates) Up() error {
	if facades.Schema().HasColumn("alert_states", "clear_since") {
		return nil
	}

	return facades.Schema().Table("alert_states", func(table schema.Blueprint) {
		table.TimestampTz("clear_since").Nullable()
	})
}

// Down Reverse the migrations.
func (r *M20261030200000AddClearSinceToAlertStates) Down() error {
	return facades.Schema().Table("alert_states", func(table schema.Blueprint) {
		table.DropColumn("clear_since")
	})
}
