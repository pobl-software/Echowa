package metrics

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// Range is a time range the charts can show.
type Range struct {
	Key      string
	Duration time.Duration
}

// Ranges are the ranges the range picker offers, shortest first. Up to
// samplesUpTo they read samples, and beyond, hourly rollups.
var Ranges = []Range{
	{"1h", time.Hour},
	{"6h", 6 * time.Hour},
	{"24h", 24 * time.Hour},
	{"7d", 7 * 24 * time.Hour},
	{"30d", 30 * 24 * time.Hour},
	{"90d", 90 * 24 * time.Hour},
}

// DefaultRange is shown when the query string doesn't pick one.
const DefaultRange = "24h"

const samplesUpTo = 24 * time.Hour

// ParseRange returns the range with the given key, or the default.
func ParseRange(key string) Range {
	for _, r := range Ranges {
		if r.Key == key {
			return r
		}
	}

	return ParseRange(DefaultRange)
}

// maxPoints is the most points a series has.
const maxPoints = 400

// niceSteps are the bucket sizes a series can have, so axis ticks and
// tooltips land on round times. A minute is the finest: samples are a
// minute apart.
var niceSteps = []time.Duration{
	time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 2 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour,
}

// stepFor picks the bucket size for a range: the finest nice step that
// keeps it to maxPoints, and no finer than the rows it reads.
func stepFor(span, finest time.Duration) time.Duration {
	for _, step := range niceSteps {
		if span/step <= maxPoints && step >= finest {
			return step
		}
	}

	return niceSteps[len(niceSteps)-1]
}

// Point is one bucket of a series: the average and the peak in it, at the
// bucket's start (Unix seconds). A point with nil values marks a gap, where
// nothing was stored, so charts break the line there.
type Point struct {
	T   int64    `json:"t"`
	Avg *float64 `json:"avg"`
	Max *float64 `json:"max"`
}

// Window is the range a page's series cover, in Unix seconds, and their
// bucket size. Every series has its buckets on the same boundaries
// (multiples of the step since the Unix epoch), so a page can line them up
// by t.
type Window struct {
	Range       string `json:"range"`
	From        int64  `json:"from"`
	To          int64  `json:"to"`
	StepSeconds int64  `json:"step_seconds"`
}

// Series are one table's points for a window, per entity and figure.
type Series struct {
	Window
	// Keys are the entities (mounts, devices, interfaces, app IDs) in
	// order, or the one "" for the host.
	Keys   []string
	points map[string]map[string][]Point
}

// Get returns one series with its gaps marked, or an empty one if it has no
// values at all.
func (s *Series) Get(key, figure string) []Point {
	points := s.points[key][figure]
	for _, p := range points {
		if p.Avg != nil {
			return withGaps(points, s.StepSeconds)
		}
	}

	return []Point{}
}

// Query returns the given figures of a table for the range ending at now,
// bucketed in SQL: from samples up to 24 hours, and from hourly rollups
// beyond, with the newest two steps still from samples because they aren't
// rolled up yet. where narrows the rows (such as "app_id = ?", with args);
// like figures, it's fixed SQL, never input.
func Query(ctx context.Context, db *sql.DB, t Table, figures []string, where string, args []any, r Range, now time.Time) (*Series, error) {
	for _, f := range figures {
		if !slices.ContainsFunc(t.Figures, func(tf Figure) bool { return tf.Name == f }) {
			return nil, fmt.Errorf("%s has no figure %q", t.Name, f)
		}
	}
	q := newSeriesQuery(r, now)
	series := &Series{Window: q.window, points: map[string]map[string][]Point{}}

	if q.rollups {
		filter := "resolution = '" + Hourly.Key + "'"
		if where != "" {
			filter += " AND " + where
		}
		if err := q.read(ctx, db, series, t.rollupTable(), "bucket", q.from, q.samplesFrom, false, t.Key, filter, figures, args); err != nil {
			return nil, err
		}
	}
	if err := q.read(ctx, db, series, t.Name, "ts", q.samplesFrom, q.to, true, t.Key, where, figures, args); err != nil {
		return nil, err
	}
	slices.Sort(series.Keys)

	return series, nil
}

// WindowFor is the window Query uses for a range ending at now, for pages
// that have nothing to query.
func WindowFor(r Range, now time.Time) Window {
	return newSeriesQuery(r, now).window
}

type seriesQuery struct {
	window   Window
	from, to time.Time
	step     time.Duration
	// Whether to read hourly rollups before samplesFrom.
	rollups     bool
	samplesFrom time.Time
}

// newSeriesQuery works out the source, step and window for a range ending
// at now. A step is never finer than its source.
func newSeriesQuery(r Range, now time.Time) seriesQuery {
	rollups := r.Duration > samplesUpTo
	finest := time.Minute
	if rollups {
		finest = Hourly.Interval
	}
	step := stepFor(r.Duration, finest)
	// Whole buckets only, so the first point isn't an average of a sliver.
	to := now.UTC()
	from := floorTime(to.Add(-r.Duration), step)

	q := seriesQuery{from: from, to: to, step: step, rollups: rollups, samplesFrom: from}
	if rollups {
		// The hourly rollup runs at :05 for the hour before, so the last two
		// steps (the current one and the one before) come from samples.
		q.samplesFrom = floorTime(to, step).Add(-step)
		if q.samplesFrom.Before(from) {
			q.samplesFrom = from
		}
	}
	q.window = Window{Range: r.Key, From: from.Unix(), To: to.Unix(), StepSeconds: int64(step / time.Second)}

	return q
}

// read is one bucketing query over samples or rollups, which have the same
// columns, adding its points after those already in series. The window ends
// at to, included or not.
func (q seriesQuery) read(ctx context.Context, db *sql.DB, series *Series, table, timeColumn string, from, to time.Time, toIncluded bool,
	key, where string, figures []string, args []any) error {
	keyExpr := "''"
	if key != "" {
		keyExpr = "CAST(" + key + " AS TEXT)"
	}
	upper := "<"
	if toIncluded {
		upper = "<="
	}
	filter := fmt.Sprintf("%[1]s >= ? AND %[1]s %[2]s ?", timeColumn, upper)
	if where != "" {
		filter += " AND " + where
	}
	columns := make([]string, 0, len(figures)*2)
	for _, f := range figures {
		columns = append(columns, weightedAvg(f), "max("+f+"_max)")
	}
	step := int64(q.step / time.Second)
	query := fmt.Sprintf("SELECT (%[1]s / ?) * ? AS t, %[2]s AS entity, %[3]s FROM %[4]s WHERE %[5]s GROUP BY 1, 2 ORDER BY 2, 1",
		timeColumn, keyExpr, strings.Join(columns, ", "), table, filter)

	params := append([]any{step, step, from.Unix(), to.Unix()}, args...)
	rows, err := db.QueryContext(ctx, query, params...)
	if err != nil {
		return fmt.Errorf("metrics query on %s: %w", table, err)
	}
	defer rows.Close()

	values := make([]sql.NullFloat64, len(figures)*2)
	var (
		t      int64
		entity string
	)
	dest := []any{&t, &entity}
	for i := range values {
		dest = append(dest, &values[i])
	}
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return fmt.Errorf("metrics query on %s: %w", table, err)
		}
		byFigure, ok := series.points[entity]
		if !ok {
			byFigure = map[string][]Point{}
			series.points[entity] = byFigure
			series.Keys = append(series.Keys, entity)
		}
		for i, f := range figures {
			byFigure[f] = append(byFigure[f], Point{T: t, Avg: rounded(values[i*2]), Max: rounded(values[i*2+1])})
		}
	}

	return rows.Err()
}

// withGaps adds an empty point after each stretch of more than one missing
// bucket, so a chart shows a stretch with nothing stored (Echowa stopped) as
// a break, not a straight line. A single missing bucket is left alone: a
// restart loses the minute in progress.
func withGaps(points []Point, stepSeconds int64) []Point {
	out := make([]Point, 0, len(points))
	for i, p := range points {
		if i > 0 && p.T-points[i-1].T > 2*stepSeconds {
			out = append(out, Point{T: points[i-1].T + stepSeconds})
		}
		out = append(out, p)
	}

	return out
}

// rounded keeps two decimal places, which is finer than any chart or
// tooltip shows and keeps the page's JSON about a third smaller.
func rounded(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}

	return ptr(math.Round(v.Float64*100) / 100)
}
