import { Link } from '@inertiajs/react';
import {
    ArrowUpRightIcon,
    ClockIcon,
    type LucideIcon,
    PackageIcon,
    RotateCwIcon,
    ServerIcon,
    ShieldIcon,
    SquareActivityIcon,
    TimerIcon,
} from 'lucide-react';
import type { ReactNode } from 'react';

import { PercentTile, Tile, usedOf } from '@/Components/FigureTile';
import { SectionLabel } from '@/Components/SectionLabel';
import { StatusPill } from '@/Components/StatusBadge';
import { parseServerTime, uptime } from '@/lib/time';
import { cn } from '@/lib/utils';
import { diskPercent, memoryPercent, type Reading } from '@/types/metrics';
import type { AppSummary, ServerFacts, ServerMaintenance } from '@/types/models';

// From this many pending security updates the check is red, not amber.
const SECURITY_UPDATES_CRITICAL = 10;
// A restart needed this long is red.
const RESTART_CRITICAL_DAYS = 7;
// An OS release this close to its end of support is amber.
const OS_SUPPORT_WARNING_DAYS = 180;

const DAY = 86_400_000;

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
    maintenance: ServerMaintenance;
}) {
    const list = checks(facts, maintenance);
    const issues = list.filter((check) => check.tone === 'warning' || check.tone === 'danger').length;

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
                <div className="mb-1 flex items-center justify-between gap-3">
                    <SectionLabel>Maintenance</SectionLabel>
                    {issues > 0 ? (
                        <StatusPill tone={list.some((check) => check.tone === 'danger') ? 'danger' : 'warning'}>
                            {issues} to review
                        </StatusPill>
                    ) : (
                        <StatusPill tone="success">All clear</StatusPill>
                    )}
                </div>
                <ul className="divide-y">
                    {list.map((check) => (
                        <CheckRow key={check.key} check={check} />
                    ))}
                </ul>
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

type CheckTone = 'ok' | 'unknown' | 'warning' | 'danger' | 'update';

interface Check {
    key: string;
    icon: LucideIcon;
    label: string;
    value: ReactNode;
    tone: CheckTone;
    // A line under the row, for a check that needs something done.
    detail?: string;
    href?: string;
}

const rank: Record<CheckTone, number> = { danger: 0, warning: 1, update: 2, unknown: 3, ok: 4 };

// checks turns the server's state into rows, worst first; rows that tie
// keep this order.
function checks(facts: ServerFacts, m: ServerMaintenance): Check[] {
    const now = Date.now();
    const list: Check[] = [];

    const restartDays = m.restart_required_since
        ? Math.floor((now - parseServerTime(m.restart_required_since).getTime()) / DAY)
        : null;
    list.push({
        key: 'restart',
        icon: RotateCwIcon,
        ...(restartDays == null
            ? { label: 'Restart', tone: 'ok', value: 'Not needed' }
            : {
                  label: 'Restart required',
                  tone: restartDays >= RESTART_CRITICAL_DAYS ? 'danger' : 'warning',
                  value: restartDays === 0 ? 'Today' : days(restartDays),
                  detail: 'Updates installed since the last boot need a restart to apply.',
              }),
    });

    const updates = m.security_updates;
    list.push({
        key: 'security',
        icon: ShieldIcon,
        label: 'Security updates',
        ...(updates == null
            ? { tone: 'unknown', value: 'Not checked' }
            : updates === 0
              ? { tone: 'ok', value: 'None' }
              : {
                    tone: updates >= SECURITY_UPDATES_CRITICAL ? 'danger' : 'warning',
                    value: `${updates} pending`,
                }),
    });

    const failed = m.failed_services;
    list.push({
        key: 'services',
        icon: SquareActivityIcon,
        label: 'System services',
        ...(failed == null
            ? { tone: 'unknown', value: 'Not checked' }
            : failed.length === 0
              ? { tone: 'ok', value: 'All running' }
              : { tone: 'danger', value: `${failed.length} failed`, detail: failed.join(', ') }),
    });

    const supportDays = m.os_support_ends
        ? Math.ceil((new Date(`${m.os_support_ends}T00:00:00Z`).getTime() - now) / DAY)
        : null;
    list.push({
        key: 'support',
        icon: TimerIcon,
        label: 'OS support',
        ...(supportDays == null
            ? { tone: 'unknown', value: 'Unknown' }
            : supportDays <= 0
              ? { tone: 'danger', value: 'Ended', detail: `${facts.os || 'This release'} no longer gets updates.` }
              : supportDays <= OS_SUPPORT_WARNING_DAYS
                ? { tone: 'warning', value: `${days(supportDays)} left` }
                : { tone: 'ok', value: `Until ${monthYear(m.os_support_ends as string)}` }),
    });

    list.push({
        key: 'clock',
        icon: ClockIcon,
        label: 'Clock',
        ...(m.clock_synced == null
            ? { tone: 'unknown', value: 'Not checked' }
            : m.clock_synced
              ? { tone: 'ok', value: 'Synced' }
              : {
                    tone: 'warning',
                    value: 'Not synced',
                    detail: 'Certificates, schedules and backups rely on the time.',
                }),
    });

    list.push({
        key: 'echowa',
        icon: PackageIcon,
        label: 'Echowa',
        ...(facts.update
            ? { tone: 'update', value: `${facts.update} available`, href: '/settings/updates' }
            : {
                  tone: facts.update_checked ? 'ok' : 'unknown',
                  value: facts.update_checked ? facts.version : `${facts.version} · not checked`,
              }),
    });

    return list
        .map((check, i) => ({ check, i }))
        .sort((a, b) => rank[a.check.tone] - rank[b.check.tone] || a.i - b.i)
        .map(({ check }) => check);
}

const toneStyles: Record<CheckTone, { tile: string; value: string }> = {
    ok: { tile: 'text-muted-foreground', value: 'text-muted-foreground' },
    unknown: { tile: 'text-muted-foreground/60', value: 'text-muted-foreground/70' },
    warning: { tile: 'border-warning/30 bg-warning/10 text-warning', value: 'text-warning' },
    danger: { tile: 'border-destructive/30 bg-destructive/10 text-destructive', value: 'text-destructive' },
    update: { tile: 'border-primary/30 bg-primary/10 text-primary', value: 'text-primary' },
};

function CheckRow({ check }: { check: Check }) {
    const style = toneStyles[check.tone];
    const Icon = check.icon;
    const body = (
        <>
            <span
                className={cn(
                    'flex size-7 shrink-0 items-center justify-center rounded-md border bg-muted/40',
                    style.tile,
                )}
            >
                <Icon className="size-3.5" />
            </span>
            <div className="min-w-0 flex-1">
                <div className="flex items-baseline justify-between gap-3">
                    <span className="truncate text-sm">{check.label}</span>
                    <span
                        className={cn(
                            'inline-flex shrink-0 items-center gap-0.5 font-mono text-xs tabular-nums',
                            style.value,
                        )}
                    >
                        {check.value}
                        {check.href && <ArrowUpRightIcon className="size-3" />}
                    </span>
                </div>
                {check.detail && <p className="mt-0.5 text-xs text-muted-foreground">{check.detail}</p>}
            </div>
        </>
    );

    return (
        <li>
            {check.href ? (
                <Link
                    href={check.href}
                    className="-mx-2 flex gap-3 rounded-md px-2 py-2.5 transition-colors hover:bg-muted/50"
                >
                    {body}
                </Link>
            ) : (
                <div className="flex gap-3 py-2.5">{body}</div>
            )}
        </li>
    );
}

// "1 day", "3 days".
function days(n: number): string {
    return `${n} ${n === 1 ? 'day' : 'days'}`;
}

// "May 2029".
function monthYear(date: string): string {
    return new Date(`${date}T00:00:00Z`).toLocaleDateString(undefined, {
        month: 'short',
        year: 'numeric',
        timeZone: 'UTC',
    });
}
