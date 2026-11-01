import type { ReactNode } from 'react';

import { MeterBar, meterLevel } from '@/Components/Meter';
import { formatBytes } from '@/lib/format';
import { cn } from '@/lib/utils';

// Tile is one figure read at a glance: a label, a large value, an optional
// bar and a line of detail. The dashboard's sidebar and the Server page
// lay them out in grids.
export function Tile({
    label,
    value,
    bar,
    detail,
}: {
    label: ReactNode;
    value: ReactNode;
    bar?: ReactNode;
    detail?: string;
}) {
    return (
        <div className="min-w-0">
            <p className="truncate text-xs text-muted-foreground">{label}</p>
            <p className="mt-1 text-2xl font-medium tracking-tight tabular-nums">{value}</p>
            {bar && <div className="mt-2">{bar}</div>}
            {detail && <p className="mt-2 truncate text-[11px] text-muted-foreground tabular-nums">{detail}</p>}
        </div>
    );
}

// PercentTile is a Tile for a percentage, with a Meter's bar.
export function PercentTile({ label, percent, detail }: { label: ReactNode; percent: number | null; detail?: string }) {
    return (
        <Tile
            label={label}
            value={<Percent value={percent} />}
            bar={percent != null && <MeterBar percent={percent} className="h-1" />}
            detail={detail}
        />
    );
}

// "1.2 / 5.9 GiB", naming the unit once when both share it.
export function usedOf(used: number, total: number): string {
    const [usedValue, usedUnit] = formatBytes(used).split(' ');
    const whole = formatBytes(total);

    return whole.endsWith(` ${usedUnit}`) ? `${usedValue} / ${whole}` : `${usedValue} ${usedUnit} / ${whole}`;
}

function Percent({ value, className }: { value: number | null; className?: string }) {
    if (value == null) {
        return <span className="text-muted-foreground">—</span>;
    }

    return (
        <span className={cn(meterLevel(value) === 'critical' && 'text-destructive', className)}>
            {value.toFixed(0)}
            <span className="text-[0.7em] text-muted-foreground">%</span>
        </span>
    );
}
