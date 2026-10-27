import type { ReactNode } from 'react';

import { cn } from '@/lib/utils';

// From here a meter is red, and from WARNING_PERCENT amber.
export const CRITICAL_PERCENT = 90;
const WARNING_PERCENT = 75;

export function meterLevel(percent: number): 'normal' | 'warning' | 'critical' {
    return percent >= CRITICAL_PERCENT ? 'critical' : percent >= WARNING_PERCENT ? 'warning' : 'normal';
}

// A percentage with a thin bar, amber from 75% and red from 90%, and an
// optional line of detail under it ("12 GiB of 40 GiB").
export function Meter({
    percent,
    detail,
    className,
}: {
    percent: number | null | undefined;
    detail?: ReactNode;
    className?: string;
}) {
    if (percent == null) {
        return <span className="text-muted-foreground">—</span>;
    }
    const level = meterLevel(percent);

    return (
        <div className={cn('flex flex-col gap-1.5', className)}>
            <div className="flex items-baseline justify-between gap-3">
                <span className={cn('text-sm font-medium tabular-nums', level === 'critical' && 'text-destructive')}>
                    {percent.toFixed(0)}%
                </span>
                {detail && <span className="truncate text-xs text-muted-foreground tabular-nums">{detail}</span>}
            </div>
            <MeterBar percent={percent} />
        </div>
    );
}

// MeterBar is a Meter's bar alone, for a figure shown elsewhere.
export function MeterBar({ percent, className }: { percent: number; className?: string }) {
    const level = meterLevel(percent);

    return (
        // biome-ignore lint/a11y/useSemanticElements: a native <meter> can't hold the fill, which is coloured by level
        <div
            className={cn('h-1.5 overflow-hidden rounded-full bg-muted', className)}
            role="meter"
            aria-valuenow={Math.round(percent)}
            aria-valuemin={0}
            aria-valuemax={100}
        >
            <div
                className={cn(
                    'h-full rounded-full',
                    level === 'critical'
                        ? 'bg-destructive'
                        : level === 'warning'
                          ? 'bg-warning'
                          : 'bg-muted-foreground',
                )}
                style={{ width: `${Math.min(Math.max(percent, 0), 100)}%` }}
            />
        </div>
    );
}
