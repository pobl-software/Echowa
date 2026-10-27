import { Link } from '@inertiajs/react';

import { RedeployButton } from '@/Components/RedeployButton';
import { CurrentBadge, DeploymentStatusBadge } from '@/Components/StatusBadge';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/Components/ui/table';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/Components/ui/tooltip';
import { duration, formatDateTime, timeAgo } from '@/lib/time';
import { cn } from '@/lib/utils';
import type { DeploymentSummary } from '@/types/models';

const shortRef = (ref: string) => (ref.startsWith('sha256:') ? ref.slice(0, 19) : ref);
const shortDigest = (digest: string | null) => (digest?.split('sha256:')[1] ?? '').slice(0, 12);

export function DeploymentsTable({
    appId,
    deployments,
    currentId,
}: {
    appId: number;
    deployments: DeploymentSummary[];
    currentId: number | null;
}) {
    if (deployments.length === 0) {
        return (
            <p className="px-4 py-8 text-center text-sm text-muted-foreground sm:px-5">
                No deployments yet. Use Deploy to roll out the first one.
            </p>
        );
    }

    return (
        <Table>
            <TableHeader>
                <TableRow>
                    <TableHead>#</TableHead>
                    <TableHead className="w-full sm:w-auto">Status</TableHead>
                    {/* Ref takes the spare width (Status, on phones), so the id, status and ref sit together and the details line up on the right. */}
                    <TableHead className="hidden w-full sm:table-cell">Ref</TableHead>
                    <TableHead className="hidden md:table-cell">Trigger</TableHead>
                    <TableHead className="hidden sm:table-cell">Started</TableHead>
                    <TableHead className="hidden text-right xl:table-cell">Took</TableHead>
                    <TableHead>
                        <span className="sr-only">Actions</span>
                    </TableHead>
                </TableRow>
            </TableHeader>
            <TableBody>
                {deployments.map((d) => {
                    const current = d.id === currentId;
                    return (
                        <TableRow
                            key={d.id}
                            className={cn(current && 'bg-primary/[0.04] hover:bg-primary/[0.07]')}
                            aria-current={current || undefined}
                        >
                            {/* An accent bar on the left edge marks the running version. */}
                            <TableCell
                                className={cn(
                                    'relative',
                                    current &&
                                        'before:absolute before:inset-y-0 before:left-0 before:w-0.5 before:bg-primary',
                                )}
                            >
                                <Link
                                    href={`/apps/${appId}/deployments/${d.id}`}
                                    className="font-medium hover:underline"
                                >
                                    #{d.id}
                                </Link>
                                {/* Phones have no Ref column. */}
                                <div className="font-mono text-xs text-muted-foreground sm:hidden">
                                    {shortRef(d.ref)}
                                </div>
                            </TableCell>
                            <TableCell>
                                <span className="flex items-center gap-2">
                                    <DeploymentStatusBadge status={d.status} quiet />
                                    {current && <CurrentBadge />}
                                </span>
                            </TableCell>
                            <TableCell className="hidden font-mono text-xs sm:table-cell">
                                {/* The digest it pulled shows on hover. */}
                                {d.digest ? (
                                    <Tooltip>
                                        <TooltipTrigger
                                            render={
                                                <span className="cursor-default underline decoration-dotted decoration-muted-foreground/50 underline-offset-4" />
                                            }
                                        >
                                            {shortRef(d.ref)}
                                        </TooltipTrigger>
                                        <TooltipContent className="font-mono">
                                            sha256:{shortDigest(d.digest)}
                                        </TooltipContent>
                                    </Tooltip>
                                ) : (
                                    shortRef(d.ref)
                                )}
                            </TableCell>
                            <TableCell className="hidden text-muted-foreground capitalize md:table-cell">
                                {d.trigger}
                            </TableCell>
                            <TableCell
                                title={formatDateTime(d.started_at ?? d.created_at)}
                                className="hidden text-muted-foreground sm:table-cell"
                            >
                                {timeAgo(d.started_at ?? d.created_at)}
                            </TableCell>
                            <TableCell className="hidden text-right text-muted-foreground tabular-nums xl:table-cell">
                                {d.started_at ? duration(d.started_at, d.finished_at) : '—'}
                            </TableCell>
                            <TableCell className="py-1 text-right">
                                <RedeployButton appId={appId} deployment={d} compact />
                            </TableCell>
                        </TableRow>
                    );
                })}
            </TableBody>
        </Table>
    );
}
