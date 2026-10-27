import type { CSSProperties, ReactNode } from 'react';

import { cn } from '@/lib/utils';

// Panel is a titled section: a header row, then its content. Pass flush for
// tables and lists that run edge to edge.
export function Panel({
    title,
    description,
    actions,
    flush = false,
    tone,
    className,
    children,
}: {
    title?: ReactNode;
    description?: ReactNode;
    actions?: ReactNode;
    flush?: boolean;
    tone?: 'danger';
    className?: string;
    children?: ReactNode;
}) {
    return (
        <section
            className={cn(
                'overflow-hidden rounded-xl border bg-card text-card-foreground',
                tone === 'danger' && 'border-destructive/30',
                className,
            )}
        >
            {(title || actions) && (
                <div className="flex flex-wrap items-start justify-between gap-3 border-b px-4 py-3 sm:px-5">
                    <div className="min-w-0">
                        {title && (
                            <h2 className={cn('text-sm font-medium', tone === 'danger' && 'text-destructive')}>
                                {title}
                            </h2>
                        )}
                        {description && <p className="mt-0.5 text-sm text-muted-foreground">{description}</p>}
                    </div>
                    {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
                </div>
            )}
            {children !== undefined && <div className={flush ? undefined : 'p-4 sm:p-5'}>{children}</div>}
        </section>
    );
}

// Stats is a strip of figures in one panel: two per row on phones, one row
// on wide screens. An item's visual, such as a sparkline or a meter's bar,
// sits at the foot of its cell, so a row of them lines up. className can
// change the columns.
export function Stats({
    items,
    className,
}: {
    items: { label: string; value: ReactNode; hint?: ReactNode; visual?: ReactNode }[];
    className?: string;
}) {
    return (
        <dl
            className={cn(
                'grid grid-cols-2 gap-px overflow-hidden rounded-xl border bg-border lg:grid-cols-[repeat(var(--stats),minmax(0,1fr))]',
                className,
            )}
            style={{ '--stats': items.length } as CSSProperties}
        >
            {items.map((item) => (
                <div
                    key={item.label}
                    className="flex min-w-0 flex-col bg-card px-4 py-3.5 last:odd:col-span-2 sm:px-5 lg:last:odd:col-span-1"
                >
                    <dt className="text-xs text-muted-foreground">{item.label}</dt>
                    <dd className="mt-1 truncate text-lg font-semibold tracking-tight tabular-nums">{item.value}</dd>
                    {item.hint && <dd className="truncate text-xs text-muted-foreground">{item.hint}</dd>}
                    {item.visual && <dd className="mt-auto pt-3">{item.visual}</dd>}
                </div>
            ))}
        </dl>
    );
}

// ChartGrid is a panel of small charts in a grid, divided by hairlines
// rather than each in a panel of its own. Two across from md; an odd one
// out at the end runs the full width.
export function ChartGrid({
    title,
    description,
    className,
    children,
}: {
    title: ReactNode;
    description?: ReactNode;
    className?: string;
    children: ReactNode;
}) {
    return (
        <Panel title={title} description={description} flush className={className}>
            <div className="grid gap-px bg-border md:grid-cols-2">{children}</div>
        </Panel>
    );
}

// ChartCell is one chart in a ChartGrid: a title, a line saying how to read
// it, and the chart.
export function ChartCell({ title, hint, children }: { title: ReactNode; hint?: ReactNode; children: ReactNode }) {
    return (
        <section className="min-w-0 bg-card p-4 sm:p-5 md:last:odd:col-span-2">
            <h3 className="text-sm font-medium">{title}</h3>
            {hint && <p className="mt-0.5 text-xs text-muted-foreground">{hint}</p>}
            <div className="mt-3">{children}</div>
        </section>
    );
}
