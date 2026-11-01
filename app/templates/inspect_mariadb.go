package templates

import (
	"fmt"
	"strings"
)

// MariaDB keeps the Performance Schema off unless it starts with it, so
// the health check reads information_schema, which is always there, and
// the slow query list needs the switch.
const mariadbSlowQueryFlags = "--performance-schema=ON"

// mariadbQuiet is mysqlQuiet while the Performance Schema is on. While
// it's off, the Performance Schema refuses every change, so mysqlQuiet on
// its own would fail the health check.
var mariadbQuiet = `EXECUTE IMMEDIATE IF(@@performance_schema, "` + strings.TrimSuffix(mysqlQuiet, "; ") + `", 'DO 0'); `

// mariadbClient is mysqlClient with MariaDB's names.
const mariadbClient = `MYSQL_PWD="$MARIADB_ROOT_PASSWORD" mariadb -u root -N -B --connect-timeout=3`

// mariadb runs statements after mysqlQuiet, as mysql does. While the
// Performance Schema is off its tables read as empty, so mysqlQuiet's
// failure is what says there's nothing to read.
func mariadb(statements string) []string {
	return []string{"sh", "-c", mariadbClient + ` -e "$1$2"`, "sh", mysqlQuiet, statements}
}

// mariadbStatus is signed: a fresh server has read more pages from disk
// than it was asked for, so the cache's hits start below 0.
func mariadbStatus(name string) string {
	return fmt.Sprintf("(SELECT CAST(VARIABLE_VALUE AS SIGNED) FROM information_schema.GLOBAL_STATUS WHERE VARIABLE_NAME = '%s')", name)
}

// mariadbHealth is mysqlHealth from information_schema, with MariaDB's
// statement time limit and its own deadlock count.
var mariadbHealth = fmt.Sprintf(`SET STATEMENT max_statement_time=3 FOR SELECT JSON_OBJECT(
	'connections', %s - 1,
	'max_connections', @@max_connections,
	'longest_transaction_seconds', (SELECT COALESCE(MAX(TIMESTAMPDIFF(MICROSECOND, trx_started, NOW(6))) / 1e6, 0)
		FROM information_schema.innodb_trx WHERE trx_mysql_thread_id <> CONNECTION_ID()),
	'idle_in_transaction', (SELECT COUNT(*) FROM information_schema.innodb_trx t
		JOIN information_schema.PROCESSLIST p ON p.ID = t.trx_mysql_thread_id WHERE p.COMMAND = 'Sleep'),
	'cache_hits', %s - %s,
	'cache_misses', %s,
	'commits', %s,
	'rollbacks', %s,
	'deadlocks', %s
)`,
	mariadbStatus("THREADS_CONNECTED"),
	mariadbStatus("INNODB_BUFFER_POOL_READ_REQUESTS"), mariadbStatus("INNODB_BUFFER_POOL_READS"),
	mariadbStatus("INNODB_BUFFER_POOL_READS"),
	mariadbStatus("HANDLER_COMMIT"), mariadbStatus("HANDLER_ROLLBACK"),
	mariadbStatus("INNODB_DEADLOCKS"))

// mariadbSlowQueries is mysqlSlowQueries with MariaDB's statement time
// limit. The health check's own statement doesn't name performance_schema,
// so statements on GLOBAL_STATUS, Echowa's or another monitor's, are left
// out too.
var mariadbSlowQueries = fmt.Sprintf(`SET STATEMENT max_statement_time=3 FOR SELECT JSON_OBJECT(
	'since', NULL,
	'dropped', %s,
	'queries', COALESCE((SELECT JSON_ARRAYAGG(JSON_OBJECT(
		'query', DIGEST_TEXT, 'calls', COUNT_STAR, 'total_ms', SUM_TIMER_WAIT / 1e9, 'mean_ms', AVG_TIMER_WAIT / 1e9,
		'max_ms', MAX_TIMER_WAIT / 1e9, 'rows', SUM_ROWS_SENT))
		FROM (SELECT * FROM performance_schema.events_statements_summary_by_digest
			WHERE DIGEST_TEXT IS NOT NULL AND DIGEST_TEXT NOT LIKE '%%performance_schema%%' AND DIGEST_TEXT NOT LIKE '%%GLOBAL_STATUS%%'
			ORDER BY SUM_TIMER_WAIT DESC LIMIT %d) q), JSON_ARRAY())
)`, mariadbStatus("PERFORMANCE_SCHEMA_DIGEST_LOST"), SlowQueryLimit)

var mariadbInspect = &Inspect{
	// The size is the whole data directory, with the binary and redo logs.
	Health: []string{"sh", "-c",
		`du -sk /var/lib/mysql | cut -f1; ` + mariadbClient + ` -e "$1$2"`,
		"sh", mariadbQuiet, mariadbHealth},
	ParseHealth:      parseSizedHealthJSON,
	SlowQueryFlags:   mariadbSlowQueryFlags,
	SlowQueries:      mariadb(mariadbSlowQueries),
	ParseSlowQueries: parseSlowQueryJSON,
	ResetSlowQueries: mariadb("TRUNCATE TABLE performance_schema.events_statements_summary_by_digest"),
}
