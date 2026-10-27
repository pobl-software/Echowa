package templates

import (
	"bytes"
	"fmt"
	"strconv"

	"goravel/app/metrics"
)

// mysqlQuiet keeps connections over the container's socket or from
// 127.0.0.1, which are Echowa's and the healthcheck's, out of the slow
// query list. Apps and external clients connect over the network. The
// setting lives in memory, so every command makes sure of it. A
// connection is instrumented or not from when it opens, so the first one
// after a restart is recorded: mysqlSlowQueries leaves out its
// statements.
const mysqlQuiet = "INSERT IGNORE INTO performance_schema.setup_actors (HOST, USER, `ROLE`, ENABLED, HISTORY) " +
	"VALUES ('localhost', '%', '%', 'NO', 'NO'), ('127.0.0.1', '%', '%', 'NO', 'NO'); "

// mysqlClient connects over the container's socket as root, whose password
// is in the env, and prints values unadorned.
const mysqlClient = `MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -u root -N -B --connect-timeout=3`

// mysql runs statements after mysqlQuiet. They're passed to sh as
// arguments, so nothing is quoted by hand.
func mysql(statements string) []string {
	return []string{"sh", "-c", mysqlClient + ` -e "$1$2"`, "sh", mysqlQuiet, statements}
}

func mysqlStatus(name string) string {
	return fmt.Sprintf("(SELECT CAST(VARIABLE_VALUE AS UNSIGNED) FROM performance_schema.global_status WHERE VARIABLE_NAME = '%s')", name)
}

// mysqlHealth is one row of JSON. The size comes from du, before it.
var mysqlHealth = fmt.Sprintf(`SELECT /*+ MAX_EXECUTION_TIME(3000) */ JSON_OBJECT(
	'connections', %s - 1,
	'max_connections', @@max_connections,
	'longest_transaction_seconds', (SELECT COALESCE(MAX(TIMESTAMPDIFF(MICROSECOND, trx_started, NOW(6))) / 1e6, 0)
		FROM information_schema.innodb_trx WHERE trx_mysql_thread_id <> CONNECTION_ID()),
	'idle_in_transaction', (SELECT COUNT(*) FROM information_schema.innodb_trx t
		JOIN performance_schema.threads th ON th.PROCESSLIST_ID = t.trx_mysql_thread_id WHERE th.PROCESSLIST_COMMAND = 'Sleep'),
	'cache_hits', %s - %s,
	'cache_misses', %s,
	'commits', %s,
	'rollbacks', %s,
	'deadlocks', (SELECT m.COUNT FROM information_schema.INNODB_METRICS m WHERE m.NAME = 'lock_deadlocks')
)`,
	mysqlStatus("Threads_connected"),
	mysqlStatus("Innodb_buffer_pool_read_requests"), mysqlStatus("Innodb_buffer_pool_reads"),
	mysqlStatus("Innodb_buffer_pool_reads"),
	mysqlStatus("Handler_commit"), mysqlStatus("Handler_rollback"))

// mysqlSlowQueries reads the statement digests, which MySQL keeps by
// default. DIGEST_TEXT has values as ?; the sample text, which keeps
// them, is never read. Statements on performance_schema are Echowa's or
// another monitor's. Timers are in picoseconds.
var mysqlSlowQueries = fmt.Sprintf(`SELECT /*+ MAX_EXECUTION_TIME(3000) */ JSON_OBJECT(
	'since', NULL,
	'dropped', %s,
	'queries', COALESCE((SELECT JSON_ARRAYAGG(JSON_OBJECT(
		'query', DIGEST_TEXT, 'calls', COUNT_STAR, 'total_ms', SUM_TIMER_WAIT / 1e9, 'mean_ms', AVG_TIMER_WAIT / 1e9,
		'max_ms', MAX_TIMER_WAIT / 1e9, 'rows', SUM_ROWS_SENT))
		FROM (SELECT * FROM performance_schema.events_statements_summary_by_digest
			WHERE DIGEST_TEXT IS NOT NULL AND DIGEST_TEXT NOT LIKE '%%performance_schema%%'
			ORDER BY SUM_TIMER_WAIT DESC LIMIT %d) q), JSON_ARRAY())
)`, mysqlStatus("Performance_schema_digest_lost"), SlowQueryLimit)

var mysqlInspect = &Inspect{
	// The size is the whole data directory, with the binary and redo logs.
	Health: []string{"sh", "-c",
		`du -sk /var/lib/mysql | cut -f1; ` + mysqlClient + ` -e "$1$2"`,
		"sh", mysqlQuiet, mysqlHealth},
	ParseHealth:      parseMySQLHealth,
	SlowQueries:      mysql(mysqlSlowQueries),
	ParseSlowQueries: parseSlowQueryJSON,
	ResetSlowQueries: mysql("TRUNCATE TABLE performance_schema.events_statements_summary_by_digest"),
}

// parseMySQLHealth reads du's KiB on the first line, then the JSON.
func parseMySQLHealth(out []byte) (metrics.DatabaseStatus, error) {
	first, rest, _ := bytes.Cut(out, []byte("\n"))
	status, err := parseHealthJSON(rest)
	if err != nil {
		return metrics.DatabaseStatus{}, err
	}
	if kib, err := strconv.ParseFloat(string(bytes.TrimSpace(first)), 64); err == nil {
		size := kib * 1024
		status.SizeBytes = &size
	}

	return status, nil
}
