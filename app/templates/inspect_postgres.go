package templates

import "fmt"

// psql runs one query over the container's socket as the template's user,
// which the image trusts there, and prints its single value unadorned. The
// query is passed to sh as $1, so nothing is quoted by hand. The statement
// timeout keeps a stuck query from holding a connection, and Echowa's own
// queries stay out of the slow query list.
func psql(query string) []string {
	return []string{"sh", "-c",
		`PGOPTIONS='-c statement_timeout=3s -c pg_stat_statements.track=none' psql -X -q -A -t -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "$1"`,
		"sh", query}
}

// postgresHealth is one row of JSON. Connections and transactions leave out
// the check's own session.
const postgresHealth = `SELECT json_build_object(
	'size_bytes', (SELECT sum(pg_database_size(oid)) FROM pg_database WHERE datallowconn)
		+ (SELECT coalesce(sum(size), 0) FROM pg_ls_waldir()),
	'connections', (SELECT count(*) FROM pg_stat_activity WHERE backend_type = 'client backend' AND pid <> pg_backend_pid()),
	'max_connections', current_setting('max_connections')::int,
	'longest_transaction_seconds', (SELECT greatest(coalesce(extract(epoch FROM max(clock_timestamp() - xact_start)), 0), 0)
		FROM pg_stat_activity WHERE backend_type = 'client backend' AND pid <> pg_backend_pid()),
	'idle_in_transaction', (SELECT count(*) FROM pg_stat_activity WHERE state LIKE 'idle in transaction%'),
	'transaction_id_age', (SELECT max(age(datfrozenxid)) FROM pg_database),
	'cache_hits', (SELECT sum(blks_hit) FROM pg_stat_database),
	'cache_misses', (SELECT sum(blks_read) FROM pg_stat_database),
	'commits', (SELECT sum(xact_commit) FROM pg_stat_database),
	'rollbacks', (SELECT sum(xact_rollback) FROM pg_stat_database),
	'temp_bytes', (SELECT sum(temp_bytes) FROM pg_stat_database),
	'deadlocks', (SELECT sum(deadlocks) FROM pg_stat_database)
)`

// postgresSlowQueries creates the extension the first time, in the app's
// database, though its view covers every database (ADR 0027).
var postgresSlowQueries = fmt.Sprintf(`CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
SELECT json_build_object(
	'since', (SELECT stats_reset FROM pg_stat_statements_info),
	'dropped', (SELECT dealloc FROM pg_stat_statements_info),
	'queries', coalesce((SELECT json_agg(q) FROM (
		SELECT query, calls, total_exec_time AS total_ms, mean_exec_time AS mean_ms, max_exec_time AS max_ms, rows
		FROM pg_stat_statements
		ORDER BY total_exec_time DESC
		LIMIT %d
	) q), '[]'::json)
)`, SlowQueryLimit)

var postgresInspect = &Inspect{
	Health: psql(postgresHealth),
	// Utility commands such as ALTER ROLE ... PASSWORD aren't recorded, so
	// a password never reaches the list.
	SlowQueryFlags:   "-c shared_preload_libraries=pg_stat_statements -c pg_stat_statements.track_utility=off",
	SlowQueries:      psql(postgresSlowQueries),
	ParseSlowQueries: parseSlowQueryJSON,
	ResetSlowQueries: psql("SELECT pg_stat_statements_reset()"),
	ParseHealth:      parseHealthJSON,
}
