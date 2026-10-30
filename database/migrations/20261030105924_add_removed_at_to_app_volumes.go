package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

// M20261030105924AddRemovedAtToAppVolumes adds the time a custom app's
// volume was marked for removal (ADR 0039). It's nullable, so SQLite adds
// it in place, and an older release after a rollback ignores it and mounts
// the volume again.
type M20261030105924AddRemovedAtToAppVolumes struct{}

// Signature The unique signature for the migration.
func (r *M20261030105924AddRemovedAtToAppVolumes) Signature() string {
	return "20261030105924_add_removed_at_to_app_volumes"
}

// Up Run the migrations.
func (r *M20261030105924AddRemovedAtToAppVolumes) Up() error {
	if facades.Schema().HasColumn("app_volumes", "removed_at") {
		return nil
	}

	return facades.Schema().Table("app_volumes", func(table schema.Blueprint) {
		table.TimestampTz("removed_at").Nullable()
	})
}

// Down Reverse the migrations.
func (r *M20261030105924AddRemovedAtToAppVolumes) Down() error {
	return facades.Schema().Table("app_volumes", func(table schema.Blueprint) {
		table.DropColumn("removed_at")
	})
}
