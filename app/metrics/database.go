package metrics

import (
	"cmp"
	"slices"
	"time"
)

// Database is a template database's own figures, read once a minute by a
// command run in its container (ADR 0027). Nil means the database doesn't
// report the figure, or it couldn't be measured.
type Database struct {
	Name            string   `json:"app"`
	SizeBytes       *float64 `json:"size_bytes"`
	Connections     *float64 `json:"connections"`
	MaxConnections  *float64 `json:"max_connections"`
	CacheHitPercent *float64 `json:"cache_hit_percent"`
	CommitsPerSec   *float64 `json:"commits_per_sec"`
	RollbacksPerSec *float64 `json:"rollbacks_per_sec"`
	TempBytesPerSec *float64 `json:"temp_bytes_per_sec"`
	DeadlocksPerSec *float64 `json:"deadlocks_per_sec"`
	// LongestTransactionSeconds is how long the oldest open transaction
	// has been open.
	LongestTransactionSeconds *float64 `json:"longest_transaction_seconds"`
	IdleInTransaction         *float64 `json:"idle_in_transaction"`
	TransactionIDAge          *float64 `json:"transaction_id_age"`
	EvictedKeysPerSec         *float64 `json:"evicted_keys_per_sec"`
}

// DatabaseStatus is what a database's health command reports: gauges as
// they are, and counters as totals since the server started. A health
// command that prints JSON uses these keys.
type DatabaseStatus struct {
	SizeBytes                 *float64 `json:"size_bytes"`
	Connections               *float64 `json:"connections"`
	MaxConnections            *float64 `json:"max_connections"`
	LongestTransactionSeconds *float64 `json:"longest_transaction_seconds"`
	IdleInTransaction         *float64 `json:"idle_in_transaction"`
	TransactionIDAge          *float64 `json:"transaction_id_age"`

	CacheHits   *float64 `json:"cache_hits"`
	CacheMisses *float64 `json:"cache_misses"`
	Commits     *float64 `json:"commits"`
	Rollbacks   *float64 `json:"rollbacks"`
	TempBytes   *float64 `json:"temp_bytes"`
	Deadlocks   *float64 `json:"deadlocks"`
	EvictedKeys *float64 `json:"evicted_keys"`
}

// DatabaseInterval is how often the server checks each database.
const DatabaseInterval = time.Minute

// DatabaseRates turns each database's counters into rates against the
// status it reported the round before.
type DatabaseRates struct {
	at   time.Time
	last map[string]DatabaseStatus
}

// Read takes one round of statuses, by app name, and returns their
// figures, sorted by name. A database missing from the round before, or a
// round more than three intervals old, has no rates.
func (d *DatabaseRates) Read(at time.Time, statuses map[string]DatabaseStatus) []Database {
	var prev map[string]DatabaseStatus
	if gap := at.Sub(d.at); d.last != nil && gap > 0 && gap <= 3*DatabaseInterval {
		prev = d.last
	}
	seconds := at.Sub(d.at).Seconds()

	list := make([]Database, 0, len(statuses))
	for name, now := range statuses {
		db := Database{
			Name:                      name,
			SizeBytes:                 now.SizeBytes,
			Connections:               now.Connections,
			MaxConnections:            now.MaxConnections,
			LongestTransactionSeconds: now.LongestTransactionSeconds,
			IdleInTransaction:         now.IdleInTransaction,
			TransactionIDAge:          now.TransactionIDAge,
		}
		if was, ok := prev[name]; ok {
			db.CommitsPerSec = counterRate(was.Commits, now.Commits, seconds)
			db.RollbacksPerSec = counterRate(was.Rollbacks, now.Rollbacks, seconds)
			db.TempBytesPerSec = counterRate(was.TempBytes, now.TempBytes, seconds)
			db.DeadlocksPerSec = counterRate(was.Deadlocks, now.Deadlocks, seconds)
			db.EvictedKeysPerSec = counterRate(was.EvictedKeys, now.EvictedKeys, seconds)
			db.CacheHitPercent = hitPercent(was, now)
		}
		list = append(list, db)
	}
	sortByName(list)
	d.at, d.last = at, statuses

	return list
}

// hitPercent is the share of reads served from cache since the last round,
// or nil when there were none.
func hitPercent(was, now DatabaseStatus) *float64 {
	hits := counterRate(was.CacheHits, now.CacheHits, 1)
	misses := counterRate(was.CacheMisses, now.CacheMisses, 1)
	if hits == nil || misses == nil || *hits+*misses == 0 {
		return nil
	}

	return ptr(*hits / (*hits + *misses) * 100)
}

// counterRate is a counter's change per second, or nil if either side is
// missing or it went down (a restart or a reset).
func counterRate(was, now *float64, seconds float64) *float64 {
	if was == nil || now == nil || *now < *was || seconds <= 0 {
		return nil
	}

	return ptr((*now - *was) / seconds)
}

// DatabaseSample is a round of figures as a stored minute: one reading per
// database, so each figure's average and max are the reading.
func DatabaseSample(at time.Time, dbs []Database) Sample {
	s := Sample{At: at.Truncate(time.Minute), Rows: make([][]Row, len(Tables))}
	i := tableIndex(DatabaseTable.Name)
	for _, db := range dbs {
		row := Row{Key: db.Name, Readings: 1}
		for _, v := range databaseValues(db) {
			row.Figures = append(row.Figures, Stat{Avg: v, Max: v})
		}
		s.Rows[i] = append(s.Rows[i], row)
	}

	return s
}

func databaseValues(d Database) []*float64 {
	return []*float64{
		d.SizeBytes, d.Connections, d.MaxConnections, d.CacheHitPercent,
		d.CommitsPerSec, d.RollbacksPerSec, d.TempBytesPerSec, d.DeadlocksPerSec,
		d.LongestTransactionSeconds, d.IdleInTransaction, d.TransactionIDAge, d.EvictedKeysPerSec,
	}
}

func sortByName(dbs []Database) {
	slices.SortFunc(dbs, func(a, b Database) int { return cmp.Compare(a.Name, b.Name) })
}

func tableIndex(name string) int {
	return slices.IndexFunc(Tables, func(t Table) bool { return t.Name == name })
}
