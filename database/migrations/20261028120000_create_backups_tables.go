package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

// M20261028120000CreateBackupsTables adds template databases' backups and
// the schedules that make them (ADR 0034). The schedules are used from
// part 4 of docs/plans/backups.md, and made now so backups can refer to
// them without a table rebuild.
type M20261028120000CreateBackupsTables struct{}

// Signature The unique signature for the migration.
func (r *M20261028120000CreateBackupsTables) Signature() string {
	return "20261028120000_create_backups_tables"
}

// Up Run the migrations.
func (r *M20261028120000CreateBackupsTables) Up() error {
	if !facades.Schema().HasTable("backup_schedules") {
		err := facades.Schema().Create("backup_schedules", func(table schema.Blueprint) {
			table.ID()
			table.UnsignedBigInteger("app_id")
			table.UnsignedBigInteger("storage_location_id")
			// A five-field cron expression, read in timezone.
			table.String("schedule")
			table.String("timezone")
			// How many of its successful backups the schedule keeps.
			table.Integer("keep").Default(7)
			table.Boolean("enabled").Default(true)
			// The newest backup that didn't succeed and was dismissed on
			// Overview. Not a foreign key: only the ID is compared.
			table.UnsignedBigInteger("dismissed_backup_id").Nullable()
			table.TimestampsTz()
			table.Index("app_id")
			table.Foreign("app_id").References("id").On("apps").CascadeOnDelete()
			// A location a schedule uses can't be deleted.
			table.Foreign("storage_location_id").References("id").On("storage_locations").RestrictOnDelete()
		})
		if err != nil {
			return err
		}
	}

	if !facades.Schema().HasTable("backups") {
		return facades.Schema().Create("backups", func(table schema.Blueprint) {
			table.ID()
			table.UnsignedBigInteger("app_id")
			table.UnsignedBigInteger("storage_location_id")
			table.UnsignedBigInteger("backup_schedule_id").Nullable()
			table.String("trigger")
			table.String("status")
			// The dump's whole key, prefix included. Empty until the
			// backup gets as far as naming it.
			table.String("key").Default("")
			table.BigInteger("size_bytes").Default(0)
			table.String("sha256").Default("")
			table.String("server_version").Default("")
			// The end of the dump's stderr.
			table.Text("output").Default("")
			// Echowa's own note, such as why the backup failed.
			table.Text("message").Nullable()
			table.TimestampTz("started_at")
			table.TimestampTz("finished_at").Nullable()
			table.TimestampsTz()
			table.Index("app_id", "id")
			table.Index("backup_schedule_id", "status", "id")
			table.Foreign("app_id").References("id").On("apps").CascadeOnDelete()
			// Deleting a location forgets its backups; the objects stay.
			table.Foreign("storage_location_id").References("id").On("storage_locations").CascadeOnDelete()
			// Deleting a schedule keeps its backups, out of its retention.
			table.Foreign("backup_schedule_id").References("id").On("backup_schedules").NullOnDelete()
		})
	}

	return nil
}

// Down Reverse the migrations.
func (r *M20261028120000CreateBackupsTables) Down() error {
	if err := facades.Schema().DropIfExists("backups"); err != nil {
		return err
	}

	return facades.Schema().DropIfExists("backup_schedules")
}
