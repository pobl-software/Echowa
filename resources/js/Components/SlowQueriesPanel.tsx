import { router } from '@inertiajs/react';
import { RefreshCwIcon, TriangleAlertIcon } from 'lucide-react';
import { useEffect, useState } from 'react';

import { ConfirmDelete } from '@/Components/ConfirmDelete';
import { Panel } from '@/Components/Panel';
import { Alert, AlertDescription } from '@/Components/ui/alert';
import { Button } from '@/Components/ui/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/Components/ui/table';
import { formatCount, formatMilliseconds } from '@/lib/format';
import { timeAgo } from '@/lib/time';
import { cn } from '@/lib/utils';
import type { App, SlowQuery, SlowQueryResult, TemplateOption } from '@/types/models';

// SlowQueriesPanel lists the queries that took the database the most time
// in total (ADR 0027). They're read from the database when the section
// opens and on Refresh, never polled. A template that needs a switch for
// it restarts to turn it on or off.
export function SlowQueriesPanel({
    app,
    template,
    result,
}: {
    app: App;
    template: TemplateOption;
    result: SlowQueryResult | null | undefined;
}) {
    const switched = template.slow_queries === 'switch';
    const on = !switched || app.slow_queries;
    const [loading, setLoading] = useState(false);

    const load = () =>
        router.reload({ only: ['slowQueries'], onStart: () => setLoading(true), onFinish: () => setLoading(false) });
    // biome-ignore lint/correctness/useExhaustiveDependencies: read once when the list turns on; load is rebuilt every render
    useEffect(() => {
        if (on) {
            load();
        }
    }, [on]);

    if (!on) {
        return (
            <Panel
                title="Slow queries"
                description={`Off. When on, ${template.name} keeps totals for each query, which costs a little time on every query.`}
                actions={
                    <ConfirmDelete
                        trigger={<Button size="sm">Turn on</Button>}
                        title="Turn on slow queries?"
                        confirmLabel="Turn on and redeploy"
                        variant="default"
                        onConfirm={() => router.post(`/apps/${app.id}/slow-queries`)}
                    >
                        {app.name} redeploys now to load the statistics extension, so it&apos;s unavailable for a few
                        seconds.
                    </ConfirmDelete>
                }
            />
        );
    }

    const report = result?.report;
    const commands = template.slug === 'redis';

    return (
        <Panel
            title={commands ? 'Slow commands' : 'Slow queries'}
            description={`${commands ? 'The commands that took the most time in total, by name only.' : 'The queries that took the most time in total, with values as placeholders.'}${report?.since ? ` Counting began ${timeAgo(report.since)}.` : ''}`}
            actions={
                <>
                    <Button variant="outline" size="sm" onClick={load} disabled={loading}>
                        <RefreshCwIcon className={cn(loading && 'animate-spin')} />
                        Refresh
                    </Button>
                    <ConfirmDelete
                        trigger={
                            <Button variant="outline" size="sm" disabled={!report}>
                                Reset
                            </Button>
                        }
                        title="Reset the totals?"
                        confirmLabel="Reset"
                        onConfirm={() => router.post(`/apps/${app.id}/slow-queries/reset`, {}, { onSuccess: load })}
                    >
                        Every query&apos;s totals go back to zero, so the list starts again from now. Useful after
                        fixing a slow query.
                    </ConfirmDelete>
                    {switched && (
                        <ConfirmDelete
                            trigger={
                                <Button variant="outline" size="sm">
                                    Turn off
                                </Button>
                            }
                            title="Turn off slow queries?"
                            confirmLabel="Turn off and redeploy"
                            variant="default"
                            onConfirm={() => router.delete(`/apps/${app.id}/slow-queries`)}
                        >
                            {app.name} redeploys now without the statistics extension, so it&apos;s unavailable for a
                            few seconds. The totals so far are lost.
                        </ConfirmDelete>
                    )}
                </>
            }
            className={cn('transition-opacity', loading && 'opacity-60')}
            flush
        >
            {result?.problem ? (
                <div className="p-4 sm:p-5">
                    <Alert>
                        <TriangleAlertIcon />
                        <AlertDescription>{result.problem}</AlertDescription>
                    </Alert>
                </div>
            ) : report ? (
                <QueryTable
                    queries={report.queries}
                    dropped={report.dropped}
                    heading={commands ? 'Command' : 'Query'}
                />
            ) : (
                <p className="p-4 text-sm text-muted-foreground sm:p-5">Reading them from the database…</p>
            )}
        </Panel>
    );
}

function QueryTable({ queries, dropped, heading }: { queries: SlowQuery[]; dropped: number | null; heading: string }) {
    const [expanded, setExpanded] = useState<number | null>(null);
    // Redis counts neither.
    const hasMax = queries.some((q) => q.max_ms != null);
    const hasRows = queries.some((q) => q.rows != null);
    if (queries.length === 0) {
        return <p className="p-4 text-sm text-muted-foreground sm:p-5">No queries yet.</p>;
    }

    return (
        <>
            <Table>
                <TableHeader>
                    <TableRow>
                        <TableHead className="pl-4 sm:pl-5">{heading}</TableHead>
                        <TableHead className="text-right">Total</TableHead>
                        <TableHead className="hidden text-right sm:table-cell">Calls</TableHead>
                        <TableHead className="hidden text-right sm:table-cell">Mean</TableHead>
                        {hasMax && <TableHead className="hidden text-right md:table-cell">Max</TableHead>}
                        {hasRows && (
                            <TableHead className="hidden pr-4 text-right sm:pr-5 lg:table-cell">Rows</TableHead>
                        )}
                    </TableRow>
                </TableHeader>
                <TableBody>
                    {queries.map((q, i) => (
                        // biome-ignore lint/suspicious/noArrayIndexKey: a ranked report; the same text can appear twice, and expanded follows the position
                        <TableRow key={i} className="align-top">
                            <TableCell className="max-w-0 w-full pl-4 whitespace-normal sm:pl-5">
                                <button
                                    type="button"
                                    className="block w-full text-left"
                                    aria-expanded={expanded === i}
                                    onClick={() => setExpanded(expanded === i ? null : i)}
                                >
                                    <code
                                        className={cn(
                                            'block font-mono text-xs wrap-anywhere whitespace-pre-wrap',
                                            expanded !== i && 'line-clamp-2',
                                        )}
                                    >
                                        {q.query}
                                    </code>
                                </button>
                                {/* Phones have only the Total column. */}
                                <div className="mt-1 text-xs text-muted-foreground sm:hidden">
                                    {formatCount(q.calls)} calls, {formatMilliseconds(q.mean_ms)} each
                                </div>
                            </TableCell>
                            <TableCell className="text-right tabular-nums">{formatMilliseconds(q.total_ms)}</TableCell>
                            <TableCell className="hidden text-right tabular-nums sm:table-cell">
                                {formatCount(q.calls)}
                            </TableCell>
                            <TableCell className="hidden text-right tabular-nums sm:table-cell">
                                {formatMilliseconds(q.mean_ms)}
                            </TableCell>
                            {hasMax && (
                                <TableCell className="hidden text-right tabular-nums md:table-cell">
                                    {q.max_ms != null ? formatMilliseconds(q.max_ms) : '—'}
                                </TableCell>
                            )}
                            {hasRows && (
                                <TableCell className="hidden pr-4 text-right tabular-nums sm:pr-5 lg:table-cell">
                                    {q.rows != null ? formatCount(q.rows) : '—'}
                                </TableCell>
                            )}
                        </TableRow>
                    ))}
                </TableBody>
            </Table>
            {dropped != null && dropped > 0 && (
                <p className="border-t px-4 py-3 text-xs text-muted-foreground sm:px-5">
                    {formatCount(dropped)} rarer queries were forgotten to make room, so the totals may miss some.
                </p>
            )}
        </>
    );
}
