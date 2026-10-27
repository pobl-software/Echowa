package metrics

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ncruces/go-sqlite3"
)

// migrations are the metrics file's schema, one entry per version, applied
// in order and recorded in PRAGMA user_version rather than Goravel's
// migrations table, which lives in the main database (ADR 0024).
//
// Only ever append, and only add: an updater rollback runs the previous
// release against a file a newer one has upgraded, so older code must still
// be able to write to it.
var migrations = []string{
	`CREATE TABLE host_samples (
		ts INTEGER NOT NULL PRIMARY KEY,
		readings INTEGER NOT NULL,
		cpu_percent_avg REAL, cpu_percent_max REAL,
		iowait_percent_avg REAL, iowait_percent_max REAL,
		steal_percent_avg REAL, steal_percent_max REAL,
		load_1m_avg REAL, load_1m_max REAL,
		load_5m_avg REAL, load_5m_max REAL,
		load_15m_avg REAL, load_15m_max REAL,
		memory_used_bytes_avg REAL, memory_used_bytes_max REAL,
		memory_available_bytes_avg REAL, memory_available_bytes_max REAL,
		memory_total_bytes_avg REAL, memory_total_bytes_max REAL,
		swap_used_bytes_avg REAL, swap_used_bytes_max REAL,
		swap_total_bytes_avg REAL, swap_total_bytes_max REAL,
		containers_avg REAL, containers_max REAL
	);
	CREATE TABLE disk_samples (
		mount TEXT NOT NULL,
		ts INTEGER NOT NULL,
		readings INTEGER NOT NULL,
		total_bytes_avg REAL, total_bytes_max REAL,
		used_bytes_avg REAL, used_bytes_max REAL,
		available_bytes_avg REAL, available_bytes_max REAL, available_bytes_min REAL,
		inodes_total_avg REAL, inodes_total_max REAL,
		inodes_free_avg REAL, inodes_free_max REAL,
		PRIMARY KEY (mount, ts)
	);
	CREATE TABLE disk_io_samples (
		device TEXT NOT NULL,
		ts INTEGER NOT NULL,
		readings INTEGER NOT NULL,
		read_bytes_per_sec_avg REAL, read_bytes_per_sec_max REAL,
		write_bytes_per_sec_avg REAL, write_bytes_per_sec_max REAL,
		read_ops_per_sec_avg REAL, read_ops_per_sec_max REAL,
		write_ops_per_sec_avg REAL, write_ops_per_sec_max REAL,
		busy_percent_avg REAL, busy_percent_max REAL,
		PRIMARY KEY (device, ts)
	);
	CREATE TABLE network_samples (
		interface TEXT NOT NULL,
		ts INTEGER NOT NULL,
		readings INTEGER NOT NULL,
		rx_bytes_per_sec_avg REAL, rx_bytes_per_sec_max REAL,
		tx_bytes_per_sec_avg REAL, tx_bytes_per_sec_max REAL,
		rx_errors_per_sec_avg REAL, rx_errors_per_sec_max REAL,
		tx_errors_per_sec_avg REAL, tx_errors_per_sec_max REAL,
		rx_drops_per_sec_avg REAL, rx_drops_per_sec_max REAL,
		tx_drops_per_sec_avg REAL, tx_drops_per_sec_max REAL,
		PRIMARY KEY (interface, ts)
	);
	CREATE TABLE app_samples (
		app_id INTEGER NOT NULL,
		ts INTEGER NOT NULL,
		readings INTEGER NOT NULL,
		tasks_avg REAL, tasks_max REAL,
		cpu_percent_avg REAL, cpu_percent_max REAL,
		memory_used_bytes_avg REAL, memory_used_bytes_max REAL,
		memory_limit_bytes_avg REAL, memory_limit_bytes_max REAL,
		net_rx_bytes_per_sec_avg REAL, net_rx_bytes_per_sec_max REAL,
		net_tx_bytes_per_sec_avg REAL, net_tx_bytes_per_sec_max REAL,
		block_read_bytes_per_sec_avg REAL, block_read_bytes_per_sec_max REAL,
		block_write_bytes_per_sec_avg REAL, block_write_bytes_per_sec_max REAL,
		PRIMARY KEY (app_id, ts)
	);`,
	// Rollups: the samples in hourly and daily buckets, in the same shape.
	`CREATE TABLE host_rollups (
		resolution TEXT NOT NULL,
		bucket INTEGER NOT NULL,
		readings INTEGER NOT NULL,
		cpu_percent_avg REAL, cpu_percent_max REAL,
		iowait_percent_avg REAL, iowait_percent_max REAL,
		steal_percent_avg REAL, steal_percent_max REAL,
		load_1m_avg REAL, load_1m_max REAL,
		load_5m_avg REAL, load_5m_max REAL,
		load_15m_avg REAL, load_15m_max REAL,
		memory_used_bytes_avg REAL, memory_used_bytes_max REAL,
		memory_available_bytes_avg REAL, memory_available_bytes_max REAL,
		memory_total_bytes_avg REAL, memory_total_bytes_max REAL,
		swap_used_bytes_avg REAL, swap_used_bytes_max REAL,
		swap_total_bytes_avg REAL, swap_total_bytes_max REAL,
		containers_avg REAL, containers_max REAL,
		PRIMARY KEY (resolution, bucket)
	);
	CREATE TABLE disk_rollups (
		mount TEXT NOT NULL,
		resolution TEXT NOT NULL,
		bucket INTEGER NOT NULL,
		readings INTEGER NOT NULL,
		total_bytes_avg REAL, total_bytes_max REAL,
		used_bytes_avg REAL, used_bytes_max REAL,
		available_bytes_avg REAL, available_bytes_max REAL, available_bytes_min REAL,
		inodes_total_avg REAL, inodes_total_max REAL,
		inodes_free_avg REAL, inodes_free_max REAL,
		PRIMARY KEY (mount, resolution, bucket)
	);
	CREATE TABLE disk_io_rollups (
		device TEXT NOT NULL,
		resolution TEXT NOT NULL,
		bucket INTEGER NOT NULL,
		readings INTEGER NOT NULL,
		read_bytes_per_sec_avg REAL, read_bytes_per_sec_max REAL,
		write_bytes_per_sec_avg REAL, write_bytes_per_sec_max REAL,
		read_ops_per_sec_avg REAL, read_ops_per_sec_max REAL,
		write_ops_per_sec_avg REAL, write_ops_per_sec_max REAL,
		busy_percent_avg REAL, busy_percent_max REAL,
		PRIMARY KEY (device, resolution, bucket)
	);
	CREATE TABLE network_rollups (
		interface TEXT NOT NULL,
		resolution TEXT NOT NULL,
		bucket INTEGER NOT NULL,
		readings INTEGER NOT NULL,
		rx_bytes_per_sec_avg REAL, rx_bytes_per_sec_max REAL,
		tx_bytes_per_sec_avg REAL, tx_bytes_per_sec_max REAL,
		rx_errors_per_sec_avg REAL, rx_errors_per_sec_max REAL,
		tx_errors_per_sec_avg REAL, tx_errors_per_sec_max REAL,
		rx_drops_per_sec_avg REAL, rx_drops_per_sec_max REAL,
		tx_drops_per_sec_avg REAL, tx_drops_per_sec_max REAL,
		PRIMARY KEY (interface, resolution, bucket)
	);
	CREATE TABLE app_rollups (
		app_id INTEGER NOT NULL,
		resolution TEXT NOT NULL,
		bucket INTEGER NOT NULL,
		readings INTEGER NOT NULL,
		tasks_avg REAL, tasks_max REAL,
		cpu_percent_avg REAL, cpu_percent_max REAL,
		memory_used_bytes_avg REAL, memory_used_bytes_max REAL,
		memory_limit_bytes_avg REAL, memory_limit_bytes_max REAL,
		net_rx_bytes_per_sec_avg REAL, net_rx_bytes_per_sec_max REAL,
		net_tx_bytes_per_sec_avg REAL, net_tx_bytes_per_sec_max REAL,
		block_read_bytes_per_sec_avg REAL, block_read_bytes_per_sec_max REAL,
		block_write_bytes_per_sec_avg REAL, block_write_bytes_per_sec_max REAL,
		PRIMARY KEY (app_id, resolution, bucket)
	);`,
	// A template database's own figures, once a minute (ADR 0027).
	`CREATE TABLE database_samples (
		app_id INTEGER NOT NULL,
		ts INTEGER NOT NULL,
		readings INTEGER NOT NULL,
		size_bytes_avg REAL, size_bytes_max REAL,
		connections_avg REAL, connections_max REAL,
		max_connections_avg REAL, max_connections_max REAL,
		cache_hit_percent_avg REAL, cache_hit_percent_max REAL,
		commits_per_sec_avg REAL, commits_per_sec_max REAL,
		rollbacks_per_sec_avg REAL, rollbacks_per_sec_max REAL,
		temp_bytes_per_sec_avg REAL, temp_bytes_per_sec_max REAL,
		deadlocks_per_sec_avg REAL, deadlocks_per_sec_max REAL,
		longest_transaction_seconds_avg REAL, longest_transaction_seconds_max REAL,
		idle_in_transaction_avg REAL, idle_in_transaction_max REAL,
		transaction_id_age_avg REAL, transaction_id_age_max REAL,
		evicted_keys_per_sec_avg REAL, evicted_keys_per_sec_max REAL,
		PRIMARY KEY (app_id, ts)
	);
	CREATE TABLE database_rollups (
		app_id INTEGER NOT NULL,
		resolution TEXT NOT NULL,
		bucket INTEGER NOT NULL,
		readings INTEGER NOT NULL,
		size_bytes_avg REAL, size_bytes_max REAL,
		connections_avg REAL, connections_max REAL,
		max_connections_avg REAL, max_connections_max REAL,
		cache_hit_percent_avg REAL, cache_hit_percent_max REAL,
		commits_per_sec_avg REAL, commits_per_sec_max REAL,
		rollbacks_per_sec_avg REAL, rollbacks_per_sec_max REAL,
		temp_bytes_per_sec_avg REAL, temp_bytes_per_sec_max REAL,
		deadlocks_per_sec_avg REAL, deadlocks_per_sec_max REAL,
		longest_transaction_seconds_avg REAL, longest_transaction_seconds_max REAL,
		idle_in_transaction_avg REAL, idle_in_transaction_max REAL,
		transaction_id_age_avg REAL, transaction_id_age_max REAL,
		evicted_keys_per_sec_avg REAL, evicted_keys_per_sec_max REAL,
		PRIMARY KEY (app_id, resolution, bucket)
	);`,
}

// Open opens the metrics file at path with open, and brings its schema up
// to date. A file SQLite can't read as a database, or that fails its
// integrity check, is moved aside as <path>.broken-<time> and replaced
// with an empty one: metrics are only charts, and Echowa should start
// regardless. moved names the file moved aside, if any.
func Open(ctx context.Context, path string, open func(string) (*sql.DB, error), now time.Time) (db *sql.DB, moved string, err error) {
	db, err = openAndMigrate(ctx, path, open)
	if !errors.Is(err, sqlite3.NOTADB) && !errors.Is(err, sqlite3.CORRUPT) && !errors.Is(err, errDamaged) {
		return db, "", err
	}

	moved = path + ".broken-" + now.UTC().Format("20060102-150405")
	if err := os.Rename(path, moved); err != nil {
		return nil, "", err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Rename(path+suffix, moved+suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, moved, err
		}
	}
	db, err = openAndMigrate(ctx, path, open)

	return db, moved, err
}

var errDamaged = errors.New("integrity check failed")

func openAndMigrate(ctx context.Context, path string, open func(string) (*sql.DB, error)) (*sql.DB, error) {
	db, err := open(path)
	if err != nil {
		return nil, err
	}
	if err := check(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func check(ctx context.Context, db *sql.DB) error {
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check(1)").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("%w: %s", errDamaged, result)
	}

	return nil
}

// migrate applies the versions the file hasn't had, each in a transaction
// with its user_version. A file from a newer release is left as it is.
func migrate(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	for v := version; v < len(migrations); v++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[v]); err != nil {
			tx.Rollback()
			return fmt.Errorf("metrics schema version %d: %w", v+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}

	return nil
}

// Write stores a closed minute in one transaction. appIDs maps app names to
// IDs; rows of apps no longer in it are dropped.
func Write(ctx context.Context, db *sql.DB, s Sample, appIDs map[string]uint) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for i, t := range Tables {
		insert := t.insert()
		for _, row := range s.Rows[i] {
			args := []any{s.At.Unix()}
			switch t.Key {
			case "":
			case appKey:
				id, ok := appIDs[row.Key]
				if !ok {
					continue
				}
				args = append(args, id)
			default:
				args = append(args, row.Key)
			}
			args = append(args, row.Readings)
			for j, f := range t.Figures {
				stat := row.Figures[j]
				args = append(args, nullable(stat.Avg), nullable(stat.Max))
				if f.Min {
					args = append(args, nullable(stat.Min))
				}
			}
			if _, err := tx.ExecContext(ctx, insert, args...); err != nil {
				return fmt.Errorf("%s: %w", t.Name, err)
			}
		}
	}

	return tx.Commit()
}

// insert replaces the row for the same minute and entity: there's only one
// unless the clock went back.
func (t Table) insert() string {
	columns := []string{"ts"}
	if t.Key != "" {
		columns = append(columns, t.Key)
	}
	columns = append(columns, "readings")
	for _, f := range t.Figures {
		columns = append(columns, f.Name+"_avg", f.Name+"_max")
		if f.Min {
			columns = append(columns, f.Name+"_min")
		}
	}

	return fmt.Sprintf("INSERT OR REPLACE INTO %s (%s) VALUES (%s)",
		t.Name, strings.Join(columns, ", "), strings.TrimSuffix(strings.Repeat("?, ", len(columns)), ", "))
}

// DeleteApp removes a deleted app's history, samples and rollups.
func DeleteApp(ctx context.Context, db *sql.DB, appID uint) error {
	for _, table := range appTables() {
		if _, err := db.ExecContext(ctx, "DELETE FROM "+table+" WHERE app_id = ?", appID); err != nil {
			return err
		}
	}

	return nil
}

// appTables are the sample and rollup tables keyed by app ID.
func appTables() []string {
	var names []string
	for _, t := range Tables {
		if t.Key == appKey {
			names = append(names, t.Name, t.rollupTable())
		}
	}

	return names
}

func nullable(p *float64) any {
	if p == nil {
		return nil
	}

	return *p
}
