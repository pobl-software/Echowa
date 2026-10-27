package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

type M20261023210700CreateAppsTable struct{}

// Signature The unique signature for the migration.
func (r *M20261023210700CreateAppsTable) Signature() string {
	return "20261023210700_create_apps_table"
}

// Up Run the migrations.
func (r *M20261023210700CreateAppsTable) Up() error {
	if !facades.Schema().HasTable("apps") {
		return facades.Schema().Create("apps", func(table schema.Blueprint) {
			table.ID()
			// DNS-safe slug; the Swarm service is "echowa-<name>".
			table.String("name")
			// Image repository without a tag, e.g. ghcr.io/owner/app.
			table.String("repository")
			table.String("default_tag").Default("latest")
			table.UnsignedBigInteger("registry_id").Nullable()
			table.String("domain").Nullable()
			table.Integer("port")
			table.Integer("replicas").Default(1)
			table.Text("healthcheck_cmd").Nullable()
			table.Boolean("tls").Default(true)
			// Encrypted with APP_KEY (models.Secret).
			table.Text("webhook_secret")
			// References deployments, which is created next. SQLite only
			// resolves a foreign key's table when a row is written.
			table.UnsignedBigInteger("current_deployment_id").Nullable()
			table.TimestampsTz()
			table.Unique("name")
			table.Unique("domain")
			table.Foreign("registry_id").References("id").On("registries").RestrictOnDelete()
			table.Foreign("current_deployment_id").References("id").On("deployments").NullOnDelete()
		})
	}

	return nil
}

// Down Reverse the migrations.
func (r *M20261023210700CreateAppsTable) Down() error {
	return facades.Schema().DropIfExists("apps")
}
