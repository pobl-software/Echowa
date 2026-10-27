// Shapes of the metrics props. Keep in sync with app/metrics (reading.go,
// query.go, database.go) and app/services (server_metrics.go,
// app_metrics.go, databases.go).

// metrics.Point: one bucket, at its start (Unix seconds). Null values mark
// a gap.
export interface Point {
    t: number;
    avg: number | null;
    max: number | null;
}

// metrics.Window
export interface SeriesWindow {
    range: string;
    from: number;
    to: number;
    step_seconds: number;
}

// services.ServerSeries
export interface ServerSeries extends SeriesWindow {
    cpu: { usage: Point[]; iowait: Point[]; steal: Point[] };
    load: { load_1m: Point[]; load_5m: Point[]; load_15m: Point[] };
    memory: Point[];
    swap: Point[];
    disks: { mount: string; percent: Point[] }[];
    disk_io: { device: string; read: Point[]; write: Point[] }[];
    network: { interface: string; rx: Point[]; tx: Point[] }[];
}

// services.AppSeries: one app's figures, summed over its tasks.
export interface AppSeries extends SeriesWindow {
    // A percentage of one core.
    cpu: Point[];
    tasks: Point[];
    memory: Point[];
    network: { rx: Point[]; tx: Point[] };
    block: { read: Point[]; write: Point[] };
}

// services.DatabaseSeries: a template database's own figures (ADR 0027).
// Rates are per second.
export interface DatabaseSeries extends SeriesWindow {
    size: Point[];
    connections: Point[];
    max_connections: Point[];
    cache_hit: Point[];
    commits: Point[];
    rollbacks: Point[];
    temp_bytes: Point[];
    deadlocks: Point[];
    longest_transaction: Point[];
    idle_in_transaction: Point[];
    transaction_id_age: Point[];
    evicted_keys: Point[];
}

// metrics.Database: the newest check. Null figures aren't reported by
// the database; rates are null on the first check after a start.
export interface DatabaseFigures {
    app: string;
    size_bytes: number | null;
    connections: number | null;
    max_connections: number | null;
    cache_hit_percent: number | null;
    commits_per_sec: number | null;
    rollbacks_per_sec: number | null;
    temp_bytes_per_sec: number | null;
    deadlocks_per_sec: number | null;
    longest_transaction_seconds: number | null;
    idle_in_transaction: number | null;
    transaction_id_age: number | null;
    evicted_keys_per_sec: number | null;
}

// services.DatabaseLive: the newest check, with what its meters measure
// it against. disk holds Docker's data, and so the volume.
export interface DatabaseLive extends DatabaseFigures {
    disk: Reading['disks'][number] | null;
    memory_used_bytes: number | null;
    host_memory_total_bytes: number | null;
    host_memory_available_bytes: number | null;
}

export type AppReading = Reading['apps'][number];

// services.AppLive: the app's part of the newest reading. memory_limited
// is false when its tasks have no limit of their own, and Docker reports
// the host's memory as the limit.
export interface AppLive extends AppReading {
    memory_limited: boolean;
}

// metrics.Reading: the newest reading, live from memory. Null figures
// weren't measured; rates are null on the first reading after a start.
export interface Reading {
    at: string;
    host: {
        cpu_percent: number | null;
        iowait_percent: number | null;
        steal_percent: number | null;
        cpu_count: number | null;
        load_1m: number | null;
        load_5m: number | null;
        load_15m: number | null;
        memory_total_bytes: number | null;
        memory_used_bytes: number | null;
        memory_available_bytes: number | null;
        swap_total_bytes: number | null;
        swap_used_bytes: number | null;
        // Null when Docker didn't answer.
        containers: number | null;
    };
    disks: {
        mount: string;
        device: string;
        filesystem: string;
        total_bytes: number;
        used_bytes: number;
        available_bytes: number;
        inodes_total: number;
        inodes_free: number;
    }[];
    disk_io: {
        device: string;
        read_bytes_per_sec: number | null;
        write_bytes_per_sec: number | null;
    }[];
    network: {
        interface: string;
        rx_bytes_per_sec: number | null;
        tx_bytes_per_sec: number | null;
    }[];
    apps: {
        app: string;
        tasks: number;
        cpu_percent: number | null;
        memory_used_bytes: number;
        memory_limit_bytes: number;
        net_rx_bytes_per_sec: number | null;
        net_tx_bytes_per_sec: number | null;
        block_read_bytes_per_sec: number | null;
        block_write_bytes_per_sec: number | null;
    }[];
    other_containers: {
        name: string;
        cpu_percent: number | null;
        memory_used_bytes: number;
        memory_limit_bytes: number;
    }[];
}

// A disk's fill level as df shows it: used of what a user can use.
export function diskPercent(disk: Reading['disks'][number]): number | null {
    const usable = disk.used_bytes + disk.available_bytes;
    return usable > 0 ? (100 * disk.used_bytes) / usable : null;
}

export function memoryPercent(host: Reading['host']): number | null {
    return host.memory_used_bytes != null && host.memory_total_bytes
        ? (100 * host.memory_used_bytes) / host.memory_total_bytes
        : null;
}

// The root filesystem, or the first one if / isn't reported.
export function rootDisk(reading: Reading): Reading['disks'][number] | undefined {
    return reading.disks.find((disk) => disk.mount === '/') ?? reading.disks[0];
}
