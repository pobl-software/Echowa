package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

// M20261028090000CreateStorageLocationsTable adds the S3 buckets database
// backups go to (ADR 0034).
type M20261028090000CreateStorageLocationsTable struct{}

// Signature The unique signature for the migration.
func (r *M20261028090000CreateStorageLocationsTable) Signature() string {
	return "20261028090000_create_storage_locations_table"
}

// Up Run the migrations.
func (r *M20261028090000CreateStorageLocationsTable) Up() error {
	if facades.Schema().HasTable("storage_locations") {
		return nil
	}

	return facades.Schema().Create("storage_locations", func(table schema.Blueprint) {
		table.ID()
		table.String("name")
		table.String("endpoint")
		table.String("region")
		table.String("bucket")
		table.String("prefix").Default("")
		table.String("access_key_id")
		// Encrypted with APP_KEY (models.Secret).
		table.Text("secret_key")
		table.Boolean("path_style").Default(false)
		table.TimestampsTz()
		table.Unique("name")
	})
}

// Down Reverse the migrations.
func (r *M20261028090000CreateStorageLocationsTable) Down() error {
	return facades.Schema().DropIfExists("storage_locations")
}
