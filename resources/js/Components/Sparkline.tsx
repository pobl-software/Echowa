import { useId } from 'react';

import { cn } from '@/lib/utils';
import type { Point } from '@/types/metrics';

// Sparkline is a figure's trend over the chart range: its buckets' averages
// as a line with a faint fill, placed by time like MetricChart. A gap of
// up to three buckets is bridged, since this is a trend; the charts show
// every gap. Plain SVG, so a strip of them needs no Recharts. max fixes
// the top, e.g. 100 for percentages; otherwise the data's max sits a fifth
// below it, so a steady figure doesn't read as maxed out.
export function Sparkline({
    points,
    from,
    to,
    step,
    max,
    className,
}: {
    points: Point[];
    from: number;
    to: number;
    step: number;
    max?: number;
    className?: string;
}) {
    const fill = useId();
    const segments = segmentsOf(points, step);
    const known = points.flatMap((p) => (p.avg != null ? [p.avg] : []));
    const top = max ?? Math.max(...known, 0) * 1.25;
    const span = Math.max(to - from, 1);
    // Lines keep a pixel off the top and bottom, so a flat line at 0 or the
    // max still shows.
    const x = (t: number) => (100 * (t - from)) / span;
    const y = (v: number) => (top > 0 ? 31 - (30 * v) / top : 31);

    return (
        <svg
            viewBox="0 0 100 32"
            preserveAspectRatio="none"
            className={cn('h-8 w-full overflow-visible', className)}
            aria-hidden="true"
        >
            <defs>
                <linearGradient id={fill} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0" stopColor="var(--primary)" stopOpacity={0.18} />
                    <stop offset="1" stopColor="var(--primary)" stopOpacity={0} />
                </linearGradient>
            </defs>
            {segments.map((segment) => {
                // A lone bucket has no neighbour to draw to, so it's a short dash.
                const line =
                    segment.length === 1
                        ? `M${x(segment[0].t) - 0.75},${y(segment[0].avg)}h1.5`
                        : segment.map((p, j) => `${j === 0 ? 'M' : 'L'}${x(p.t)},${y(p.avg)}`).join('');
                const area =
                    segment.length > 1 && `${line}L${x(segment[segment.length - 1].t)},32L${x(segment[0].t)},32Z`;
                return (
                    <g key={segment[0].t}>
                        {area && <path d={area} fill={`url(#${fill})`} />}
                        <path
                            d={line}
                            fill="none"
                            className="stroke-primary"
                            strokeWidth={1.5}
                            strokeLinejoin="round"
                            vectorEffect="non-scaling-stroke"
                        />
                    </g>
                );
            })}
        </svg>
    );
}

type Known = { t: number; avg: number };

// The runs of measured buckets, split where more than three are missing.
function segmentsOf(points: Point[], step: number): Known[][] {
    const segments: Known[][] = [];
    let last: Known | undefined;
    for (const p of points) {
        if (p.avg == null) {
            continue;
        }
        const point = { t: p.t, avg: p.avg };
        if (last && point.t - last.t <= step * 4) {
            segments[segments.length - 1].push(point);
        } else {
            segments.push([point]);
        }
        last = point;
    }

    return segments;
}
