package templates

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"goravel/app/metrics"
)

// Inspect is how Echowa reads a template database's own figures, with a
// command run in its container (ADR 0027).
type Inspect struct {
	// Health prints the database's status. It's a constant: nothing a
	// request sends ever reaches it.
	Health []string
	// ParseHealth reads what Health printed.
	ParseHealth func([]byte) (metrics.DatabaseStatus, error)

	// SlowQueryFlags turn on what SlowQueries reads, added to Server while
	// the app's switch is on (Options.SlowQueries). Empty when the server
	// collects it anyway, so there's no switch.
	SlowQueryFlags string
	// SlowQueries prints the queries that took the most time in total, and
	// ParseSlowQueries reads them. Only normalised text: never a query's
	// values.
	SlowQueries      []string
	ParseSlowQueries func([]byte) (SlowQueryReport, error)
	// ResetSlowQueries clears the totals.
	ResetSlowQueries []string
}

// SlowQueryReport is the queries that took the most time in total, since
// Since. Mirrored by SlowQueryReport in resources/js/types/models.ts.
type SlowQueryReport struct {
	// Since is when the totals start: the last reset, or nil when the
	// database doesn't say.
	Since *time.Time `json:"since"`
	// Dropped is how many queries were forgotten to make room for others.
	Dropped *int64      `json:"dropped"`
	Queries []SlowQuery `json:"queries"`
}

type SlowQuery struct {
	// Query has its values replaced by placeholders such as $1, or is only
	// a command's name.
	Query   string  `json:"query"`
	Calls   int64   `json:"calls"`
	TotalMs float64 `json:"total_ms"`
	MeanMs  float64 `json:"mean_ms"`
	// MaxMs and Rows are nil when the database doesn't count them.
	MaxMs *float64 `json:"max_ms"`
	Rows  *int64   `json:"rows"`
}

// SlowQueryLimit is how many queries a report lists.
const SlowQueryLimit = 20

// HasSlowQueries is whether the app's database can list its slow queries
// now: the template reads them, and the switch is on if it needs one.
func (t Template) HasSlowQueries(switchedOn bool) bool {
	return t.Inspect != nil && t.Inspect.SlowQueries != nil && (t.Inspect.SlowQueryFlags == "" || switchedOn)
}

// parseHealthJSON reads a status printed as JSON, with DatabaseStatus's
// keys.
func parseHealthJSON(out []byte) (metrics.DatabaseStatus, error) {
	var status metrics.DatabaseStatus
	if err := json.Unmarshal(out, &status); err != nil {
		return metrics.DatabaseStatus{}, fmt.Errorf("reading the database's status: %w", err)
	}

	return status, nil
}

// parseSlowQueryJSON reads a SlowQueryReport printed as JSON.
func parseSlowQueryJSON(out []byte) (SlowQueryReport, error) {
	var report SlowQueryReport
	if err := json.Unmarshal(out, &report); err != nil {
		return SlowQueryReport{}, fmt.Errorf("reading the slow queries: %w", err)
	}
	if report.Queries == nil {
		report.Queries = []SlowQuery{}
	}
	// MySQL doesn't promise JSON_ARRAYAGG keeps the subquery's order.
	slices.SortStableFunc(report.Queries, func(a, b SlowQuery) int { return cmp.Compare(b.TotalMs, a.TotalMs) })

	return report, nil
}
