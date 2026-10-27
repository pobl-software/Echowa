package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

// M20261025234540AddTemplatesAndVolumes adds what app templates need
// (ADR 0025). The new apps columns are nullable, so SQLite adds them in
// place.
type M20261025234540AddTemplatesAndVolumes struct{}

// Signature The unique signature for the migration.
func (r *M20261025234540AddTemplatesAndVolumes) Signature() string {
	return "20261025234540_add_templates_and_volumes"
}

// Up Run the migrations.
func (r *M20261025234540AddTemplatesAndVolumes) Up() error {
	if !facades.Schema().HasColumn("apps", "template") {
		err := facades.Schema().Table("apps", func(table schema.Blueprint) {
			// The template's slug, e.g. "postgres". Null for a custom app.
			table.String("template").Nullable()
			// Container arguments as a JSON array. Null keeps the image's CMD.
			table.Text("args").Nullable()
		})
		if err != nil {
			return err
		}
	}

	if !facades.Schema().HasTable("app_volumes") {
		return facades.Schema().Create("app_volumes", func(table schema.Blueprint) {
			table.ID()
			table.UnsignedBigInteger("app_id")
			// The Docker volume's name, e.g. echowa-db-data.
			table.String("name")
			// Absolute path in the container.
			table.String("target")
			table.TimestampsTz()
			table.Unique("name")
			table.Unique("app_id", "target")
			table.Foreign("app_id").References("id").On("apps").CascadeOnDelete()
		})
	}

	return nil
}

// Down Reverse the migrations.
func (r *M20261025234540AddTemplatesAndVolumes) Down() error {
	if err := facades.Schema().DropIfExists("app_volumes"); err != nil {
		return err
	}

	return facades.Schema().Table("apps", func(table schema.Blueprint) {
		table.DropColumn("template", "args")
	})
}
