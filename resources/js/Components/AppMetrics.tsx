import { usePoll } from '@inertiajs/react';
import { ActivityIcon } from 'lucide-react';

import { MetricChart } from '@/Components/LazyMetricChart';
import { ChartCell, ChartGrid, Stats } from '@/Components/Panel';
import { RangePicker, useSeriesRange } from '@/Components/RangePicker';
import { formatBytes, formatPercent, formatRate } from '@/lib/format';
import { cn } from '@/lib/utils';
import type { AppLive, AppSeries } from '@/types/metrics';

// Task counts are whole, but a bucket's average isn't during a redeploy.
const formatTasks = (value: number) => (Number.isInteger(value) ? String(value) : value.toFixed(1));

// An app's Metrics section: its newest reading, live, and charts of its
// tasks summed, for the range in ?range=. A template database has
// DatabaseMetrics instead, which includes these charts.
export function AppMetrics({ ranges, series, live }: { ranges: string[]; series: AppSeries; live: AppLive | null }) {
    // Echowa reads every 15 seconds and stores a minute at a time.
    usePoll(15_000, { only: ['metrics', 'series'] });
    const { range, loading, pick } = useSeriesRange(series);

    return (
        <>
            {live ? (
                <Stats
                    items={[
                        {
                            label: 'CPU',
                            value: live.cpu_percent != null ? formatPercent(live.cpu_percent) : '—',
                            hint: 'of one core',
                        },
                        {
                            label: 'Memory',
                            value: formatBytes(live.memory_used_bytes),
                            hint: live.memory_limited ? `of ${formatBytes(live.memory_limit_bytes)} limit` : 'no limit',
                        },
                        { label: 'Tasks', value: live.tasks, hint: 'running' },
                        {
                            label: 'Network',
                            value: live.net_rx_bytes_per_sec != null ? formatRate(live.net_rx_bytes_per_sec) : '—',
                            hint:
                                live.net_tx_bytes_per_sec != null &&
                                `in · ${formatRate(live.net_tx_bytes_per_sec)} out`,
                        },
                    ]}
                />
            ) : (
                <NoLiveFigures />
            )}
            <section className="space-y-4" aria-label="Charts">
                <RangePicker ranges={ranges} range={range} step={series.step_seconds} onPick={pick} />
                <ChartGrid
                    title="Resources"
                    description="Its tasks summed, as Docker measures them."
                    className={cn('transition-opacity', loading && 'opacity-60')}
                >
                    <ResourceCharts series={series} live={live} />
                </ChartGrid>
            </section>
        </>
    );
}

export function NoLiveFigures() {
    return (
        <div className="flex items-center gap-3 rounded-xl border border-dashed px-4 py-4 text-sm text-muted-foreground sm:px-5">
            <ActivityIcon className="size-4 shrink-0" />
            No live figures: none of its tasks is running. They appear within 15 seconds of the app running.
        </div>
    );
}

// ResourceCharts is an app's CPU, memory, network and disk I/O, as cells
// for a ChartGrid.
export function ResourceCharts({ series, live }: { series: AppSeries; live: AppLive | null }) {
    const chart = { from: series.from, to: series.to, step: series.step_seconds, height: 'h-36' };

    return (
        <>
            <ChartCell title="CPU" hint="A percentage of one core: 200% is two whole cores.">
                <MetricChart
                    {...chart}
                    format={formatPercent}
                    unit="percent"
                    lines={[{ label: 'CPU', points: series.cpu }]}
                />
                <MetricChart
                    {...chart}
                    className="mt-3"
                    height="h-16"
                    format={formatTasks}
                    unit="count"
                    lines={[{ label: 'Tasks', points: series.tasks, curve: 'step' }]}
                />
            </ChartCell>
            <ChartCell
                title="Memory"
                hint={live && !live.memory_limited ? 'No limit set: it can use all of the server’s memory.' : undefined}
            >
                <MetricChart
                    {...chart}
                    format={formatBytes}
                    unit="bytes"
                    reference={
                        live?.memory_limited
                            ? { value: live.memory_limit_bytes, label: `Limit ${formatBytes(live.memory_limit_bytes)}` }
                            : undefined
                    }
                    lines={[{ label: 'Used', points: series.memory }]}
                />
            </ChartCell>
            <ChartCell title="Network">
                <MetricChart
                    {...chart}
                    format={formatRate}
                    unit="rate"
                    lines={[
                        { label: 'Received', points: series.network.rx },
                        { label: 'Sent', points: series.network.tx },
                    ]}
                />
            </ChartCell>
            <ChartCell title="Disk I/O" hint="Reads and writes by its containers.">
                <MetricChart
                    {...chart}
                    format={formatRate}
                    unit="rate"
                    lines={[
                        { label: 'Read', points: series.block.read },
                        { label: 'Write', points: series.block.write },
                    ]}
                />
            </ChartCell>
        </>
    );
}
