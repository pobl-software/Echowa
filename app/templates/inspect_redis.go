package templates

import (
	"cmp"
	"errors"
	"slices"
	"strconv"
	"strings"

	"goravel/app/metrics"
)

// redisCLI reads the password from REDISCLI_AUTH, so it isn't an argument
// anyone in the container could see. It exits 0 on an error reply, so
// whatever reads its output checks for what it expects.
const redisCLI = `REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli`

// redisOwnCommands are Echowa's and the healthcheck's, left out of the
// slow query list.
var redisOwnCommands = map[string]bool{"auth": true, "info": true, "ping": true, "config|resetstat": true}

// redisInspect lists commands, not queries: Redis keeps totals for each
// command name since it started, and the name is all that's shown, never
// a key or a value. Commits count every command.
var redisInspect = &Inspect{
	// The size is the volume's: the append-only file and snapshots.
	Health: []string{"sh", "-c",
		`printf 'size_kib:%s\n' "$(du -sk /data | cut -f1)"; ` + redisCLI + ` INFO clients stats`},
	ParseHealth:      parseRedisHealth,
	SlowQueries:      []string{"sh", "-c", redisCLI + " INFO commandstats"},
	ParseSlowQueries: parseRedisCommandStats,
	ResetSlowQueries: []string{"sh", "-c",
		`out=$(` + redisCLI + ` CONFIG RESETSTAT); [ "$out" = OK ] || { echo "$out" >&2; exit 1; }`},
}

// redisInfo reads INFO's "key:value" lines.
func redisInfo(out []byte) map[string]string {
	info := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && !strings.HasPrefix(key, "#") {
			info[key] = value
		}
	}

	return info
}

func parseRedisHealth(out []byte) (metrics.DatabaseStatus, error) {
	info := redisInfo(out)
	figure := func(key string) *float64 {
		v, err := strconv.ParseFloat(info[key], 64)
		if err != nil {
			return nil
		}
		return &v
	}
	connections := figure("connected_clients")
	if connections == nil {
		return metrics.DatabaseStatus{}, errors.New("reading Redis's INFO: no connected_clients")
	}
	*connections-- // the check's own

	status := metrics.DatabaseStatus{
		Connections:    connections,
		MaxConnections: figure("maxclients"),
		CacheHits:      figure("keyspace_hits"),
		CacheMisses:    figure("keyspace_misses"),
		Commits:        figure("total_commands_processed"),
		EvictedKeys:    figure("evicted_keys"),
	}
	if kib := figure("size_kib"); kib != nil {
		bytes := *kib * 1024
		status.SizeBytes = &bytes
	}

	return status, nil
}

// parseRedisCommandStats reads lines such as
// "cmdstat_get:calls=2,usec=15,usec_per_call=7.50,...". Subcommands come
// as "config|get".
func parseRedisCommandStats(out []byte) (SlowQueryReport, error) {
	if !strings.Contains(string(out), "# Commandstats") {
		return SlowQueryReport{}, errors.New("reading Redis's command stats: no Commandstats section")
	}
	queries := []SlowQuery{}
	for key, value := range redisInfo(out) {
		name, ok := strings.CutPrefix(key, "cmdstat_")
		if !ok || redisOwnCommands[name] {
			continue
		}
		query := SlowQuery{Query: strings.ToUpper(strings.ReplaceAll(name, "|", " "))}
		for _, field := range strings.Split(value, ",") {
			k, v, _ := strings.Cut(field, "=")
			switch k {
			case "calls":
				query.Calls, _ = strconv.ParseInt(v, 10, 64)
			case "usec":
				usec, _ := strconv.ParseFloat(v, 64)
				query.TotalMs = usec / 1000
			case "usec_per_call":
				usec, _ := strconv.ParseFloat(v, 64)
				query.MeanMs = usec / 1000
			}
		}
		queries = append(queries, query)
	}
	slices.SortFunc(queries, func(a, b SlowQuery) int {
		return cmp.Or(cmp.Compare(b.TotalMs, a.TotalMs), cmp.Compare(a.Query, b.Query))
	})

	return SlowQueryReport{Queries: queries[:min(len(queries), SlowQueryLimit)]}, nil
}
