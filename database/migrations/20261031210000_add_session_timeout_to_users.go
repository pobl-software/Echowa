package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

// M20261031210000AddSessionTimeoutToUsers adds how long the admin's
// sessions last without activity, in minutes (ADR 0043). Null is the
// default. It also clears remember_token: "remember me" is gone, and an
// older release after a rollback would still honour its cookies. The
// column stays, since that release still reads it.
type M20261031210000AddSessionTimeoutToUsers struct{}

// Signature The unique signature for the migration.
func (r *M20261031210000AddSessionTimeoutToUsers) Signature() string {
	return "20261031210000_add_session_timeout_to_users"
}

// Up Run the migrations.
func (r *M20261031210000AddSessionTimeoutToUsers) Up() error {
	if !facades.Schema().HasColumn("users", "session_timeout_minutes") {
		if err := facades.Schema().Table("users", func(table schema.Blueprint) {
			table.Integer("session_timeout_minutes").Nullable()
		}); err != nil {
			return err
		}
	}
	// The migrator's transaction: any other query waits on its write lock.
	_, err := facades.Schema().Orm().Query().Table("users").
		WhereNotNull("remember_token").
		Update("remember_token", nil)

	return err
}

// Down Reverse the migrations.
func (r *M20261031210000AddSessionTimeoutToUsers) Down() error {
	return facades.Schema().Table("users", func(table schema.Blueprint) {
		table.DropColumn("session_timeout_minutes")
	})
}
