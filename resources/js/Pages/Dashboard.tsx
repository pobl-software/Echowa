import { Head, Link, usePoll } from '@inertiajs/react';
import { BoxIcon, ChevronRightIcon, GlobeIcon, PlusIcon } from 'lucide-react';

import { meterLevel } from '@/Components/Meter';
import { Page } from '@/Components/Page';
import { Stats } from '@/Components/Panel';
import { DeploymentStatusBadge, LiveStatusBadge } from '@/Components/StatusBadge';
import { buttonVariants } from '@/Components/ui/button';
import { formatBytes, formatPercent } from '@/lib/format';
import { templateIcon } from '@/lib/templates';
import { formatDateTime, timeAgo } from '@/lib/time';
import { cn } from '@/lib/utils';
import { diskPercent, memoryPercent, type Reading, rootDisk } from '@/types/metrics';
import { type AppSummary, runningStatuses } from '@/types/models';

export default function Dashboard({ apps, metrics }: { apps: AppSummary[]; metrics: Reading | null }) {
    // Echowa polls Swarm every 30 seconds and reads the server every 15; follow along.
    usePoll(30000, { only: ['apps'] });
    usePoll(15000, { only: ['metrics'] });

    const unhealthy = apps.filter((app) => app.status.unhealthy_since).length;
    const running = apps.filter((app) => app.status.state === 'running').length;
    const attention = apps.filter(
        (app) =>
            app.status.state === 'degraded' ||
            app.status.state === 'down' ||
            app.last_deployment?.status === 'failed' ||
            app.last_deployment?.status === 'rolled_back',
    ).length;
    const deploying = apps.filter(
        (app) => app.last_deployment && runningStatuses.includes(app.last_deployment.status),
    ).length;
    // A linked database or service sits under the app that uses it, not on a card of its own (ADRs
    // 0028, 0037). Services are one level deep, so a service's own databases join its app's tray.
    const byId = new Map(apps.map((app) => [app.id, app]));
    const linked = new Set(apps.flatMap((app) => app.links));
    const cards = apps.filter((app) => !linked.has(app.id));
    const tray = (app: AppSummary) => {
        const ids = app.links.flatMap((id) => [id, ...(byId.get(id)?.links ?? [])]);
        return [...new Set(ids)].flatMap((id) => (id === app.id ? [] : (byId.get(id) ?? [])));
    };

    return (
        <>
            <Head title="Apps" />
            <Page
                crumbs={[{ label: 'Apps' }]}
                actions={
                    <Link href="/apps/create" className={buttonVariants()}>
                        <PlusIcon />
                        New app
                    </Link>
                }
            >
                {metrics && <ServerStrip reading={metrics} />}
                {apps.length === 0 ? (
                    <div className="flex flex-col items-center rounded-xl border border-dashed px-6 py-14 text-center">
                        <span className="mb-4 flex size-11 items-center justify-center rounded-xl border bg-card">
                            <BoxIcon className="size-5 text-muted-foreground" />
                        </span>
                        <h2 className="font-medium">No apps yet</h2>
                        <p className="mt-1 max-w-md text-sm text-muted-foreground">
                            An app is one image from a registry, run as a Swarm service behind a domain. If the image is
                            private, add its registry first.
                        </p>
                        <div className="mt-5 flex flex-wrap justify-center gap-2">
                            <Link href="/apps/create" className={buttonVariants()}>
                                Create an app
                            </Link>
                            <Link href="/registries" className={buttonVariants({ variant: 'outline' })}>
                                Registries
                            </Link>
                        </div>
                    </div>
                ) : (
                    <div className="space-y-6">
                        <Stats
                            items={[
                                { label: 'Apps', value: apps.length },
                                { label: 'Running', value: running },
                                {
                                    label: 'Need attention',
                                    value: (
                                        <span className={attention ? 'text-destructive' : undefined}>{attention}</span>
                                    ),
                                    hint: unhealthy > 0 ? `${unhealthy} unhealthy` : undefined,
                                },
                                { label: 'Deploying', value: deploying },
                            ]}
                        />
                        {/* Cards keep their own height: a tray of linked apps makes one taller than its row. */}
                        <ul className="grid items-start gap-4 sm:grid-cols-2 xl:grid-cols-3">
                            {cards.map((app) => (
                                <li key={app.id}>
                                    <AppCard app={app} linked={tray(app)} />
                                </li>
                            ))}
                        </ul>
                    </div>
                )}
            </Page>
        </>
    );
}

// The whole card opens the app; the domain link inside it opens the site.
// Its linked databases and services sit on a tray under it, a row each.
function AppCard({ app, linked }: { app: AppSummary; linked: AppSummary[] }) {
    const card = <AppCardBody app={app} tray={linked.length > 0} />;
    if (linked.length === 0) {
        return card;
    }

    return (
        <div className="flex h-full flex-col rounded-xl border bg-muted/60 dark:bg-muted/30">
            {card}
            <ul aria-label={`${app.name}'s databases and services`} className="p-1">
                {linked.map((db) => {
                    const Icon = templateIcon(db.template);
                    return (
                        <li key={db.id}>
                            <Link
                                href={`/apps/${db.id}`}
                                className="group/db flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm transition-colors hover:bg-background/70 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
                            >
                                <Icon className="size-3.5 shrink-0 text-muted-foreground" />
                                <span className="min-w-0 truncate font-medium">{db.name}</span>
                                <span className="hidden min-w-0 truncate font-mono text-xs text-muted-foreground sm:inline">
                                    {db.repository}
                                </span>
                                <span className="ml-auto flex shrink-0 items-center gap-1.5">
                                    <LiveStatusBadge status={db.status} quiet />
                                    <ChevronRightIcon className="size-4 text-muted-foreground transition-transform group-hover/db:translate-x-0.5" />
                                </span>
                            </Link>
                        </li>
                    );
                })}
            </ul>
        </div>
    );
}

// AppCardBody is the card itself. On a tray it overlaps the tray's border,
// so the two read as one tile.
function AppCardBody({ app, tray }: { app: AppSummary; tray: boolean }) {
    const Icon = templateIcon(app.template);
    const url = app.domain ? `${app.tls ? 'https' : 'http'}://${app.domain}` : null;

    return (
        <article
            className={cn(
                'group relative flex h-full flex-col rounded-xl border bg-card transition-colors hover:border-foreground/20',
                tray && '-m-px shadow-xs',
                app.status.unhealthy_since && 'border-destructive/40 hover:border-destructive/60',
            )}
        >
            <div className="flex items-start gap-3 p-4">
                <span className="flex size-9 shrink-0 items-center justify-center rounded-lg border bg-muted/50">
                    <Icon className="size-4 text-muted-foreground" />
                </span>
                <div className="min-w-0 flex-1">
                    <h2 className="truncate font-medium">
                        <Link
                            href={`/apps/${app.id}`}
                            className="outline-none after:absolute after:inset-0 after:rounded-xl focus-visible:after:ring-2 focus-visible:after:ring-ring"
                        >
                            {app.name}
                        </Link>
                    </h2>
                    <p className="truncate font-mono text-xs text-muted-foreground">{app.repository}</p>
                </div>
                {/* Above the card's link, so hovering the dot shows its word. */}
                <span className="relative z-10">
                    <LiveStatusBadge status={app.status} quiet />
                </span>
            </div>
            <div className="mt-auto flex items-center justify-between gap-3 border-t px-4 py-2.5 text-xs text-muted-foreground">
                {url ? (
                    <a
                        href={url}
                        target="_blank"
                        rel="noreferrer"
                        className="relative z-10 flex min-w-0 items-center gap-1.5 hover:text-foreground"
                    >
                        <GlobeIcon className="size-3.5 shrink-0" />
                        <span className="truncate">{app.domain}</span>
                    </a>
                ) : (
                    <span>No domain</span>
                )}
                {app.last_deployment ? (
                    <span
                        className="flex shrink-0 items-center gap-2"
                        title={formatDateTime(app.last_deployment.created_at)}
                    >
                        {app.last_deployment.status !== 'succeeded' && (
                            <DeploymentStatusBadge status={app.last_deployment.status} />
                        )}
                        {timeAgo(app.last_deployment.created_at)}
                    </span>
                ) : (
                    <span className="shrink-0">Never deployed</span>
                )}
            </div>
        </article>
    );
}

// The server's headline figures from the newest reading, linking to the
// Server page. Memory and the root disk turn amber from 75% and red from 90%.
function ServerStrip({ reading }: { reading: Reading }) {
    const host = reading.host;
    const memory = memoryPercent(host);
    const disk = rootDisk(reading);
    const diskUsed = disk ? diskPercent(disk) : null;
    const levelled = (percent: number | null) =>
        percent == null ? (
            '—'
        ) : (
            <span
                className={cn(
                    meterLevel(percent) === 'critical' && 'text-destructive',
                    meterLevel(percent) === 'warning' && 'text-warning',
                )}
            >
                {formatPercent(percent)}
            </span>
        );

    return (
        <section className="mb-6" aria-label="Server">
            <div className="mb-2 flex items-center justify-between">
                <h2 className="text-sm font-medium">Server</h2>
                <Link
                    href="/server"
                    className="flex items-center gap-0.5 text-sm text-muted-foreground hover:text-foreground"
                >
                    Details
                    <ChevronRightIcon className="size-4" />
                </Link>
            </div>
            <Stats
                items={[
                    { label: 'CPU', value: host.cpu_percent != null ? formatPercent(host.cpu_percent) : '—' },
                    {
                        label: 'Memory',
                        value: levelled(memory),
                        hint:
                            host.memory_total_bytes != null ? `of ${formatBytes(host.memory_total_bytes)}` : undefined,
                    },
                    {
                        label: disk && disk.mount !== '/' ? `Disk ${disk.mount}` : 'Root disk',
                        value: levelled(diskUsed),
                        hint: disk ? `of ${formatBytes(disk.used_bytes + disk.available_bytes)}` : undefined,
                    },
                    {
                        label: 'Load',
                        value: host.load_1m != null ? host.load_1m.toFixed(2) : '—',
                        hint:
                            host.cpu_count != null
                                ? `${host.cpu_count} ${host.cpu_count === 1 ? 'core' : 'cores'}`
                                : undefined,
                    },
                ]}
            />
        </section>
    );
}
