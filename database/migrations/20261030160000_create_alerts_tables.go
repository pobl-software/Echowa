package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

// M20261030160000CreateAlertsTables adds alert rules, the providers each
// names, each rule's state per subject, and the notifications sent (ADR
// 0040). The conditions' columns (threshold, for and repeat minutes, the
// state's since and value) are used from part 5 of
// docs/plans/notifications.md, and made now so no table is rebuilt.
type M20261030160000CreateAlertsTables struct{}

// Signature The unique signature for the migration.
func (r *M20261030160000CreateAlertsTables) Signature() string {
	return "20261030160000_create_alerts_tables"
}

// Up Run the migrations.
func (r *M20261030160000CreateAlertsTables) Up() error {
	if !facades.Schema().HasTable("alert_rules") {
		err := facades.Schema().Create("alert_rules", func(table schema.Blueprint) {
			table.ID()
			// server, all_apps or app. Only an app rule has an app_id.
			table.String("scope")
			table.UnsignedBigInteger("app_id").Nullable()
			// A trigger's ID in app/alerts. One this release doesn't
			// know, from a newer one, is skipped.
			table.String("trigger")
			table.Double("threshold").Nullable()
			table.Integer("for_minutes").Default(0)
			// 0 never repeats.
			table.Integer("repeat_minutes").Default(0)
			table.Boolean("notify_resolved").Default(true)
			table.String("severity")
			table.Boolean("enabled").Default(true)
			table.TimestampsTz()
			table.Index("trigger", "enabled")
			table.Foreign("app_id").References("id").On("apps").CascadeOnDelete()
		})
		if err != nil {
			return err
		}
	}

	if !facades.Schema().HasTable("alert_rule_providers") {
		err := facades.Schema().Create("alert_rule_providers", func(table schema.Blueprint) {
			table.UnsignedBigInteger("alert_rule_id")
			table.UnsignedBigInteger("notification_provider_id")
			table.Primary("alert_rule_id", "notification_provider_id")
			table.Index("notification_provider_id")
			table.Foreign("alert_rule_id").References("id").On("alert_rules").CascadeOnDelete()
			table.Foreign("notification_provider_id").References("id").On("notification_providers").CascadeOnDelete()
		})
		if err != nil {
			return err
		}
	}

	if !facades.Schema().HasTable("alert_states") {
		err := facades.Schema().Create("alert_states", func(table schema.Blueprint) {
			table.ID()
			table.UnsignedBigInteger("alert_rule_id")
			// What the rule watches: app:<id>, mount:<path> or server.
			table.String("subject")
			// A condition's: ok, pending or firing.
			table.String("state").Default("ok")
			table.TimestampTz("since").Nullable()
			table.Double("value").Nullable()
			table.TimestampTz("notified_at").Nullable()
			// An event read from a poll: when the newest one it notified
			// happened, such as a kill for memory.
			table.TimestampTz("event_at").Nullable()
			table.TimestampsTz()
			table.Unique("alert_rule_id", "subject")
			table.Foreign("alert_rule_id").References("id").On("alert_rules").CascadeOnDelete()
		})
		if err != nil {
			return err
		}
	}

	if !facades.Schema().HasTable("notifications") {
		return facades.Schema().Create("notifications", func(table schema.Blueprint) {
			table.ID()
			// Null for the held-back summary, and once its rule is deleted.
			table.UnsignedBigInteger("alert_rule_id").Nullable()
			table.UnsignedBigInteger("notification_provider_id")
			table.UnsignedBigInteger("app_id").Nullable()
			table.String("trigger")
			table.String("severity")
			table.String("title")
			table.Text("summary")
			// pending, sent, failed or suppressed.
			table.String("status")
			table.Integer("attempts").Default(0)
			table.Text("error").Nullable()
			table.TimestampTz("sent_at").Nullable()
			table.TimestampsTz()
			table.Index("notification_provider_id", "created_at")
			table.Index("app_id", "id")
			table.Index("status")
			table.Foreign("alert_rule_id").References("id").On("alert_rules").NullOnDelete()
			table.Foreign("notification_provider_id").References("id").On("notification_providers").CascadeOnDelete()
			table.Foreign("app_id").References("id").On("apps").CascadeOnDelete()
		})
	}

	return nil
}

// Down Reverse the migrations.
func (r *M20261030160000CreateAlertsTables) Down() error {
	for _, table := range []string{"notifications", "alert_states", "alert_rule_providers", "alert_rules"} {
		if err := facades.Schema().DropIfExists(table); err != nil {
			return err
		}
	}

	return nil
}
