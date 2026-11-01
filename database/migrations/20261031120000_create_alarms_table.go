package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

// M20261031120000CreateAlarmsTable adds the alarms: one row for each time
// an alert went off, whoever it was sent to, for the Alarms page.
type M20261031120000CreateAlarmsTable struct{}

// Signature The unique signature for the migration.
func (r *M20261031120000CreateAlarmsTable) Signature() string {
	return "20261031120000_create_alarms_table"
}

// Up Run the migrations.
func (r *M20261031120000CreateAlarmsTable) Up() error {
	if facades.Schema().HasTable("alarms") {
		return nil
	}

	return facades.Schema().Create("alarms", func(table schema.Blueprint) {
		table.ID()
		// The loudest rule that went off; null once it's deleted.
		table.UnsignedBigInteger("alert_rule_id").Nullable()
		table.UnsignedBigInteger("app_id").Nullable()
		table.String("trigger")
		// info, warning, critical, or resolved for a condition that cleared.
		table.String("severity")
		table.String("title")
		table.Text("summary")
		// The page it's about, such as /apps/3?tab=deployments, or empty.
		table.String("path").Default("")
		table.TimestampTz("read_at").Nullable()
		table.TimestampsTz()
		table.Index("read_at")
		table.Index("created_at")
		table.Foreign("alert_rule_id").References("id").On("alert_rules").NullOnDelete()
		table.Foreign("app_id").References("id").On("apps").CascadeOnDelete()
	})
}

// Down Reverse the migrations.
func (r *M20261031120000CreateAlarmsTable) Down() error {
	return facades.Schema().DropIfExists("alarms")
}
