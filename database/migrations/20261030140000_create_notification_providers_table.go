package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

// M20261030140000CreateNotificationProvidersTable adds where notifications
// go: Slack, Discord, Teams, ntfy, SMTP or Resend (ADR 0040).
type M20261030140000CreateNotificationProvidersTable struct{}

// Signature The unique signature for the migration.
func (r *M20261030140000CreateNotificationProvidersTable) Signature() string {
	return "20261030140000_create_notification_providers_table"
}

// Up Run the migrations.
func (r *M20261030140000CreateNotificationProvidersTable) Up() error {
	if facades.Schema().HasTable("notification_providers") {
		return nil
	}

	return facades.Schema().Create("notification_providers", func(table schema.Blueprint) {
		table.ID()
		table.String("name")
		table.String("kind")
		// The kind's plain settings as JSON.
		table.Text("config")
		// The kind's secrets as JSON, encrypted with APP_KEY (models.Secret).
		table.Text("secrets")
		table.Boolean("enabled").Default(true)
		table.TimestampsTz()
		table.Unique("name")
	})
}

// Down Reverse the migrations.
func (r *M20261030140000CreateNotificationProvidersTable) Down() error {
	return facades.Schema().DropIfExists("notification_providers")
}
