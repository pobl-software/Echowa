package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

type M20261023210703CreateDeploymentsTable struct{}

// Signature The unique signature for the migration.
func (r *M20261023210703CreateDeploymentsTable) Signature() string {
	return "20261023210703_create_deployments_table"
}

// Up Run the migrations.
func (r *M20261023210703CreateDeploymentsTable) Up() error {
	if !facades.Schema().HasTable("deployments") {
		return facades.Schema().Create("deployments", func(table schema.Blueprint) {
			table.ID()
			table.UnsignedBigInteger("app_id")
			table.String("status")
			table.String("trigger")
			// What was asked for: a tag or a sha256 digest.
			table.String("ref")
			// What it resolved to: repository@sha256:...
			table.String("digest").Nullable()
			table.LongText("log").Default("")
			table.Text("error").Nullable()
			table.TimestampTz("started_at").Nullable()
			table.TimestampTz("finished_at").Nullable()
			table.TimestampsTz()
			table.Index("app_id", "id")
			table.Index("status")
			table.Foreign("app_id").References("id").On("apps").CascadeOnDelete()
		})
	}

	return nil
}

// Down Reverse the migrations.
func (r *M20261023210703CreateDeploymentsTable) Down() error {
	return facades.Schema().DropIfExists("deployments")
}
