package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

type M20261017000444CreateUsersTable struct{}

// Signature The unique signature for the migration.
func (r *M20261017000444CreateUsersTable) Signature() string {
	return "20261017000444_create_users_table"
}

// Up Run the migrations.
func (r *M20261017000444CreateUsersTable) Up() error {
	if !facades.Schema().HasTable("users") {
		return facades.Schema().Create("users", func(table schema.Blueprint) {
			table.ID()
			table.String("name")
			table.String("email")
			table.TimestampTz("email_verified_at").Nullable()
			table.String("password")
			table.String("remember_token", 100).Nullable()
			table.TimestampsTz()
			table.Unique("email")
		})
	}

	return nil
}

// Down Reverse the migrations.
func (r *M20261017000444CreateUsersTable) Down() error {
	return facades.Schema().DropIfExists("users")
}
