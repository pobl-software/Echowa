package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

type M20261023210657CreateRegistriesTable struct{}

// Signature The unique signature for the migration.
func (r *M20261023210657CreateRegistriesTable) Signature() string {
	return "20261023210657_create_registries_table"
}

// Up Run the migrations.
func (r *M20261023210657CreateRegistriesTable) Up() error {
	if !facades.Schema().HasTable("registries") {
		return facades.Schema().Create("registries", func(table schema.Blueprint) {
			table.ID()
			table.String("name")
			table.String("host").Default("ghcr.io")
			table.String("username")
			// Encrypted with APP_KEY (models.Secret).
			table.Text("token")
			table.TimestampsTz()
			table.Unique("name")
		})
	}

	return nil
}

// Down Reverse the migrations.
func (r *M20261023210657CreateRegistriesTable) Down() error {
	return facades.Schema().DropIfExists("registries")
}
