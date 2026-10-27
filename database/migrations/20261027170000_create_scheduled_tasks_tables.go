package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

// M20261027170000CreateScheduledTasksTables adds a custom app's scheduled
// tasks and their runs (ADR 0032). Both go with their app.
type M20261027170000CreateScheduledTasksTables struct{}

// Signature The unique signature for the migration.
func (r *M20261027170000CreateScheduledTasksTables) Signature() string {
	return "20261027170000_create_scheduled_tasks_tables"
}

// Up Run the migrations.
func (r *M20261027170000CreateScheduledTasksTables) Up() error {
	if !facades.Schema().HasTable("scheduled_tasks") {
		err := facades.Schema().Create("scheduled_tasks", func(table schema.Blueprint) {
			table.ID()
			table.UnsignedBigInteger("app_id")
			table.String("name")
			// A five-field cron expression, read in timezone.
			table.String("schedule")
			table.String("timezone")
			table.Text("command")
			table.Integer("timeout_minutes").Default(15)
			table.Boolean("enabled").Default(true)
			// The newest failed run dismissed on the Overview tab. Not a
			// foreign key: runs are pruned, and only the ID is compared.
			table.UnsignedBigInteger("dismissed_run_id").Nullable()
			table.TimestampsTz()
			table.Index("app_id")
			table.Foreign("app_id").References("id").On("apps").CascadeOnDelete()
		})
		if err != nil {
			return err
		}
	}

	if !facades.Schema().HasTable("scheduled_task_runs") {
		return facades.Schema().Create("scheduled_task_runs", func(table schema.Blueprint) {
			table.ID()
			table.UnsignedBigInteger("scheduled_task_id")
			table.String("trigger")
			table.String("status")
			table.Integer("exit_code").Nullable()
			// The tail of stdout and stderr, as written.
			table.LongText("output").Default("")
			// Echowa's own note, such as why the run was skipped.
			table.Text("message").Nullable()
			table.TimestampTz("started_at")
			table.TimestampTz("finished_at").Nullable()
			table.TimestampsTz()
			table.Index("scheduled_task_id", "id")
			table.Foreign("scheduled_task_id").References("id").On("scheduled_tasks").CascadeOnDelete()
		})
	}

	return nil
}

// Down Reverse the migrations.
func (r *M20261027170000CreateScheduledTasksTables) Down() error {
	if err := facades.Schema().DropIfExists("scheduled_task_runs"); err != nil {
		return err
	}

	return facades.Schema().DropIfExists("scheduled_tasks")
}
