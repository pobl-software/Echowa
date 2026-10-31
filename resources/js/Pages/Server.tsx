import { Head, Link, usePoll } from '@inertiajs/react';
import { ActivityIcon, ServerIcon } from 'lucide-react';
import { useState } from 'react';
import { PercentTile, Tile, usedOf } from '@/Components/FigureTile';
import { MetricChart } from '@/Components/LazyMetricChart';
import { MeterBar } from '@/Components/Meter';
import type { ChartLine } from '@/Components/MetricChart';
import { NativeSelect } from '@/Components/NativeSelect';
import { Page } from '@/Components/Page';
import { Panel } from '@/Components/Panel';
import { RangePicker, useSeriesRange } from '@/Components/RangePicker';
import { SectionLabel } from '@/Components/SectionLabel';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/Components/ui/table';
import { formatBytes, formatPercent, formatRate } from '@/lib/format';
import { uptime } from '@/lib/time';
import { cn } from '@/lib/utils';
import { diskPercent, memoryPercent, type Point, type Reading, type ServerSeries } from '@/types/metrics';
import type { ServerFacts } from '@/types/models';

interface Props {
    // The range picker's choices, shortest first.
    ranges: string[];
    series: ServerSeries;
    // The newest reading, live. Null until Echowa's first, 15 seconds after it starts.
    latest: Reading | null;
    server: ServerFacts;
    // App IDs by name, to link the containers table.
    appIds: Record<string, number>;
}

export default function Server({ ranges, series, latest, server, appIds }: Props) {
    // Echowa reads every 15 seconds and stores a minute at a time.
    usePoll(15_000, { only: ['latest', 'series'] });

    return (
        <>
            <Head title="Server" />
            <Page crumbs={[{ label: 'Server' }]} className="xl:max-w-[100rem]">
                {/* From xl the server's figures and charts are on the left, its containers on the right. */}
                <div className="space-y-6 xl:grid xl:grid-cols-[minmax(0,1fr)_26rem] xl:items-start xl:gap-6 xl:space-y-0">
                    <div className="min-w-0 space-y-6">
                        <Now latest={latest} facts={server} />
                        <Charts series={series} ranges={ranges} cpuCount={latest?.host.cpu_count ?? null} />
                    </div>
                    {latest && <Containers latest={latest} appIds={appIds} />}
                </div>
            </Page>
        </>
    );
}

// The server and its newest figures, in one panel: which machine it is,
// then CPU, memory, load and each disk as tiles.
function Now({ latest, facts }: { latest: Reading | null; facts: ServerFacts }) {
    const about = [facts.os, facts.kernel, facts.booted_at && `up ${uptime(facts.booted_at)}`]
        .filter(Boolean)
        .join(' · ');

    return (
        <section aria-label="Now" className="overflow-hidden rounded-xl border bg-card">
            <div className="flex items-center gap-3 border-b px-4 py-3.5 sm:px-5">
                <span className="flex size-9 shrink-0 items-center justify-center rounded-lg border bg-muted/50">
                    <ServerIcon className="size-4 text-muted-foreground" />
                </span>
                <div className="min-w-0 flex-1">
                    <p className="truncate font-mono text-sm font-medium">{facts.hostname || 'This server'}</p>
                    {about && <p className="truncate text-xs text-muted-foreground">{about}</p>}
                </div>
                {latest && (
                    <span
                        className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground"
                        title="Read every 15 seconds"
                    >
                        <span className="size-1.5 rounded-full bg-success" />
                        Live
                    </span>
                )}
            </div>
            {latest ? <Figures latest={latest} /> : <Waiting />}
        </section>
    );
}

// Each tile draws its own right and bottom hairline, and the grid hangs
// one pixel past the panel's edge, so a short last row leaves no stray
// lines.
function Figures({ latest }: { latest: Reading }) {
    const host = latest.host;
    const cores = host.cpu_count;
    const cells = [
        <PercentTile
            key="cpu"
            label="CPU"
            percent={host.cpu_percent}
            detail={cores != null ? `${cores} ${cores === 1 ? 'core' : 'cores'}` : undefined}
        />,
        <PercentTile
            key="memory"
            label="Memory"
            percent={memoryPercent(host)}
            detail={
                host.memory_used_bytes != null && host.memory_total_bytes != null
                    ? usedOf(host.memory_used_bytes, host.memory_total_bytes)
                    : undefined
            }
        />,
        <Tile
            key="load"
            label="Load"
            value={host.load_1m != null ? host.load_1m.toFixed(2) : <span className="text-muted-foreground">—</span>}
            // The 1 minute load against the cores: a full bar is every core busy.
            bar={
                host.load_1m != null &&
                cores != null &&
                cores > 0 && <MeterBar percent={(100 * host.load_1m) / cores} className="h-1" />
            }
            detail={
                host.load_5m != null && host.load_15m != null
                    ? `${host.load_5m.toFixed(2)} · ${host.load_15m.toFixed(2)} (5, 15 min)`
                    : undefined
            }
        />,
        ...latest.disks.map((disk) => (
            <PercentTile
                key={`disk:${disk.mount}`}
                label={
                    <>
                        Disk <span className="font-mono">{disk.mount}</span>
                    </>
                }
                percent={diskPercent(disk)}
                detail={`${usedOf(disk.used_bytes, disk.used_bytes + disk.available_bytes)} · ${disk.filesystem}`}
            />
        )),
    ];

    return (
        <div className="-mr-px -mb-px grid grid-cols-2 md:grid-cols-4">
            {cells.map((cell) => (
                <div key={cell.key} className="min-w-0 border-r border-b px-4 py-4 sm:px-5">
                    {cell}
                </div>
            ))}
        </div>
    );
}

function Waiting() {
    return (
        <div className="flex flex-col items-center px-6 py-10 text-center">
            <ActivityIcon className="mb-3 size-5 text-muted-foreground" />
            <h2 className="font-medium">Reading the server</h2>
            <p className="mt-1 max-w-md text-sm text-muted-foreground">
                Echowa reads CPU, memory, disks and containers every 15 seconds. The first figures appear here shortly
                after it starts.
            </p>
        </div>
    );
}

const percentOrDash = (value: number | null) => (value != null ? formatPercent(value) : '—');

// The range picker and every chart. Changing the range reloads only the
// series, and the charts keep their last render, dimmed, until it arrives.
function Charts({ series, ranges, cpuCount }: { series: ServerSeries; ranges: string[]; cpuCount: number | null }) {
    const { range, loading, pick } = useSeriesRange(series);

    const chart = { from: series.from, to: series.to, step: series.step_seconds };
    const percent = { format: formatPercent, unit: 'percent', max: 100 } as const;
    const lines = (entries: [string, Point[]][]): ChartLine[] => entries.map(([label, points]) => ({ label, points }));

    return (
        <section className="@container space-y-4" aria-label="Charts">
            <RangePicker ranges={ranges} range={range} step={series.step_seconds} onPick={pick} />
            <div className={cn('grid gap-4 transition-opacity @3xl:grid-cols-2', loading && 'opacity-60')}>
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

// What a container that runs beside an app is.
function containerNote(container: Reading['other_containers'][number]): string {
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
            managed: true,
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
            managed: container.app !== '',
            key: `other:${container.name}`,
            name: container.name,
            href: appIds[container.app] ? `/apps/${appIds[container.app]}` : undefined,
            note: container.app ? containerNote(container) : undefined,
            tasks: null,
            cpu: container.cpu_percent,
            used: container.memory_used_bytes,
            limit: container.memory_limit_bytes,
        })),
    ];
    const managed = rows.filter((row) => row.managed);
    const unmanaged = rows.filter((row) => !row.managed);

    return (
        <Panel
            title={
                <>
                    Containers
                    {latest.host.containers != null && (
                        <span className="ml-2 font-normal text-muted-foreground tabular-nums">
                            {latest.host.containers}
                        </span>
                    )}
                </>
            }
            description="CPU is a percentage of one core: 200% is two whole cores."
            className="@container"
            flush
        >
            {rows.length === 0 ? (
                <p className="px-4 py-6 text-center text-sm text-muted-foreground sm:px-5">
                    No containers are running.
                </p>
            ) : (
                <Table>
                    <TableHeader>
                        <TableRow>
                            <TableHead>Name</TableHead>
                            <TableHead className="hidden @md:table-cell">Tasks</TableHead>
                            <TableHead className="text-right">CPU</TableHead>
                            <TableHead className="text-right">Memory</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {managed.map((row) => (
                            <ContainerRow key={row.key} row={row} />
                        ))}
                        {unmanaged.length > 0 && (
                            <>
                                <TableRow className="hover:bg-transparent">
                                    <TableCell colSpan={4} className="bg-muted/30 py-2">
                                        <SectionLabel>Not managed by Echowa</SectionLabel>
                                    </TableCell>
                                </TableRow>
                                {unmanaged.map((row) => (
                                    <ContainerRow key={row.key} row={row} faded />
                                ))}
                            </>
                        )}
                    </TableBody>
                </Table>
            )}
        </Panel>
    );
}

interface ContainerRowData {
    key: string;
    name: string;
    href: string | undefined;
    note: string | undefined;
    tasks: number | null;
    cpu: number | null;
    used: number;
    limit: number;
}

// faded quiets a container Echowa doesn't manage until it's hovered.
function ContainerRow({ row, faded }: { row: ContainerRowData; faded?: boolean }) {
    return (
        <TableRow className={cn(faded && 'opacity-60 transition-opacity hover:opacity-100')}>
            <TableCell className="max-w-0 w-full">
                {row.href ? (
                    <Link href={row.href} className="block truncate font-medium hover:underline">
                        {row.name}
                    </Link>
                ) : (
                    <span className="block truncate font-mono text-sm">{row.name}</span>
                )}
                {row.note && <span className="block text-xs text-muted-foreground">{row.note}</span>}
            </TableCell>
            <TableCell className="hidden tabular-nums @md:table-cell">{row.tasks ?? '—'}</TableCell>
            <TableCell className="text-right tabular-nums">{percentOrDash(row.cpu)}</TableCell>
            <TableCell className="text-right whitespace-nowrap tabular-nums">
                {formatBytes(row.used)}
                <span className="hidden text-muted-foreground @lg:inline"> of {formatBytes(row.limit)}</span>
            </TableCell>
        </TableRow>
    );
}
