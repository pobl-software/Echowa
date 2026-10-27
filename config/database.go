package config

import (
	"net/url"
	"path/filepath"

	"github.com/goravel/framework/contracts/database/driver"
	sqlitefacades "github.com/goravel/sqlite/facades"

	"goravel/app/facades"
)

func init() {
	config := facades.Config()
	database := config.Env("DB_DATABASE", "database/echowa.sqlite").(string)
	config.Add("database", map[string]any{
		// Default database connection name
		"default": "sqlite",
		// Database connections
		"connections": map[string]any{
			"sqlite": map[string]any{
				"database": database,
				"dsn":      SQLiteDSN(database),
				"prefix":   "",
				"singular": false,
				"via": func() (driver.Driver, error) {
					return sqlitefacades.Sqlite("sqlite")
				},
			},
		},
		// Pool configuration
		"pool": map[string]any{
			// Sets the maximum number of connections in the idle
			// connection pool.
			//
			// If MaxOpenConns is greater than 0 but less than the new MaxIdleConns,
			// then the new MaxIdleConns will be reduced to match the MaxOpenConns limit.
			//
			// If n <= 0, no idle connections are retained.
			"max_idle_conns": 10,
			// Sets the maximum number of open connections to the database.
			//
			// If MaxIdleConns is greater than 0 and the new MaxOpenConns is less than
			// MaxIdleConns, then MaxIdleConns will be reduced to match the new
			// MaxOpenConns limit.
			//
			// If n <= 0, then there is no limit on the number of open connections.
			"max_open_conns": 100,
			// Sets the maximum amount of time a connection may be idle.
			//
			// Expired connections may be closed lazily before reuse.
			//
			// If d <= 0, connections are not closed due to a connection's idle time.
			// Unit: Second
			"conn_max_idletime": 3600,
			// Sets the maximum amount of time a connection may be reused.
			//
			// Expired connections may be closed lazily before reuse.
			//
			// If d <= 0, connections are not closed due to a connection's age.
			// Unit: Second
			"conn_max_lifetime": 3600,
		},
		// Sets the threshold for slow queries in milliseconds, the slow query will be logged.
		// Unit: Millisecond
		"slow_threshold": 200,
		// The metrics file, beside the main database unless
		// ECHOWA_METRICS_DB names another (ADR 0024). It isn't a Goravel
		// connection: app/metrics opens it and versions its own schema.
		"metrics": map[string]any{
			"database": config.Env("ECHOWA_METRICS_DB", filepath.Join(filepath.Dir(database), "metrics.sqlite")),
		},
		// Migration Repository Table
		//
		// This table keeps track of all the migrations that have already run for
		// your application. Using this information, we can determine which of
		// the migrations on disk haven't actually been run in the database.
		"migrations": map[string]any{
			"table": "migrations",
		},
	})
}

// SQLiteDSN opens path with the pragmas Echowa relies on: WAL so the UI can
// read while a deploy writes, a busy timeout instead of instant SQLITE_BUSY,
// enforced foreign keys, and write transactions that take the lock up front
// (_txlock=immediate) so two writers queue instead of deadlocking. See
// ADR 0014.
func SQLiteDSN(path string) string {
	query := url.Values{}
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "journal_mode(wal)")
	query.Add("_pragma", "synchronous(normal)")
	query.Add("_pragma", "foreign_keys(1)")
	query.Set("_txlock", "immediate")

	return "file:" + path + "?" + query.Encode()
}
