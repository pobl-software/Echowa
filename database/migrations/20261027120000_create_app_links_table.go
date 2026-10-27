package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

// M20261027120000CreateAppLinksTable links an app to the apps it uses,
// such as its databases (ADR 0028). A link goes when either app does.
type M20261027120000CreateAppLinksTable struct{}

// Signature The unique signature for the migration.
func (r *M20261027120000CreateAppLinksTable) Signature() string {
	return "20261027120000_create_app_links_table"
}

// Up Run the migrations.
func (r *M20261027120000CreateAppLinksTable) Up() error {
	if !facades.Schema().HasTable("app_links") {
		return facades.Schema().Create("app_links", func(table schema.Blueprint) {
			table.ID()
			// The app that uses linked_app_id.
			table.UnsignedBigInteger("app_id")
			table.UnsignedBigInteger("linked_app_id")
			table.TimestampsTz()
			table.Unique("app_id", "linked_app_id")
			table.Index("linked_app_id")
			table.Foreign("app_id").References("id").On("apps").CascadeOnDelete()
			table.Foreign("linked_app_id").References("id").On("apps").CascadeOnDelete()
		})
	}

	return nil
}

// Down Reverse the migrations.
func (r *M20261027120000CreateAppLinksTable) Down() error {
	return facades.Schema().DropIfExists("app_links")
}
