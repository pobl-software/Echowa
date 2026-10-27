import { Link, usePoll } from '@inertiajs/react';
import { ChevronRightIcon, DatabaseIcon, TriangleAlertIcon } from 'lucide-react';
import type { ComponentProps, MouseEvent, ReactNode } from 'react';

import { ResourceCharts } from '@/Components/AppMetrics';
import { MetricChart } from '@/Components/LazyMetricChart';
import { MeterBar, meterLevel } from '@/Components/Meter';
import { ChartCell, ChartGrid, Stats } from '@/Components/Panel';
import { RangePicker, useSeriesRange } from '@/Components/RangePicker';
import { SlowQueriesPanel } from '@/Components/SlowQueriesPanel';
import { Sparkline } from '@/Components/Sparkline';
import { Alert, AlertDescription, AlertTitle } from '@/Components/ui/alert';
import { formatBytes, formatCount, formatPercent, formatPerSecond, formatRate, formatSeconds } from '@/lib/format';
import { cn } from '@/lib/utils';
import {
    type AppLive,
    type AppSeries,
    type DatabaseLive,
    type DatabaseSeries,
    diskPercent,
    type Point,
} from '@/types/metrics';
import type { App, SlowQueryResult, TemplateOption } from '@/types/models';

// When a figure needs attention (ADR 0027). Disk and connections use the
// meters' own levels.
const LONG_TRANSACTION_SECONDS = 5 * 60;
const TRANSACTION_ID_AGE = 1_000_000_000;
const LOW_CACHE_HIT_PERCENT = 95;

// Redis reports commands where the others report commits, and its cache
// hits are lookups that found their key.
type Wording = { commits: string; transactions: string; cacheHits: string; cacheHitsOf: string; size: string };
const wording = (slug: string): Wording =>
    slug === 'redis'
        ? {
              commits: 'Commands',
              transactions: 'Commands',
              cacheHits:
                  'Lookups that found their key. Low for long, keys expire or are evicted before they’re read again.',
              cacheHitsOf: 'of lookups, last minute',
              size: 'Its volume: the append-only file and snapshots.',
          }
        : {
              commits: 'Commits',
              transactions: 'Transactions',
              cacheHits: `Reads served from memory. Below ${LOW_CACHE_HIT_PERCENT}% for long, the cache is too small for what’s read.`,
              cacheHitsOf: 'of reads, last minute',
              size:
                  slug === 'mysql'
                      ? 'Its data directory on disk, with the binary and redo logs.'
                      : 'Its data and write-ahead log on disk.',
          };

type Warning = { title: string; body: ReactNode; critical: boolean };

function warningsFor(live: DatabaseLive): Warning[] {
    const warnings: Warning[] = [];
    const disk = live.disk ? diskPercent(live.disk) : null;
    if (disk != null && meterLevel(disk) !== 'normal') {
        warnings.push({
            title: `The disk is ${formatPercent(disk)} full`,
            body: 'The database stops accepting writes when its disk is full. Free some space, or move to a bigger disk.',
            critical: meterLevel(disk) === 'critical',
        });
    }
    if (live.connections != null && live.max_connections) {
        const percent = (100 * live.connections) / live.max_connections;
        if (meterLevel(percent) !== 'normal') {
            warnings.push({
                title: `${formatCount(live.connections)} of ${formatCount(live.max_connections)} connections in use`,
                body: 'Once every connection is taken, apps get “too many clients” errors. Check for apps that hold connections open, or use a pool.',
                critical: meterLevel(percent) === 'critical',
            });
        }
    }
    if (live.longest_transaction_seconds != null && live.longest_transaction_seconds >= LONG_TRANSACTION_SECONDS) {
        const idle = live.idle_in_transaction ?? 0;
        warnings.push({
            title: `A transaction has been open for ${formatSeconds(live.longest_transaction_seconds)}`,
            body: `It holds its locks and stops old rows being cleaned up${idle > 0 ? `, and ${formatCount(idle)} ${idle === 1 ? 'session is' : 'sessions are'} idle in a transaction` : ''}. An app that crashed mid-transaction is the usual cause.`,
            critical: false,
        });
    }
    if (live.transaction_id_age != null && live.transaction_id_age >= TRANSACTION_ID_AGE) {
        warnings.push({
            title: 'Transaction IDs are running out',
            body: 'PostgreSQL refuses writes at about 2.1 billion. Something is stopping vacuum: look for a transaction left open.',
            critical: true,
        });
    }

    return warnings;
}

// Any point measured, so a figure the database doesn't report has no chart.
const measured = (...series: Point[][]) => series.some((points) => points.some((p) => p.avg != null));

const percentOf = (part: number | null | undefined, whole: number | null | undefined) =>
    part != null && whole ? (100 * part) / whole : null;

// DatabaseWarnings is an alert for each limit a template database is close
// to, on its Metrics section and its Overview.
export function DatabaseWarnings({ database }: { database: DatabaseLive }) {
    return warningsFor(database).map((w) => (
        <Alert key={w.title} variant={w.critical ? 'destructive' : 'default'}>
            <TriangleAlertIcon className={cn(!w.critical && 'text-warning')} />
            <AlertTitle>{w.title}</AlertTitle>
            <AlertDescription>{w.body}</AlertDescription>
        </Alert>
    ));
}

// A template database's Metrics section (ADR 0027): warnings, a strip of
// its vitals, its slow queries, then charts of what it reports about
// itself and of its container, for the range in ?range=. The range picker
// leads, since the strip's sparklines follow it too. Figures it doesn't
// report show as a dash, and have no chart.
export function DatabaseMetrics({
    app,
    template,
    ranges,
    series,
    metrics,
    databaseSeries,
    database,
    slowQueries,
}: {
    app: App;
    template: TemplateOption;
    ranges: string[];
    series: AppSeries;
    metrics: AppLive | null;
    databaseSeries: DatabaseSeries;
    database: DatabaseLive | null;
    slowQueries: SlowQueryResult | null | undefined;
}) {
    // Echowa reads the container every 15 seconds, and checks the database
    // once a minute.
    usePoll(15_000, { only: ['metrics', 'series'] });
    usePoll(30_000, { only: ['database', 'databaseSeries'] });
    const { range, loading, pick } = useSeriesRange(databaseSeries, ['series', 'databaseSeries']);

    const db = databaseSeries;
    const chart = { from: db.from, to: db.to, step: db.step_seconds, height: 'h-36' };
    const words = wording(template.slug);
    // On, slow queries are worth a place near the top; off, the switch waits at the end.
    const slowOn = template.slow_queries === 'always' || (template.slow_queries === 'switch' && app.slow_queries);
    const slowPanel = template.slow_queries && <SlowQueriesPanel app={app} template={template} result={slowQueries} />;
    const dim = cn('transition-opacity', loading && 'opacity-60');

    return (
        <>
            <RangePicker ranges={ranges} range={range} step={db.step_seconds} onPick={pick} />
            {database && <DatabaseWarnings database={database} />}
            <DatabaseVitals
                template={template}
                series={series}
                metrics={metrics}
                databaseSeries={db}
                database={database}
                className={cn('sm:grid-cols-3 lg:grid-cols-3 2xl:grid-cols-6', dim)}
            />
            {slowOn && slowPanel}
            <ChartGrid
                title={template.name}
                description="What it reports about itself, checked once a minute."
                className={dim}
            >
                <ChartCell title="Connections">
                    <MetricChart
                        {...chart}
                        format={formatCount}
                        unit="count"
                        reference={
                            database?.max_connections != null
                                ? {
                                      value: database.max_connections,
                                      label: `Max ${formatCount(database.max_connections)}`,
                                  }
                                : undefined
                        }
                        lines={[{ label: 'Connections', points: db.connections, curve: 'step' }]}
                    />
                </ChartCell>
                {measured(db.commits, db.rollbacks) && (
                    <ChartCell title={words.transactions} hint="Per second.">
                        <MetricChart
                            {...chart}
                            format={formatPerSecond}
                            unit="number"
                            lines={[
                                { label: words.commits, points: db.commits },
                                { label: 'Rollbacks', points: db.rollbacks },
                            ]}
                        />
                    </ChartCell>
                )}
                {measured(db.cache_hit) && (
                    <ChartCell title="Cache hits" hint={words.cacheHits}>
                        <MetricChart
                            {...chart}
                            format={formatPercent}
                            unit="percent"
                            max={100}
                            lines={[{ label: 'Cache hits', points: db.cache_hit }]}
                        />
                    </ChartCell>
                )}
                <ChartCell title="Size" hint={words.size}>
                    <MetricChart
                        {...chart}
                        format={formatBytes}
                        unit="bytes"
                        lines={[{ label: 'Size', points: db.size }]}
                    />
                </ChartCell>
                {measured(db.longest_transaction) && (
                    <ChartCell
                        title="Longest open transaction"
                        hint="An open transaction holds its locks, and old rows until it ends."
                    >
                        <MetricChart
                            {...chart}
                            format={formatSeconds}
                            unit="number"
                            lines={[{ label: 'Longest', points: db.longest_transaction }]}
                        />
                        <MetricChart
                            {...chart}
                            className="mt-3"
                            height="h-16"
                            format={formatCount}
                            unit="count"
                            lines={[{ label: 'Idle in a transaction', points: db.idle_in_transaction, curve: 'step' }]}
                        />
                    </ChartCell>
                )}
                {measured(db.temp_bytes) && (
                    <ChartCell
                        title="Temporary files"
                        hint="Sorts and hashes too big for the memory each query gets, written to disk instead."
                    >
                        <MetricChart
                            {...chart}
                            format={formatRate}
                            unit="rate"
                            lines={[{ label: 'Written', points: db.temp_bytes }]}
                        />
                    </ChartCell>
                )}
                {measured(db.evicted_keys) && (
                    <ChartCell title="Evicted keys" hint="Keys removed to stay under the memory limit. Per second.">
                        <MetricChart
                            {...chart}
                            format={formatPerSecond}
                            unit="number"
                            lines={[{ label: 'Evicted', points: db.evicted_keys }]}
                        />
                    </ChartCell>
                )}
            </ChartGrid>
            <ChartGrid title="Resources" description="Its container, as Docker measures it." className={dim}>
                <ResourceCharts series={series} live={metrics} />
            </ChartGrid>
            {!slowOn && slowPanel}
        </>
    );
}

type Vital = 'commits' | 'cacheHits' | 'cpu' | 'connections' | 'memory' | 'storage';
const allVitals: Vital[] = ['commits', 'cacheHits', 'cpu', 'connections', 'memory', 'storage'];

type VitalsProps = {
    template: TemplateOption;
    series: AppSeries;
    metrics: AppLive | null;
    databaseSeries: DatabaseSeries;
    database: DatabaseLive | null;
};

// DatabaseVitals is a strip of a template database's vitals: throughput,
// cache hits and CPU with sparklines over the chart range, and
// connections, memory and storage with a bar against their limit. show
// picks which, in order.
export function DatabaseVitals({
    template,
    series,
    metrics,
    databaseSeries: db,
    database,
    show = allVitals,
    className,
}: VitalsProps & { show?: Vital[]; className?: string }) {
    if (!database && !metrics) {
        return (
            <div className="flex items-center gap-3 rounded-xl border border-dashed px-4 py-4 text-sm text-muted-foreground sm:px-5">
                <DatabaseIcon className="size-4 shrink-0" />
                No figures yet: Echowa checks the database once a minute while it runs.
            </div>
        );
    }

    const spark = { from: db.from, to: db.to, step: db.step_seconds };
    const words = wording(template.slug);
    const disk = database?.disk ?? null;
    const diskUsed = disk ? diskPercent(disk) : null;
    const connectionsUsed = percentOf(database?.connections, database?.max_connections);
    const memoryUsed = database?.memory_used_bytes ?? metrics?.memory_used_bytes ?? null;
    const hostMemory = database?.host_memory_total_bytes ?? null;
    const memoryShare = percentOf(memoryUsed, hostMemory);

    const items: Record<Vital, ComponentProps<typeof Stats>['items'][number]> = {
        commits: {
            label: words.commits,
            value: database?.commits_per_sec != null ? formatPerSecond(database.commits_per_sec) : '—',
            hint:
                database?.rollbacks_per_sec != null
                    ? `${formatPerSecond(database.rollbacks_per_sec)} rolled back`
                    : 'last minute',
            visual: <Sparkline {...spark} points={db.commits} />,
        },
        cacheHits: {
            label: 'Cache hits',
            value: database?.cache_hit_percent != null ? formatPercent(database.cache_hit_percent) : '—',
            hint: words.cacheHitsOf,
            visual: <Sparkline {...spark} points={db.cache_hit} max={100} />,
        },
        cpu: {
            label: 'CPU',
            value: metrics?.cpu_percent != null ? formatPercent(metrics.cpu_percent) : '—',
            hint: 'of one core',
            visual: <Sparkline {...spark} points={series.cpu} />,
        },
        connections: {
            label: 'Connections',
            value: database?.connections != null ? formatCount(database.connections) : '—',
            hint: database?.max_connections != null ? `of ${formatCount(database.max_connections)} allowed` : undefined,
            visual: connectionsUsed != null && <MeterBar percent={connectionsUsed} />,
        },
        memory: {
            label: 'Memory',
            value: memoryUsed != null ? formatBytes(memoryUsed) : '—',
            hint: hostMemory != null ? `of the server’s ${formatBytes(hostMemory)}` : undefined,
            visual: memoryShare != null && <MeterBar percent={memoryShare} />,
        },
        storage: {
            label: 'Storage',
            value: database?.size_bytes != null ? formatBytes(database.size_bytes) : '—',
            hint: disk ? `${formatBytes(disk.available_bytes)} free on disk` : 'on disk',
            visual: diskUsed != null && <MeterBar percent={diskUsed} />,
        },
    };

    return <Stats className={className} items={show.map((key) => items[key])} />;
}

// DatabaseGlance is the Overview's look at a template database: what it
// uses of the server and how close it is to its limits, and a link to its
// Metrics section. Its warnings sit above it, in full.
export function DatabaseGlance({
    app,
    onViewMetrics,
    ...props
}: VitalsProps & { app: App; onViewMetrics: () => void }) {
    usePoll(30_000, { only: ['metrics', 'series', 'database', 'databaseSeries'] });
    const href = `/apps/${app.id}?tab=metrics`;
    // Switch sections in place, as the section nav does; a modified click opens a tab.
    const view = (event: MouseEvent) => {
        if (!event.metaKey && !event.ctrlKey && !event.shiftKey) {
            event.preventDefault();
            onViewMetrics();
        }
    };

    return (
        <section aria-label={props.template.name}>
            <div className="mb-2 flex items-center justify-between gap-3">
                <h2 className="min-w-0 text-sm font-medium">{props.template.name}</h2>
                <Link
                    href={href}
                    onClick={view}
                    className="flex shrink-0 items-center gap-0.5 text-sm text-muted-foreground hover:text-foreground"
                >
                    View metrics
                    <ChevronRightIcon className="size-4" />
                </Link>
            </div>
            <DatabaseVitals {...props} show={['cpu', 'memory', 'connections', 'storage']} />
        </section>
    );
}
