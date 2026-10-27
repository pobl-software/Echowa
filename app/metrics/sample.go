package metrics

import (
	"math"
	"sort"
	"time"
)

// A Table is one sample table: the column naming the entity (empty for the
// host, which has one row a minute) and its figures. Each figure is stored
// as <name>_avg and <name>_max, plus <name>_min where Min is set, and every
// row has a readings count (ADR 0024).
type Table struct {
	Name    string
	Key     string
	Figures []Figure
	// entries picks this table's entities out of a reading, with values in
	// Figures order.
	entries func(Reading) []entry
}

type Figure struct {
	Name string
	Min  bool
}

type entry struct {
	key    string
	values []*float64
}

const appKey = "app_id"

// The sample tables (ADR 0024). Each has a rollup table of the same shape,
// with resolution and bucket in place of ts.
var (
	HostTable = Table{
		Name: "host_samples",
		Figures: figures("cpu_percent", "iowait_percent", "steal_percent", "load_1m", "load_5m", "load_15m",
			"memory_used_bytes", "memory_available_bytes", "memory_total_bytes", "swap_used_bytes", "swap_total_bytes", "containers"),
		entries: func(r Reading) []entry {
			h := r.Host
			return []entry{{values: []*float64{
				h.CPUPercent, h.IOWaitPercent, h.StealPercent, h.Load1, h.Load5, h.Load15,
				float(h.MemoryUsed), float(h.MemoryAvailable), float(h.MemoryTotal), float(h.SwapUsed), float(h.SwapTotal), float(h.Containers),
			}}}
		},
	}
	DiskTable = Table{
		Name: "disk_samples",
		Key:  "mount",
		Figures: []Figure{
			{Name: "total_bytes"}, {Name: "used_bytes"}, {Name: "available_bytes", Min: true}, {Name: "inodes_total"}, {Name: "inodes_free"},
		},
		entries: func(r Reading) []entry {
			var es []entry
			for _, d := range r.Disks {
				es = append(es, entry{d.Mount, values(d.TotalBytes, d.UsedBytes, d.AvailableBytes, d.InodesTotal, d.InodesFree)})
			}
			return es
		},
	}
	DiskIOTable = Table{
		Name:    "disk_io_samples",
		Key:     "device",
		Figures: figures("read_bytes_per_sec", "write_bytes_per_sec", "read_ops_per_sec", "write_ops_per_sec", "busy_percent"),
		entries: func(r Reading) []entry {
			var es []entry
			for _, d := range r.DiskIO {
				es = append(es, entry{d.Device, []*float64{d.ReadBytesPerSec, d.WriteBytesPerSec, d.ReadsPerSec, d.WritesPerSec, d.BusyPercent}})
			}
			return es
		},
	}
	NetworkTable = Table{
		Name: "network_samples",
		Key:  "interface",
		Figures: figures("rx_bytes_per_sec", "tx_bytes_per_sec", "rx_errors_per_sec", "tx_errors_per_sec",
			"rx_drops_per_sec", "tx_drops_per_sec"),
		entries: func(r Reading) []entry {
			var es []entry
			for _, n := range r.Network {
				es = append(es, entry{n.Name, []*float64{n.RxBytesPerSec, n.TxBytesPerSec, n.RxErrorsPerSec, n.TxErrorsPerSec, n.RxDropsPerSec, n.TxDropsPerSec}})
			}
			return es
		},
	}
	// Keyed by app name in memory, and by app ID once written.
	AppTable = Table{
		Name: "app_samples",
		Key:  appKey,
		Figures: figures("tasks", "cpu_percent", "memory_used_bytes", "memory_limit_bytes",
			"net_rx_bytes_per_sec", "net_tx_bytes_per_sec", "block_read_bytes_per_sec", "block_write_bytes_per_sec"),
		entries: func(r Reading) []entry {
			var es []entry
			for _, a := range r.Apps {
				tasks, used, limit := float64(a.Tasks), float64(a.MemoryUsed), float64(a.MemoryLimit)
				es = append(es, entry{a.Name, []*float64{&tasks, a.CPUPercent, &used, &limit,
					a.NetRxBytesPerSec, a.NetTxBytesPerSec, a.BlockReadBytesPerSec, a.BlockWriteBytesPerSec}})
			}
			return es
		},
	}
	// Written once a minute by DatabaseSample, not from the 15-second
	// readings (ADR 0027). Keyed like AppTable.
	DatabaseTable = Table{
		Name: "database_samples",
		Key:  appKey,
		Figures: figures("size_bytes", "connections", "max_connections", "cache_hit_percent",
			"commits_per_sec", "rollbacks_per_sec", "temp_bytes_per_sec", "deadlocks_per_sec",
			"longest_transaction_seconds", "idle_in_transaction", "transaction_id_age", "evicted_keys_per_sec"),
		entries: func(Reading) []entry { return nil },
	}
)

// Tables are the sample tables, in the order a Sample holds their rows.
var Tables = []Table{HostTable, DiskTable, DiskIOTable, NetworkTable, AppTable, DatabaseTable}

// Sample is one minute of readings combined: per table, one row per entity.
type Sample struct {
	// At is the start of the minute.
	At   time.Time
	Rows [][]Row
}

type Row struct {
	Key string
	// Readings is how many of the minute's readings saw this entity.
	Readings int
	// Figures are in the table's Figures order.
	Figures []Stat
}

// Stat is one figure over a minute. All nil when no reading measured it.
type Stat struct {
	Avg, Max, Min *float64
}

// Minute combines the readings of the current minute. The first reading of
// a new minute closes the previous one.
type Minute struct {
	start time.Time
	// Per table, by entity key. Nil before the first reading.
	tables []map[string]*tally
}

type tally struct {
	readings int
	figures  []figureTally
}

type figureTally struct {
	n             int
	sum, max, min float64
}

// Add takes a reading into its minute. When the reading starts a new
// minute, it returns the previous one, complete.
func (m *Minute) Add(r Reading) (Sample, bool) {
	start := r.At.Truncate(time.Minute)
	var closed Sample
	done := m.tables != nil && !start.Equal(m.start)
	if done {
		closed = m.sample()
		m.tables = nil
	}
	if m.tables == nil {
		m.start = start
		m.tables = make([]map[string]*tally, len(Tables))
		for i := range Tables {
			m.tables[i] = map[string]*tally{}
		}
	}

	for i, t := range Tables {
		for _, e := range t.entries(r) {
			tl := m.tables[i][e.key]
			if tl == nil {
				tl = &tally{figures: make([]figureTally, len(t.Figures))}
				m.tables[i][e.key] = tl
			}
			tl.readings++
			for j, v := range e.values {
				if v != nil {
					tl.figures[j].add(*v)
				}
			}
		}
	}

	return closed, done
}

func (f *figureTally) add(v float64) {
	if f.n == 0 {
		f.max, f.min = v, v
	}
	f.n++
	f.sum += v
	f.max = math.Max(f.max, v)
	f.min = math.Min(f.min, v)
}

func (m *Minute) sample() Sample {
	s := Sample{At: m.start, Rows: make([][]Row, len(Tables))}
	for i, entities := range m.tables {
		for key, tl := range entities {
			row := Row{Key: key, Readings: tl.readings, Figures: make([]Stat, len(tl.figures))}
			for j, f := range tl.figures {
				if f.n > 0 {
					row.Figures[j] = Stat{Avg: ptr(f.sum / float64(f.n)), Max: ptr(f.max), Min: ptr(f.min)}
				}
			}
			s.Rows[i] = append(s.Rows[i], row)
		}
		sort.Slice(s.Rows[i], func(a, b int) bool { return s.Rows[i][a].Key < s.Rows[i][b].Key })
	}

	return s
}

func figures(names ...string) []Figure {
	fs := make([]Figure, len(names))
	for i, n := range names {
		fs[i] = Figure{Name: n}
	}

	return fs
}

func values(vs ...uint64) []*float64 {
	out := make([]*float64, len(vs))
	for i, v := range vs {
		out[i] = ptr(float64(v))
	}

	return out
}

func float[T uint64 | int](p *T) *float64 {
	if p == nil {
		return nil
	}

	return ptr(float64(*p))
}
