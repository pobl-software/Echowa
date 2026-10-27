package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

type M20261023210706CreateAppEnvVarsTable struct{}

// Signature The unique signature for the migration.
func (r *M20261023210706CreateAppEnvVarsTable) Signature() string {
	return "20261023210706_create_app_env_vars_table"
}

// Up Run the migrations.
func (r *M20261023210706CreateAppEnvVarsTable) Up() error {
	if !facades.Schema().HasTable("app_env_vars") {
		return facades.Schema().Create("app_env_vars", func(table schema.Blueprint) {
			table.ID()
			table.UnsignedBigInteger("app_id")
			table.String("key")
			// Encrypted with APP_KEY (models.Secret).
			table.Text("value")
			table.TimestampsTz()
			table.Unique("app_id", "key")
			table.Foreign("app_id").References("id").On("apps").CascadeOnDelete()
		})
	}

	return nil
}

// Down Reverse the migrations.
func (r *M20261023210706CreateAppEnvVarsTable) Down() error {
	return facades.Schema().DropIfExists("app_env_vars")
}
