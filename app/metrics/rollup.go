package metrics

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// How long each kind of row is kept (ADR 0024). Fixed, with no settings.
const (
	SampleRetention = 48 * time.Hour
	HourlyRetention = 90 * 24 * time.Hour
	DailyRetention  = 365 * 24 * time.Hour
)

// Resolution is a rollup bucket size.
type Resolution struct {
	// The rollup tables' resolution column.
	Key      string
	Interval time.Duration
	// How far back each scheduled run recomputes, so a run that was
	// skipped or failed is made up by the next.
	redo      time.Duration
	retention time.Duration
}

var (
	Hourly = Resolution{"1h", time.Hour, 3 * time.Hour, HourlyRetention}
	Daily  = Resolution{"1d", 24 * time.Hour, 2 * 24 * time.Hour, DailyRetention}
)

// RollupResult says what a rollup covered.
type RollupResult struct {
	// Whole buckets, From inclusive and To exclusive. Equal when there was
	// nothing to do.
	From, To time.Time
	// Rollup rows written, all tables together.
	Rows int64
}

// Rollup rolls the samples in [from, to) up at the given resolution, widened
// to whole buckets counted from the Unix epoch, like the charts. Each bucket
// is recomputed in full from its samples and replaces what's stored, so
// running it again changes nothing.
//
// It never starts before the oldest whole bucket the samples still cover
// (SampleRetention before now): rolling up a bucket that's been partly
// pruned would overwrite good numbers with partial ones. It works a day at a
// time, so a catch-up doesn't hold one long write lock.
func Rollup(ctx context.Context, db *sql.DB, resolution Resolution, from, to, now time.Time) (RollupResult, error) {
	from = floorTime(from, resolution.Interval)
	to = ceilTime(to, resolution.Interval)
	if earliest := ceilTime(now.Add(-SampleRetention), resolution.Interval); from.Before(earliest) {
		from = earliest
	}
	result := RollupResult{From: from, To: from}

	chunk := max(24*time.Hour, resolution.Interval)
	step := int64(resolution.Interval / time.Second)
	for start := from; start.Before(to); start = start.Add(chunk) {
		end := start.Add(chunk)
		if end.After(to) {
			end = to
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return result, err
		}
		var rows int64
		for _, t := range Tables {
			res, err := tx.ExecContext(ctx, t.rollup(), resolution.Key, step, step, start.Unix(), end.Unix())
			if err != nil {
				tx.Rollback()
				return result, fmt.Errorf("%s rollup (%s, %s to %s): %w", t.rollupTable(), resolution.Key,
					start.Format(time.RFC3339), end.Format(time.RFC3339), err)
			}
			affected, _ := res.RowsAffected()
			rows += affected
		}
		if err := tx.Commit(); err != nil {
			return result, err
		}
		result.To, result.Rows = end, result.Rows+rows
	}

	return result, nil
}

// ScheduledRollup is what the scheduler runs: the last few buckets of a
// resolution up to the current one (which isn't over yet), or every bucket
// since the newest one stored if that's further back, so an Echowa that was
// stopped for a while catches up as far as the samples go.
func ScheduledRollup(ctx context.Context, db *sql.DB, resolution Resolution, now time.Time) (RollupResult, error) {
	to := floorTime(now, resolution.Interval)
	from := to.Add(-resolution.redo)

	var newest sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT max(bucket) FROM host_rollups WHERE resolution = ?", resolution.Key).
		Scan(&newest); err != nil {
		return RollupResult{}, err
	}
	if !newest.Valid {
		// Nothing rolled up yet: everything the samples cover.
		from = time.Time{}
	} else if next := time.Unix(newest.Int64, 0).Add(resolution.Interval); next.Before(from) {
		from = next
	}

	return Rollup(ctx, db, resolution, from, to, now)
}

// rollup recomputes one table's buckets in [from, to) from its samples.
// Averages are weighted by readings, counting only the rows that measured
// the figure; maxes are the largest max, mins the smallest min.
//
//	args: resolution, step, step, from, to (Unix seconds)
func (t Table) rollup() string {
	columns := []string{"resolution", "bucket"}
	values := []string{"?", "(ts / ?) * ?"}
	groupBy := "2"
	if t.Key != "" {
		columns = append(columns, t.Key)
		values = append(values, t.Key)
		groupBy += ", 3"
	}
	columns = append(columns, "readings")
	values = append(values, "sum(readings)")
	for _, f := range t.Figures {
		columns = append(columns, f.Name+"_avg", f.Name+"_max")
		values = append(values, weightedAvg(f.Name), "max("+f.Name+"_max)")
		if f.Min {
			columns = append(columns, f.Name+"_min")
			values = append(values, "min("+f.Name+"_min)")
		}
	}

	return fmt.Sprintf("INSERT OR REPLACE INTO %s (%s)\nSELECT %s\nFROM %s\nWHERE ts >= ? AND ts < ?\nGROUP BY %s",
		t.rollupTable(), strings.Join(columns, ", "), strings.Join(values, ", "), t.Name, groupBy)
}

// weightedAvg averages a figure's averages over sample or rollup rows,
// weighted by how many readings each holds.
func weightedAvg(figure string) string {
	return fmt.Sprintf("sum(%[1]s_avg * readings) / sum(CASE WHEN %[1]s_avg IS NOT NULL THEN readings END)", figure)
}

func (t Table) rollupTable() string {
	return strings.TrimSuffix(t.Name, "_samples") + "_rollups"
}

func floorTime(t time.Time, d time.Duration) time.Time {
	seconds := int64(d / time.Second)

	return time.Unix(t.Unix()/seconds*seconds, 0).UTC()
}

func ceilTime(t time.Time, d time.Duration) time.Time {
	floor := floorTime(t, d)
	if floor.Before(t) {
		return floor.Add(d)
	}

	return floor
}
