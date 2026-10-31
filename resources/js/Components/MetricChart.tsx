import { router } from '@inertiajs/react';
import { useMemo } from 'react';
import { CartesianGrid, Line, LineChart, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';

import { type AxisUnit, axisTicks, formatStep } from '@/lib/format';
import { cn } from '@/lib/utils';
import type { Point } from '@/types/metrics';

// A time-series line chart of metrics (metrics.Query). Each line plots its
// buckets' averages. The first line is the accent colour, the others
// neutral and dashed; a chart with one line also draws its peaks, faintly.
// The legend names each line with its newest value, so colour is never the
// only key, and the tooltip adds the peak when it differs. A bucket with
// no neighbours is drawn as a dot, since a line needs two.

export interface ChartLine {
    label: string;
    points: Point[];
    // Steps for counts, such as running tasks; slopes otherwise.
    curve?: 'step';
}

// A moment to mark, such as a deploy, in Unix seconds. The tooltip lists
// a bucket's own markers. Markers too close to tell apart at the chart's
// range share one flag, at the first, which leads to the newest's page.
export interface ChartMarker {
    at: number;
    label: string;
    href: string;
}

interface Props {
    lines: ChartLine[];
    // The range shown, in Unix seconds, and the bucket size.
    from: number;
    to: number;
    step: number;
    // Values in the legend and tooltip.
    format: (value: number) => string;
    // What the y axis counts, for round ticks in its units.
    unit: AxisUnit;
    // A fixed top for the y axis, e.g. 100 for percentages. Defaults to
    // the data's max.
    max?: number;
    reference?: { value: number; label: string };
    markers?: ChartMarker[];
    // The plot's height class.
    height?: string;
    className?: string;
}

type Row = { t: number } & Record<string, number | null>;

const styles = [
    { stroke: 'var(--primary)', dash: undefined },
    { stroke: 'var(--foreground)', dash: '5 4' },
    { stroke: 'var(--muted-foreground)', dash: '2 3' },
];
const styleOf = (index: number) => styles[index % styles.length];
const PEAK = 'var(--muted-foreground)';
const curveOf = (line: ChartLine) => (line.curve === 'step' ? 'stepAfter' : 'linear');

// Line keys are positional (s0, s1…) rather than labels, which can be any
// mount or device name.
const key = (index: number) => `s${index}`;

export function MetricChart({
    lines,
    from,
    to,
    step,
    format,
    unit,
    max,
    reference,
    markers,
    height = 'h-48',
    className,
}: Props) {
    const visible = lines.filter((line) => line.points.length > 0);
    const rows = useMemo(() => mergeRows(lines), [lines]);
    const placed = useMemo(() => placeMarkers(markers ?? [], from, to, step), [markers, from, to, step]);
    const ticks = useMemo(() => timeTicks(from, to), [from, to]);
    const peaks = lines.length === 1;
    // The keys drawn: each line's averages, and the first line's peaks.
    const plotted = lines
        .flatMap((line, i) => (line.points.length > 0 ? [key(i)] : []))
        .concat(peaks ? [`${key(0)}_max`] : []);
    const yTicks = axisTicks(max ?? Math.max(highest(rows, plotted), reference?.value ?? 0), unit);

    if (visible.length === 0) {
        return (
            <div className={cn('flex items-center justify-center text-sm text-muted-foreground', height, className)}>
                No data in this range yet.
            </div>
        );
    }

    return (
        <div className={className}>
            <Legend lines={lines} format={format} peaks={peaks} />
            <div className={cn('w-full text-xs', height)}>
                <ResponsiveContainer width="100%" height="100%">
                    <LineChart data={rows} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
                        <CartesianGrid vertical={false} stroke="var(--border)" />
                        <XAxis
                            dataKey="t"
                            type="number"
                            scale="time"
                            domain={[from * 1000, to * 1000]}
                            ticks={ticks.values}
                            tickFormatter={ticks.format}
                            tick={{ fill: 'var(--muted-foreground)' }}
                            tickLine={false}
                            axisLine={false}
                            tickMargin={8}
                            minTickGap={16}
                        />
                        <YAxis
                            domain={[0, yTicks.values[yTicks.values.length - 1]]}
                            ticks={yTicks.values}
                            interval={0}
                            // Plain text: Recharts' own wraps a label it guesses is too wide.
                            tick={({ x, y, payload }) => (
                                <text x={x} y={y} dy={4} textAnchor="end" fill="var(--muted-foreground)">
                                    {yTicks.label(payload.value)}
                                </text>
                            )}
                            tickLine={false}
                            axisLine={false}
                            width={72}
                        />
                        {reference && (
                            <ReferenceLine
                                y={reference.value}
                                ifOverflow="extendDomain"
                                stroke="var(--muted-foreground)"
                                strokeDasharray="4 4"
                                label={{
                                    value: reference.label,
                                    position: 'insideTopRight',
                                    fill: 'var(--muted-foreground)',
                                    fontSize: 11,
                                }}
                            />
                        )}
                        {placed.flags.map((flag) => (
                            <ReferenceLine
                                key={flag.t}
                                x={flag.t}
                                stroke="var(--muted-foreground)"
                                strokeOpacity={0.45}
                                label={<Flag flag={flag} />}
                            />
                        ))}
                        <Tooltip
                            cursor={{ stroke: 'var(--muted-foreground)', strokeWidth: 1 }}
                            isAnimationActive={false}
                            content={({ active, payload }) =>
                                active && payload?.[0] ? (
                                    <ChartTooltip
                                        row={payload[0].payload as Row}
                                        lines={lines}
                                        step={step}
                                        format={format}
                                        marked={placed.byBucket.get((payload[0].payload as Row).t)}
                                    />
                                ) : null
                            }
                        />
                        {peaks && visible.length > 0 && (
                            <Line
                                dataKey={`${key(0)}_max`}
                                type={curveOf(lines[0])}
                                stroke={PEAK}
                                strokeWidth={1}
                                strokeOpacity={0.6}
                                dot={loneDot(rows, `${key(0)}_max`, PEAK)}
                                activeDot={false}
                                connectNulls={false}
                                isAnimationActive={false}
                            />
                        )}
                        {lines.map((line, i) =>
                            line.points.length > 0 ? (
                                <Line
                                    key={key(i)}
                                    dataKey={key(i)}
                                    name={line.label}
                                    type={curveOf(line)}
                                    stroke={styleOf(i).stroke}
                                    strokeWidth={2}
                                    strokeDasharray={styleOf(i).dash}
                                    dot={loneDot(rows, key(i), styleOf(i).stroke)}
                                    activeDot={{ r: 4, strokeWidth: 2, stroke: 'var(--card)', fill: styleOf(i).stroke }}
                                    connectNulls={false}
                                    isAnimationActive={false}
                                />
                            ) : null,
                        )}
                    </LineChart>
                </ResponsiveContainer>
            </div>
        </div>
    );
}

function highest(rows: Row[], keys: string[]): number {
    let top = 0;
    for (const row of rows) {
        for (const k of keys) {
            const value = row[k];
            if (value != null && value > top) {
                top = value;
            }
        }
    }
    return top;
}

// Draws a dot for a bucket with no value either side, where the line has
// nothing to join it to, and nothing elsewhere.
function loneDot(rows: Row[], dataKey: string, fill: string) {
    return ({ cx, cy, index }: { cx?: number; cy?: number; index?: number }) => {
        const i = index ?? -1;
        const lone = rows[i]?.[dataKey] != null && rows[i - 1]?.[dataKey] == null && rows[i + 1]?.[dataKey] == null;
        return lone ? <circle key={`${dataKey}-${i}`} cx={cx} cy={cy} r={2.5} fill={fill} /> : null;
    };
}

// One row per bucket with every line's average (s0) and peak (s0_max).
function mergeRows(lines: ChartLine[]): Row[] {
    const byT = new Map<number, Row>();
    lines.forEach((line, i) => {
        for (const point of line.points) {
            const t = point.t * 1000;
            let row = byT.get(t);
            if (!row) {
                row = { t } as Row;
                byT.set(t, row);
            }
            row[key(i)] = point.avg;
            row[`${key(i)}_max`] = point.max;
        }
    });

    return [...byT.values()].sort((a, b) => a.t - b.t);
}

function newest(points: Point[]): number | null {
    for (let i = points.length - 1; i >= 0; i--) {
        if (points[i].avg !== null) {
            return points[i].avg;
        }
    }
    return null;
}

function Swatch({ stroke, dash }: { stroke: string; dash?: string }) {
    return (
        <svg width="14" height="2" className="shrink-0" aria-hidden="true">
            <line x1="0" y1="1" x2="14" y2="1" stroke={stroke} strokeWidth="2" strokeDasharray={dash} />
        </svg>
    );
}

// Each line's name and its newest value, and the peaks' key when drawn.
function Legend({ lines, format, peaks }: { lines: ChartLine[]; format: (value: number) => string; peaks: boolean }) {
    return (
        <ul className="mb-3 flex flex-wrap gap-x-4 gap-y-1 text-xs">
            {lines.map((line, i) => {
                if (line.points.length === 0) {
                    return null;
                }
                const value = newest(line.points);
                return (
                    <li key={key(i)} className="flex items-center gap-1.5">
                        <Swatch {...styleOf(i)} />
                        <span className="text-muted-foreground">{line.label}</span>
                        <span className="font-medium tabular-nums">{value !== null ? format(value) : '—'}</span>
                    </li>
                );
            })}
            {peaks && (
                <li className="flex items-center gap-1.5 text-muted-foreground">
                    <Swatch stroke={PEAK} />
                    Peak
                </li>
            )}
        </ul>
    );
}

interface MarkerFlag {
    // The bucket's start, in milliseconds, as rows have it.
    t: number;
    labels: string[];
    href: string;
}

// Flags closer than a 24th of the range would overlap on a narrow chart,
// so a marker within that of a flag's first joins it. Measuring from the
// first, not the one before, keeps a run of deploys from becoming one flag.
function placeMarkers(markers: ChartMarker[], from: number, to: number, step: number) {
    const gap = (to - from) / 24;
    const byBucket = new Map<number, string[]>();
    const flags: MarkerFlag[] = [];
    let first = Number.NEGATIVE_INFINITY;
    for (const marker of [...markers].sort((a, b) => a.at - b.at)) {
        const t = Math.floor(marker.at / step) * step * 1000;
        byBucket.set(t, [...(byBucket.get(t) ?? []), marker.label]);
        const flag = flags.at(-1);
        if (flag && marker.at - first < gap) {
            flag.labels.push(marker.label);
            flag.href = marker.href;
        } else {
            flags.push({ t, labels: [marker.label], href: marker.href });
            first = marker.at;
        }
    }
    return { byBucket, flags };
}

// A small pennant above the plot, linking to the marker's page. Recharts
// passes the line's viewBox to its label. It's out of the tab order: every
// chart repeats it, and the Deployments section lists the same pages.
function Flag({ flag, viewBox }: { flag: MarkerFlag; viewBox?: { x?: number; y?: number } }) {
    if (viewBox?.x == null || viewBox.y == null) {
        return null;
    }
    const { x, y } = viewBox;
    return (
        <a
            href={flag.href}
            tabIndex={-1}
            onClick={(e) => {
                if (!e.metaKey && !e.ctrlKey && !e.shiftKey) {
                    e.preventDefault();
                    router.visit(flag.href);
                }
            }}
            className="cursor-pointer"
        >
            <title>
                {flag.labels.length > 1
                    ? `${flag.labels.at(-1)} and ${flag.labels.length - 1} earlier`
                    : flag.labels[0]}
            </title>
            <rect x={x - 12} y={y - 8} width={24} height={24} fill="transparent" />
            <path d={`M${x - 4} ${y - 6}h8l-4 6z`} fill="var(--foreground)" fillOpacity={0.55} />
        </a>
    );
}

function ChartTooltip({
    row,
    lines,
    step,
    format,
    marked,
}: {
    row: Row;
    lines: ChartLine[];
    step: number;
    format: (value: number) => string;
    marked?: string[];
}) {
    return (
        <div className="grid min-w-40 gap-1.5 rounded-lg border bg-popover px-2.5 py-2 text-xs text-popover-foreground shadow-xl">
            <div className="font-medium">{formatBucket(row.t, step)}</div>
            {marked?.map((label) => (
                <div key={label} className="flex items-center gap-2 text-muted-foreground">
                    <svg width="14" height="8" viewBox="0 0 14 8" className="shrink-0" aria-hidden="true">
                        <path d="M3 1h8l-4 6z" fill="var(--foreground)" fillOpacity={0.55} />
                    </svg>
                    {label}
                </div>
            ))}
            {lines.map((line, i) => {
                if (line.points.length === 0) {
                    return null;
                }
                const avg = row[key(i)];
                const max = row[`${key(i)}_max`];
                return (
                    <div key={key(i)} className="flex items-center gap-2">
                        <Swatch {...styleOf(i)} />
                        <span className="font-medium tabular-nums">{avg != null ? format(avg) : '—'}</span>
                        <span className="text-muted-foreground">{line.label}</span>
                        {avg != null && max != null && format(max) !== format(avg) && (
                            <span className="ml-auto pl-2 text-muted-foreground tabular-nums">peak {format(max)}</span>
                        )}
                    </div>
                );
            })}
        </div>
    );
}

// "18 Oct, 14:05 · 5 min average".
function formatBucket(ms: number, step: number): string {
    const when = new Date(ms).toLocaleString(undefined, {
        day: 'numeric',
        month: 'short',
        hour: '2-digit',
        minute: '2-digit',
    });
    return `${when} · ${formatStep(step)} average`;
}

const MINUTE = 60;
const HOUR = 3600;
const DAY = 86400;
const tickSteps = [
    5 * MINUTE,
    10 * MINUTE,
    15 * MINUTE,
    30 * MINUTE,
    HOUR,
    2 * HOUR,
    3 * HOUR,
    4 * HOUR,
    6 * HOUR,
    12 * HOUR,
    DAY,
    2 * DAY,
    7 * DAY,
    14 * DAY,
];

// Ticks on round local times (every 10 minutes, every 4 hours, each
// midnight, every week), about six across the range.
export function timeTicks(from: number, to: number): { values: number[]; format: (ms: number) => string } {
    const span = to - from;
    const step = tickSteps.find((s) => span / s <= 7) ?? tickSteps[tickSteps.length - 1];
    // Seconds east of UTC, so ticks land on local hours and midnights.
    const offset = -new Date(to * 1000).getTimezoneOffset() * 60;
    const values: number[] = [];
    for (let t = Math.ceil((from + offset) / step) * step - offset; t <= to; t += step) {
        values.push(t * 1000);
    }
    const format =
        step >= 2 * DAY
            ? (ms: number) => new Date(ms).toLocaleDateString(undefined, { day: 'numeric', month: 'short' })
            : step >= DAY
              ? (ms: number) => new Date(ms).toLocaleDateString(undefined, { weekday: 'short', day: 'numeric' })
              : (ms: number) => new Date(ms).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });

    return { values, format };
}
