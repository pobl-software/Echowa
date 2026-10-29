package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

// M20261028150000CreateRestoresTable adds restores of template databases
// from their backups (ADR 0034).
type M20261028150000CreateRestoresTable struct{}

// Signature The unique signature for the migration.
func (r *M20261028150000CreateRestoresTable) Signature() string {
	return "20261028150000_create_restores_table"
}

// Up Run the migrations.
func (r *M20261028150000CreateRestoresTable) Up() error {
	if facades.Schema().HasTable("restores") {
		return nil
	}

	return facades.Schema().Create("restores", func(table schema.Blueprint) {
		table.ID()
		table.UnsignedBigInteger("app_id")
		// None for an uploaded file.
		table.UnsignedBigInteger("storage_location_id").Nullable()
		// The dump restored, its whole key, prefix included, or an uploaded
		// file's name.
		table.String("key")
		// The backup's record, when Echowa has one.
		table.UnsignedBigInteger("backup_id").Nullable()
		table.UnsignedBigInteger("safety_backup_id").Nullable()
		// Whether the app was made for this restore (part 6).
		table.Boolean("new_app").Default(false)
		table.String("status")
		// The dump's size, from its manifest, and how much has been read.
		table.BigInteger("size_bytes").Default(0)
		table.BigInteger("read_bytes").Default(0)
		// The end of the restore command's stderr.
		table.Text("output").Default("")
		table.Text("message").Nullable()
		table.TimestampTz("started_at")
		table.TimestampTz("finished_at").Nullable()
		table.TimestampsTz()
		table.Index("app_id", "id")
		table.Foreign("app_id").References("id").On("apps").CascadeOnDelete()
		// Deleting a location forgets its restores, as it does its backups.
		table.Foreign("storage_location_id").References("id").On("storage_locations").CascadeOnDelete()
		table.Foreign("backup_id").References("id").On("backups").NullOnDelete()
		table.Foreign("safety_backup_id").References("id").On("backups").NullOnDelete()
	})
}

// Down Reverse the migrations.
func (r *M20261028150000CreateRestoresTable) Down() error {
	return facades.Schema().DropIfExists("restores")
}
