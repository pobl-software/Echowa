import { type ComponentProps, lazy, Suspense } from 'react';

import type { MetricChart as Chart } from '@/Components/MetricChart';

const Loaded = lazy(() => import('@/Components/MetricChart').then((module) => ({ default: module.MetricChart })));

// MetricChart in its own chunk: Recharts is most of its weight, and only
// the pages that chart should load it. Import this, and MetricChart's types
// with `import type`.
export function MetricChart(props: ComponentProps<typeof Chart>) {
    return (
        <Suspense fallback={<div className="h-55" />}>
            <Loaded {...props} />
        </Suspense>
    );
}
