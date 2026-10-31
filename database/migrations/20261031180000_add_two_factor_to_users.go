package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/app/facades"
)

// M20261031180000AddTwoFactorToUsers adds two-factor authentication (ADR
// 0042): the TOTP secret, encrypted, when it was confirmed, and the last
// step a code was accepted for. They're nullable, so SQLite adds them in
// place.
type M20261031180000AddTwoFactorToUsers struct{}

// Signature The unique signature for the migration.
func (r *M20261031180000AddTwoFactorToUsers) Signature() string {
	return "20261031180000_add_two_factor_to_users"
}

// Up Run the migrations.
func (r *M20261031180000AddTwoFactorToUsers) Up() error {
	if facades.Schema().HasColumn("users", "two_factor_secret") {
		return nil
	}

	return facades.Schema().Table("users", func(table schema.Blueprint) {
		table.Text("two_factor_secret").Nullable()
		table.TimestampTz("two_factor_confirmed_at").Nullable()
		table.BigInteger("two_factor_last_step").Nullable()
	})
}

// Down Reverse the migrations.
func (r *M20261031180000AddTwoFactorToUsers) Down() error {
	return facades.Schema().Table("users", func(table schema.Blueprint) {
		table.DropColumn("two_factor_secret", "two_factor_confirmed_at", "two_factor_last_step")
	})
}
