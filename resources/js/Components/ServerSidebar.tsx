import { Link, router } from '@inertiajs/react';
import { ArrowUpRightIcon, RefreshCwIcon, ServerIcon } from 'lucide-react';
import { useState } from 'react';

import { PercentTile, Tile, usedOf } from '@/Components/FigureTile';
import { SectionLabel } from '@/Components/SectionLabel';
import { StatusPill } from '@/Components/StatusBadge';
import { Button } from '@/Components/ui/button';
import { type Check, type CheckTone, isDue, maintenanceChecks, measured } from '@/lib/maintenance';
import { formatDateTime, timeAgo, uptime } from '@/lib/time';
import { cn } from '@/lib/utils';
import { diskPercent, memoryPercent, type Reading } from '@/types/metrics';
import type { AppSummary, Maintenance, ServerFacts } from '@/types/models';

// ServerSidebar is the dashboard's right column, one panel: the machine at
// the top, shaped like a server switcher that opens the server's page, its newest figures, then the checks on what needs doing to
// it, problems first.
export function ServerSidebar({
    facts,
    reading,
    apps,
    maintenance,
}: {
    facts: ServerFacts;
    apps: AppSummary[];
    reading: Reading | null;
    // The last read of what the server needs, from the Server page or
    // Refresh; null when there's been none since Echowa started.
    maintenance: Maintenance | null;
}) {
    return (
        <div className="min-h-full divide-y bg-card">
            <Machine facts={facts} />

            <section aria-label="Figures" className="px-5 py-5">
                {reading?.host ? (
                    <Figures reading={reading} apps={apps} />
                ) : (
                    <p className="text-xs text-muted-foreground">Reading the server…</p>
                )}
            </section>

            <section aria-label="Maintenance" className="px-5 py-5">
                {maintenance ? (
                    <Checks maintenance={maintenance} facts={facts} />
                ) : (
                    <SectionLabel>Maintenance</SectionLabel>
                )}
                <Refresh readAt={maintenance?.read_at ?? null} />
            </section>
        </div>
    );
}

function Machine({ facts }: { facts: ServerFacts }) {
    const about = [facts.os, facts.booted_at && `up ${uptime(facts.booted_at)}`].filter(Boolean).join(' · ');

    return (
        // h-14, as Page's top bar, so the line under it meets the top bar's.
        <div className="flex h-14 items-center px-2">
            <Link
                href="/server"
                className="group flex min-w-0 flex-1 items-center gap-2.5 rounded-lg px-2 py-1 transition-colors hover:bg-muted/50"
            >
                <span className="flex size-8 shrink-0 items-center justify-center rounded-md border bg-muted/50">
                    <ServerIcon className="size-4 text-muted-foreground" />
                </span>
                <span className="min-w-0 flex-1">
                    <span className="block truncate font-mono text-sm font-medium" title={facts.kernel || undefined}>
                        {facts.hostname || 'This server'}
                    </span>
                    {about && <span className="block truncate text-xs text-muted-foreground">{about}</span>}
                </span>
                <ArrowUpRightIcon className="size-4 shrink-0 text-muted-foreground transition-colors group-hover:text-foreground" />
            </Link>
        </div>
    );
}

// The figures as tiles, two to a row: CPU, memory, each disk, then the
// containers.
function Figures({ reading, apps }: { reading: Reading; apps: AppSummary[] }) {
    const host = reading.host;

    return (
        <div className="grid grid-cols-2 gap-x-6 gap-y-5">
            <PercentTile
                label="CPU"
                percent={host.cpu_percent}
                detail={host.load_1m != null ? `load ${host.load_1m.toFixed(2)}` : undefined}
            />
            <PercentTile
                label="Memory"
                percent={memoryPercent(host)}
                detail={
                    host.memory_used_bytes != null && host.memory_total_bytes != null
                        ? usedOf(host.memory_used_bytes, host.memory_total_bytes)
                        : undefined
                }
            />
            {reading.disks.map((disk) => (
                <PercentTile
                    key={disk.mount}
                    label={
                        <>
                            Disk <span className="font-mono">{disk.mount}</span>
                        </>
                    }
                    percent={diskPercent(disk)}
                    detail={usedOf(disk.used_bytes, disk.used_bytes + disk.available_bytes)}
                />
            ))}
            <ContainersTile running={host.containers} apps={apps} />
        </div>
    );
}

// How many containers run, with a bar split by state: apps' tasks that
// run, Echowa's own (Traefik, poolers), and the tasks apps are short of,
// red when the app is down or unhealthy and amber otherwise.
function ContainersTile({ running, apps }: { running: number | null; apps: AppSummary[] }) {
    let tasks = 0;
    let short = 0;
    let failing = 0;
    for (const { status } of apps) {
        tasks += status.running;
        const missing = Math.max(0, status.desired - status.running);
        if (status.state === 'down' || status.unhealthy_since) {
            failing += missing;
        } else {
            short += missing;
        }
    }
    const own = running == null ? 0 : Math.max(0, running - tasks);
    const segments = [
        { count: Math.min(tasks, running ?? tasks), className: 'bg-success' },
        { count: own, className: 'bg-muted-foreground' },
        { count: short, className: 'bg-warning' },
        { count: failing, className: 'bg-destructive' },
    ].filter((segment) => segment.count > 0);
    const total = segments.reduce((sum, segment) => sum + segment.count, 0);
    const down = short + failing;

    return (
        <Tile
            label="Containers"
            value={running ?? <span className="text-muted-foreground">—</span>}
            bar={
                total > 0 && (
                    <div className="flex h-1 gap-px overflow-hidden rounded-full bg-muted">
                        {segments.map((segment) => (
                            <div
                                key={segment.className}
                                className={cn('h-full', segment.className)}
                                style={{ width: `${(100 * segment.count) / total}%` }}
                            />
                        ))}
                    </div>
                )
            }
            detail={
                down > 0
                    ? `${down} not running`
                    : running != null
                      ? [`${tasks} ${tasks === 1 ? 'app' : 'apps'}`, own > 0 && `${own} other`]
                            .filter(Boolean)
                            .join(' · ')
                      : undefined
            }
        />
    );
}

// Checks lists the summary checks, and any other that needs doing, worst
// first (lib/maintenance.ts). The pill counts what's amber or red, and
// says All clear only when Echowa could read the server.
function Checks({ maintenance, facts }: { maintenance: Maintenance; facts: ServerFacts }) {
    const list = maintenanceChecks(maintenance, facts).filter((check) => check.summary || isDue(check));
    const issues = list.filter((check) => check.tone === 'warning' || check.tone === 'danger').length;

    return (
        <>
            <div className="mb-2 flex items-center justify-between gap-3">
                <SectionLabel>Maintenance</SectionLabel>
                {issues > 0 ? (
                    <StatusPill tone={list.some((check) => check.tone === 'danger') ? 'danger' : 'warning'}>
                        {issues} to review
                    </StatusPill>
                ) : (
                    measured(maintenance) && <StatusPill tone="success">All clear</StatusPill>
                )}
            </div>
            <ul>
                {list.map((check) => (
                    <CheckRow key={check.key} check={check} />
                ))}
            </ul>
        </>
    );
}

// Refresh says when the server was last read, and reads it again. The
// dashboard never reads it by itself (ADR 0041); the Server page does.
function Refresh({ readAt }: { readAt: string | null }) {
    const [refreshing, setRefreshing] = useState(false);
    const refresh = () =>
        router.reload({
            only: ['freshMaintenance'],
            onStart: () => setRefreshing(true),
            onFinish: () => setRefreshing(false),
            // The dot reads apt's last run, which this read may have made.
            onSuccess: () => router.reload({ only: ['serverAttention'] }),
        });

    return (
        <div className="mt-2 flex items-center justify-between gap-3 text-xs text-muted-foreground">
            {refreshing ? (
                <span>Checking the server…</span>
            ) : readAt ? (
                <span title={formatDateTime(readAt)}>Checked {timeAgo(readAt)}</span>
            ) : (
                <span>Not checked since Echowa started.</span>
            )}
            <Button variant="ghost" size="xs" className="-mr-2" onClick={refresh} disabled={refreshing}>
                <RefreshCwIcon className={cn(refreshing && 'animate-spin')} />
                Refresh
            </Button>
        </div>
    );
}

const toneStyles: Record<CheckTone, { tile: string; value: string }> = {
    ok: { tile: 'text-muted-foreground', value: 'text-muted-foreground' },
    unknown: { tile: 'text-muted-foreground/60', value: 'text-muted-foreground/70' },
    warning: { tile: 'border-warning/30 bg-warning/10 text-warning', value: 'text-warning' },
    danger: { tile: 'border-destructive/30 bg-destructive/10 text-destructive', value: 'text-destructive' },
    update: { tile: 'border-primary/30 bg-primary/10 text-primary', value: 'text-primary' },
};

// CheckRow is a check in one line: its icon tinted by its state, its name
// and a few words. Why is in its tooltip; one that needs doing links to the
// Server page, which says why and how.
function CheckRow({ check }: { check: Check }) {
    const style = toneStyles[check.tone];
    const Icon = check.icon;
    const href = check.href ?? (isDue(check) ? '/server' : undefined);
    const body = (
        <>
            <span
                className={cn(
                    'flex size-6 shrink-0 items-center justify-center rounded-md border bg-muted/40',
                    style.tile,
                )}
            >
                <Icon className="size-3" />
            </span>
            <span className="min-w-0 flex-1 truncate text-sm">{check.label}</span>
            <span
                className={cn('inline-flex shrink-0 items-center gap-0.5 font-mono text-xs tabular-nums', style.value)}
            >
                {check.value}
                {href && <ArrowUpRightIcon className="size-3" />}
            </span>
        </>
    );

    return (
        <li title={check.why}>
            {href ? (
                <Link
                    href={href}
                    className="-mx-2 flex items-center gap-2.5 rounded-md px-2 py-1.5 transition-colors hover:bg-muted/50"
                >
                    {body}
                </Link>
            ) : (
                <div className="flex items-center gap-2.5 py-1.5">{body}</div>
            )}
        </li>
    );
}
