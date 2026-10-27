package metrics

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// PruneBatchSize is how many rows each delete removes, so pruning never
// holds the write lock for long.
const PruneBatchSize = 5000

// Prune deletes the rows past retention (ADR 0024), and the rows of apps
// that aren't in appIDs any more, which deleting an app normally removes
// already. It returns how many rows went.
func Prune(ctx context.Context, db *sql.DB, now time.Time, appIDs []uint, batchSize int) (int64, error) {
	type target struct {
		table, where string
		args         []any
	}
	var targets []target
	for _, t := range Tables {
		targets = append(targets,
			target{t.Name, "ts < ?", []any{now.Add(-SampleRetention).Unix()}},
			target{t.rollupTable(), "resolution = ? AND bucket < ?", []any{Hourly.Key, now.Add(-Hourly.retention).Unix()}},
			target{t.rollupTable(), "resolution = ? AND bucket < ?", []any{Daily.Key, now.Add(-Daily.retention).Unix()}},
		)
	}
	ids := make([]any, len(appIDs))
	for i, id := range appIDs {
		ids[i] = id
	}
	gone := "app_id NOT IN (" + strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ") + ")"
	for _, table := range appTables() {
		targets = append(targets, target{table, gone, ids})
	}

	var total int64
	for _, t := range targets {
		statement := fmt.Sprintf("DELETE FROM %[1]s WHERE rowid IN (SELECT rowid FROM %[1]s WHERE %[2]s LIMIT ?)", t.table, t.where)
		for {
			res, err := db.ExecContext(ctx, statement, append(t.args, batchSize)...)
			if err != nil {
				return total, fmt.Errorf("pruning %s: %w", t.table, err)
			}
			deleted, _ := res.RowsAffected()
			total += deleted
			if deleted < int64(batchSize) {
				break
			}
		}
	}

	return total, nil
}
