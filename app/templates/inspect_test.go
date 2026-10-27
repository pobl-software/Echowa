package templates

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisHealthLeavesOutTheCheck(t *testing.T) {
	out := "size_kib:12\n# Clients\r\nconnected_clients:3\r\nmaxclients:10000\r\n# Stats\r\nkeyspace_hits:90\r\nkeyspace_misses:10\r\n" +
		"evicted_keys:0\r\ntotal_commands_processed:500\r\n"
	status, err := parseRedisHealth([]byte(out))
	require.NoError(t, err)
	assert.Equal(t, 12288.0, *status.SizeBytes)
	assert.Equal(t, 2.0, *status.Connections, "not the check's own connection")
	assert.Equal(t, 10000.0, *status.MaxConnections)
	assert.Equal(t, 500.0, *status.Commits)
	assert.Nil(t, status.Rollbacks)

	_, err = parseRedisHealth([]byte("NOAUTH Authentication required.\n"))
	assert.Error(t, err, "redis-cli exits 0 on an error reply")
}

func TestRedisCommandStatsAreRankedByTotalTime(t *testing.T) {
	out := "# Commandstats\r\n" +
		"cmdstat_get:calls=2,usec=30,usec_per_call=15.00,rejected_calls=0,failed_calls=0\r\n" +
		"cmdstat_info:calls=70,usec=5800,usec_per_call=82.86,rejected_calls=0,failed_calls=0\r\n" +
		"cmdstat_config|get:calls=1,usec=40,usec_per_call=40.00,rejected_calls=0,failed_calls=0\r\n"
	report, err := parseRedisCommandStats([]byte(out))
	require.NoError(t, err)
	require.Len(t, report.Queries, 2, "Echowa's own INFO is left out")
	assert.Equal(t, SlowQuery{Query: "CONFIG GET", Calls: 1, TotalMs: 0.04, MeanMs: 0.04}, report.Queries[0])
	assert.Equal(t, "GET", report.Queries[1].Query)

	_, err = parseRedisCommandStats([]byte("NOAUTH Authentication required.\n"))
	assert.Error(t, err)
}

func TestMySQLHealthTakesItsSizeFromDu(t *testing.T) {
	status, err := parseMySQLHealth([]byte("213488\n{\"connections\": 0.0, \"commits\": 618}\n"))
	require.NoError(t, err)
	assert.Equal(t, 213488.0*1024, *status.SizeBytes)
	assert.Equal(t, 618.0, *status.Commits)

	status, err = parseMySQLHealth([]byte("\n{\"connections\": 1}\n"))
	require.NoError(t, err)
	assert.Nil(t, status.SizeBytes, "du failing leaves the size unknown")
}

func TestSlowQueriesAreRankedByTotalTime(t *testing.T) {
	report, err := parseSlowQueryJSON([]byte(`{"since": null, "dropped": 0, "queries": [
		{"query": "a", "calls": 1, "total_ms": 1, "mean_ms": 1, "max_ms": 1, "rows": 1},
		{"query": "b", "calls": 1, "total_ms": 5, "mean_ms": 5, "max_ms": 5, "rows": 1}]}`))
	require.NoError(t, err)
	assert.Equal(t, "b", report.Queries[0].Query)

	report, err = parseSlowQueryJSON([]byte(`{"since": null, "dropped": null, "queries": null}`))
	require.NoError(t, err)
	assert.Equal(t, []SlowQuery{}, report.Queries, "an empty list, not null")
}
