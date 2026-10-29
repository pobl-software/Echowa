import { Head, Link, usePoll } from '@inertiajs/react';
import { ActivityIcon } from 'lucide-react';
import { useState } from 'react';
import { MetricChart } from '@/Components/LazyMetricChart';
import { Meter } from '@/Components/Meter';
import type { ChartLine } from '@/Components/MetricChart';
import { NativeSelect } from '@/Components/NativeSelect';
import { Page } from '@/Components/Page';
import { Panel, Stats } from '@/Components/Panel';
import { RangePicker, useSeriesRange } from '@/Components/RangePicker';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/Components/ui/table';
import { formatBytes, formatPercent, formatRate } from '@/lib/format';
import { cn } from '@/lib/utils';
import { diskPercent, memoryPercent, type Point, type Reading, type ServerSeries } from '@/types/metrics';

interface Props {
    // The range picker's choices, shortest first.
    ranges: string[];
    series: ServerSeries;
    // The newest reading, live. Null until Echowa's first, 15 seconds after it starts.
    latest: Reading | null;
    // App IDs by name, to link the containers table.
    appIds: Record<string, number>;
}

export default function Server({ ranges, series, latest, appIds }: Props) {
    // Echowa reads every 15 seconds and stores a minute at a time.
    usePoll(15_000, { only: ['latest', 'series'] });

    return (
        <>
            <Head title="Server" />
            <Page crumbs={[{ label: 'Server' }]}>
                <div className="space-y-6">
                    {latest ? <Now latest={latest} /> : <Waiting />}
                    {latest && latest.disks.length > 0 && <Disks latest={latest} />}
                    <Charts series={series} ranges={ranges} cpuCount={latest?.host.cpu_count ?? null} />
                    {latest && <Containers latest={latest} appIds={appIds} />}
                </div>
            </Page>
        </>
    );
}

function Waiting() {
    return (
        <div className="flex flex-col items-center rounded-xl border border-dashed px-6 py-10 text-center">
            <span className="mb-4 flex size-11 items-center justify-center rounded-xl border bg-card">
                <ActivityIcon className="size-5 text-muted-foreground" />
            </span>
            <h2 className="font-medium">Reading the server</h2>
            <p className="mt-1 max-w-md text-sm text-muted-foreground">
                Echowa reads CPU, memory, disks and containers every 15 seconds. The first figures appear here shortly
                after it starts.
            </p>
        </div>
    );
}

const percentOrDash = (value: number | null) => (value != null ? formatPercent(value) : '—');

// The headline figures, from the newest reading.
function Now({ latest }: { latest: Reading }) {
    const host = latest.host;
    const memory = memoryPercent(host);

    return (
        <Stats
            items={[
                {
                    label: 'CPU',
                    value: percentOrDash(host.cpu_percent),
                    hint: host.cpu_count != null && `${host.cpu_count} ${host.cpu_count === 1 ? 'core' : 'cores'}`,
                },
                {
                    label: 'Memory',
                    value: percentOrDash(memory),
                    hint:
                        host.memory_used_bytes != null &&
                        host.memory_total_bytes != null &&
                        `${formatBytes(host.memory_used_bytes)} of ${formatBytes(host.memory_total_bytes)}`,
                },
                {
                    label: 'Load',
                    value: host.load_1m != null ? host.load_1m.toFixed(2) : '—',
                    hint:
                        host.load_5m != null &&
                        host.load_15m != null &&
                        `${host.load_5m.toFixed(2)} · ${host.load_15m.toFixed(2)} (5, 15 min)`,
                },
                {
                    label: 'Containers',
                    value: host.containers ?? '—',
                    hint:
                        host.containers == null
                            ? 'Docker didn’t answer'
                            : `${latest.apps.length} ${latest.apps.length === 1 ? 'app' : 'apps'}`,
                },
            ]}
        />
    );
}

function Disks({ latest }: { latest: Reading }) {
    return (
        <Panel title="Disks">
            <ul className="grid gap-x-8 gap-y-4 sm:grid-cols-2 xl:grid-cols-3">
                {latest.disks.map((disk) => (
                    <li key={disk.mount} className="min-w-0">
                        <div className="mb-1.5 flex items-baseline justify-between gap-3">
                            <span className="truncate font-mono text-sm">{disk.mount}</span>
                            <span className="shrink-0 text-xs text-muted-foreground">{disk.filesystem}</span>
                        </div>
                        <Meter
                            percent={diskPercent(disk)}
                            detail={`${formatBytes(disk.used_bytes)} of ${formatBytes(disk.used_bytes + disk.available_bytes)}`}
                        />
                    </li>
                ))}
            </ul>
        </Panel>
    );
}

// The range picker and every chart. Changing the range reloads only the
// series, and the charts keep their last render, dimmed, until it arrives.
function Charts({ series, ranges, cpuCount }: { series: ServerSeries; ranges: string[]; cpuCount: number | null }) {
    const { range, loading, pick } = useSeriesRange(series);

    const chart = { from: series.from, to: series.to, step: series.step_seconds };
    const percent = { format: formatPercent, unit: 'percent', max: 100 } as const;
    const lines = (entries: [string, Point[]][]): ChartLine[] => entries.map(([label, points]) => ({ label, points }));

    return (
        <section className="space-y-4" aria-label="Charts">
            <RangePicker ranges={ranges} range={range} step={series.step_seconds} onPick={pick} />
            <div className={cn('grid gap-4 transition-opacity xl:grid-cols-2', loading && 'opacity-60')}>
                <Panel title="CPU">
                    <MetricChart
                        {...chart}
                        {...percent}
                        lines={lines([
                            ['Usage', series.cpu.usage],
                            ['I/O wait', series.cpu.iowait],
                            ['Steal', series.cpu.steal],
                        ])}
                    />
                </Panel>
                <Panel title="Load average">
                    <MetricChart
                        {...chart}
                        format={(value) => value.toFixed(2)}
                        unit="number"
                        reference={
                            cpuCount
                                ? { value: cpuCount, label: `${cpuCount} ${cpuCount === 1 ? 'core' : 'cores'}` }
                                : undefined
                        }
                        lines={lines([
                            ['1 min', series.load.load_1m],
                            ['5 min', series.load.load_5m],
                            ['15 min', series.load.load_15m],
                        ])}
                    />
                </Panel>
                <Panel title="Memory">
                    <MetricChart
                        {...chart}
                        {...percent}
                        lines={lines([
                            ['Memory', series.memory],
                            ['Swap', series.swap],
                        ])}
                    />
                </Panel>
                <Panel title="Disk usage">
                    <MetricChart
                        {...chart}
                        {...percent}
                        lines={series.disks.map((disk) => ({ label: disk.mount, points: disk.percent }))}
                    />
                </Panel>
                <PerEntityPanel
                    title="Disk I/O"
                    noun="device"
                    entities={series.disk_io.map((disk) => ({
                        name: disk.device,
                        lines: lines([
                            ['Read', disk.read],
                            ['Write', disk.write],
                        ]),
                    }))}
                    chart={chart}
                />
                <PerEntityPanel
                    title="Network"
                    noun="interface"
                    entities={series.network.map((nic) => ({
                        name: nic.interface,
                        lines: lines([
                            ['Received', nic.rx],
                            ['Sent', nic.tx],
                        ]),
                    }))}
                    chart={chart}
                />
            </div>
        </section>
    );
}

// A rate chart for one device or interface at a time, with a picker when
// there's more than one. Starts on the busiest.
function PerEntityPanel({
    title,
    noun,
    entities,
    chart,
}: {
    title: string;
    noun: string;
    entities: { name: string; lines: ChartLine[] }[];
    chart: { from: number; to: number; step: number };
}) {
    const [chosen, setChosen] = useState<string | null>(null);
    const busiest = [...entities].sort((a, b) => traffic(b.lines) - traffic(a.lines))[0];
    const current = entities.find((entity) => entity.name === chosen) ?? busiest;

    return (
        <Panel
            title={title}
            actions={
                entities.length > 1 && (
                    <NativeSelect
                        value={current.name}
                        onChange={(event) => setChosen(event.target.value)}
                        aria-label={`Choose a ${noun}`}
                        className="h-7 w-auto font-mono text-xs md:text-xs"
                    >
                        {entities.map((entity) => (
                            <option key={entity.name} value={entity.name}>
                                {entity.name}
                            </option>
                        ))}
                    </NativeSelect>
                )
            }
            description={entities.length === 1 && <span className="font-mono text-xs">{current.name}</span>}
        >
            <MetricChart {...chart} format={formatRate} unit="rate" lines={current?.lines ?? []} />
        </Panel>
    );
}

// What a container that isn't an app's own task is.
function containerNote(container: Reading['other_containers'][number]): string {
    if (!container.app) {
        return 'Not managed by Echowa';
    }
    return container.role === 'pooler' ? `Connection pooler for ${container.app}` : `Runs beside ${container.app}`;
}

function traffic(lines: ChartLine[]): number {
    return lines.reduce((sum, line) => sum + line.points.reduce((total, point) => total + (point.avg ?? 0), 0), 0);
}

// Every running container: Echowa's apps, summed over their tasks and
// linked, then the rest: apps' poolers, linked to their app, and Traefik
// and anything else on the server.
function Containers({ latest, appIds }: { latest: Reading; appIds: Record<string, number> }) {
    const rows = [
        ...latest.apps.map((app) => ({
            key: `app:${app.app}`,
            name: app.app,
            href: appIds[app.app] ? `/apps/${appIds[app.app]}` : undefined,
            note: undefined,
            tasks: app.tasks,
            cpu: app.cpu_percent,
            used: app.memory_used_bytes,
            limit: app.memory_limit_bytes,
        })),
        ...latest.other_containers.map((container) => ({
            key: `other:${container.name}`,
            name: container.name,
            href: appIds[container.app] ? `/apps/${appIds[container.app]}` : undefined,
            note: containerNote(container),
            tasks: null,
            cpu: container.cpu_percent,
            used: container.memory_used_bytes,
            limit: container.memory_limit_bytes,
        })),
    ];

    return (
        <Panel title="Containers" description="CPU is a percentage of one core: 200% is two whole cores." flush>
            {rows.length === 0 ? (
                <p className="px-4 py-6 text-center text-sm text-muted-foreground sm:px-5">
                    No containers are running.
                </p>
            ) : (
                <Table>
                    <TableHeader>
                        <TableRow>
                            <TableHead>Name</TableHead>
                            <TableHead className="hidden sm:table-cell">Tasks</TableHead>
                            <TableHead className="text-right">CPU</TableHead>
                            <TableHead className="text-right">Memory</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {rows.map((row) => (
                            <TableRow key={row.key}>
                                <TableCell className="max-w-0 w-full">
                                    {row.href ? (
                                        <Link href={row.href} className="block truncate font-medium hover:underline">
                                            {row.name}
                                        </Link>
                                    ) : (
                                        <span className="block truncate font-mono text-sm">{row.name}</span>
                                    )}
                                    {row.note && (
                                        <span className="block text-xs text-muted-foreground">{row.note}</span>
                                    )}
                                </TableCell>
                                <TableCell className="hidden tabular-nums sm:table-cell">{row.tasks ?? '—'}</TableCell>
                                <TableCell className="text-right tabular-nums">{percentOrDash(row.cpu)}</TableCell>
                                <TableCell className="text-right whitespace-nowrap tabular-nums">
                                    {formatBytes(row.used)}
                                    <span className="hidden text-muted-foreground md:inline">
                                        {' '}
                                        of {formatBytes(row.limit)}
                                    </span>
                                </TableCell>
                            </TableRow>
                        ))}
                    </TableBody>
                </Table>
            )}
        </Panel>
    );
}
