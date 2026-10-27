import { router } from '@inertiajs/react';
import { useState } from 'react';

import { Tabs, TabsList, TabsTrigger } from '@/Components/ui/tabs';
import { formatStep } from '@/lib/format';
import type { SeriesWindow } from '@/types/metrics';

const rangeLabels: Record<string, string> = {
    '1h': '1 hour',
    '6h': '6 hours',
    '24h': '24 hours',
    '7d': '7 days',
    '30d': '30 days',
    '90d': '90 days',
};

// The chart range in ?range=. Picking one reloads only the page's series
// props, "series" unless props names others. While it loads, range is the
// one picked, so the tabs move at once and the charts can dim.
export function useSeriesRange(series: SeriesWindow, props: string[] = ['series']) {
    const [loading, setLoading] = useState<string | null>(null);

    const pick = (next: string) => {
        if (next === series.range) {
            return;
        }
        const query = Object.fromEntries(new URLSearchParams(window.location.search));
        router.get(
            window.location.pathname,
            { ...query, range: next },
            {
                only: props,
                preserveState: true,
                preserveScroll: true,
                replace: true,
                onStart: () => setLoading(next),
                onFinish: () => setLoading(null),
            },
        );
    };

    return { range: loading ?? series.range, loading: loading !== null, pick };
}

// Tabs for the ranges, and what the charts show: "Last 24 hours · 5 min averages".
export function RangePicker({
    ranges,
    range,
    step,
    onPick,
}: {
    ranges: string[];
    range: string;
    step: number;
    onPick: (range: string) => void;
}) {
    return (
        <div className="flex flex-wrap items-center gap-3">
            <Tabs value={range} onValueChange={(value) => onPick(String(value))}>
                <TabsList aria-label="Time range">
                    {ranges.map((key) => (
                        <TabsTrigger key={key} value={key} className="px-2.5" title={`Last ${rangeLabels[key] ?? key}`}>
                            {key}
                        </TabsTrigger>
                    ))}
                </TabsList>
            </Tabs>
            <span className="text-sm text-muted-foreground">
                Last {rangeLabels[range] ?? range} · {formatStep(step)} averages
            </span>
        </div>
    );
}
