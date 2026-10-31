import { Head, Link, usePoll } from '@inertiajs/react';
import { BoxIcon, ChevronRightIcon, GlobeIcon, PlusIcon } from 'lucide-react';

import { Page } from '@/Components/Page';
import { RecentActivity } from '@/Components/RecentActivity';
import { SectionLabel } from '@/Components/SectionLabel';
import { ServerSidebar } from '@/Components/ServerSidebar';
import { Sparkline } from '@/Components/Sparkline';
import { DeploymentStatusBadge, LiveStatusBadge, LiveStatusDot } from '@/Components/StatusBadge';
import { buttonVariants } from '@/Components/ui/button';
import { formatBytes, formatPercent } from '@/lib/format';
import { templateIcon } from '@/lib/templates';
import { formatDateTime, timeAgo } from '@/lib/time';
import { cn } from '@/lib/utils';
import type { AppsCPU, Point, Reading } from '@/types/metrics';
import type { Activity, AppSummary, ServerFacts, ServerMaintenance } from '@/types/models';

type AppReading = Reading['apps'][number];

// A group is an app and everything linked to it: its databases (ADR 0028)
// and services (ADR 0037), shown as one.
interface AppGroup {
    app: AppSummary;
    members: AppSummary[];
}

// What a row shows live: the newest reading, and the last hour of CPU.
interface Live {
    reading: AppReading | undefined;
    cpu: Point[];
    window: AppsCPU | null;
}

type LiveOf = (app: AppSummary) => Live;

// The columns every row shares, so a group's rows line up under the list's
// heading: name, last hour, CPU, memory, status, then room for the chevron.
// Placeholder until step 28 sends maintenance as a prop: a restart three
// days overdue and four security updates, so both states show.
const placeholderMaintenance: ServerMaintenance = {
    restart_required_since: new Date(Date.now() - 3 * 86400_000).toISOString(),
    security_updates: 4,
    failed_services: [],
    os_support_ends: '2029-05-31',
    clock_synced: true,
};

const columns =
    'grid grid-cols-[minmax(0,1fr)_1rem_1rem] items-center gap-x-3 sm:grid-cols-[minmax(0,1fr)_4rem_5rem_1rem_1rem] md:grid-cols-[minmax(0,1fr)_6rem_4rem_5rem_1rem_1rem]';

export default function Dashboard({
    apps,
    metrics,
    cpu,
    activity,
    server,
}: {
    apps: AppSummary[];
    metrics: Reading | null;
    cpu: AppsCPU | null;
    activity: Activity[];
    server: ServerFacts;
}) {
    // Echowa polls Swarm every 30 seconds and reads the server every 15; follow along.
    usePoll(30000, { only: ['apps', 'activity'] });
    usePoll(15000, { only: ['metrics', 'cpu'] });

    const readings = new Map((metrics?.apps ?? []).map((reading) => [reading.app, reading]));
    const live: LiveOf = (app) => ({
        reading: readings.get(app.name),
        cpu: cpu?.apps[app.id] ?? [],
        window: cpu,
    });
    const groups = groupApps(apps);

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
                sidebar={
                    <ServerSidebar facts={server} reading={metrics} apps={apps} maintenance={placeholderMaintenance} />
                }
                className="mx-0 min-[1700px]:max-w-none"
            >
                {/* Very wide screens put the activity beside the apps rather than under them. */}
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
                    <div className="space-y-10 min-[1700px]:grid min-[1700px]:grid-cols-[minmax(0,1fr)_34rem] min-[1700px]:items-start min-[1700px]:gap-12 min-[1700px]:space-y-0">
                        <section aria-label="Apps">
                            <div className={cn(columns, 'mb-2 px-4')}>
                                <SectionLabel>Apps</SectionLabel>
                                <SectionLabel className="hidden text-right md:block">Last hour</SectionLabel>
                                <SectionLabel className="hidden text-right sm:block">CPU</SectionLabel>
                                <SectionLabel className="hidden text-right sm:block">Memory</SectionLabel>
                            </div>
                            <ul className="space-y-3">
                                {groups.map((group) => (
                                    <li key={group.app.id}>
                                        <Group group={group} live={live} />
                                    </li>
                                ))}
                            </ul>
                        </section>
                        <section aria-label="Recent activity">
                            <SectionLabel className="mb-2 block px-4">Recent activity</SectionLabel>
                            <RecentActivity activity={activity} apps={apps} />
                        </section>
                    </div>
                )}
            </Page>
        </>
    );
}

// A linked database or service sits in the group of the app that uses it,
// not in one of its own. Services are one level deep, so a service's own
// databases join its app's group.
function groupApps(apps: AppSummary[]): AppGroup[] {
    const byId = new Map(apps.map((app) => [app.id, app]));
    const linked = new Set(apps.flatMap((app) => app.links));

    return apps
        .filter((app) => !linked.has(app.id))
        .map((app) => {
            const ids = new Set(app.links.flatMap((id) => [id, ...(byId.get(id)?.links ?? [])]));
            ids.delete(app.id);
            return { app, members: [...ids].flatMap((id) => byId.get(id) ?? []) };
        });
}

function Group({ group, live }: { group: AppGroup; live: LiveOf }) {
    const { app, members } = group;
    // The border says whether something is down, not whether a deploy failed:
    // the old version still serves then, and the pill says so.
    const down = [app, ...members].some((a) => a.status.unhealthy_since || a.status.state === 'down');
    const degraded = [app, ...members].some((a) => a.status.state === 'degraded');

    return (
        <article
            className={cn(
                'overflow-hidden rounded-xl border bg-card transition-colors',
                down ? 'border-destructive/50' : degraded && 'border-warning/50',
            )}
        >
            <Lead app={app} live={live(app)} />
            {members.length > 0 && (
                <ul aria-label={`${app.name}'s databases and services`} className="pb-1.5">
                    {members.map((member, i) => (
                        <li key={member.id}>
                            <Member app={member} live={live(member)} first={i === 0} last={i === members.length - 1} />
                        </li>
                    ))}
                </ul>
            )}
        </article>
    );
}

// The group's own app: the whole row opens it, and the domain inside it
// opens the site.
function Lead({ app, live }: { app: AppSummary; live: Live }) {
    const Icon = templateIcon(app.template);
    const url = app.domain ? `${app.tls ? 'https' : 'http'}://${app.domain}` : null;
    const deploy = app.last_deployment;

    return (
        <div className={cn(columns, 'group/lead relative px-4 py-3.5 transition-colors hover:bg-muted/40')}>
            <div className="flex min-w-0 items-center gap-3">
                <span className="flex size-9 shrink-0 items-center justify-center rounded-lg border bg-muted/50">
                    <Icon className="size-4 text-muted-foreground" />
                </span>
                <div className="min-w-0">
                    <div className="flex min-w-0 items-center gap-2">
                        <h3 className="truncate font-medium">
                            <Link
                                href={`/apps/${app.id}`}
                                className="outline-none after:absolute after:inset-0 focus-visible:after:ring-2 focus-visible:after:ring-ring focus-visible:after:ring-inset"
                            >
                                {app.name}
                            </Link>
                        </h3>
                        <Problems app={app} />
                    </div>
                    <p className="flex min-w-0 items-center gap-x-2 text-xs text-muted-foreground">
                        <span className="truncate font-mono">{app.repository}</span>
                        {url && (
                            <>
                                <span aria-hidden className="hidden text-muted-foreground/50 sm:inline">
                                    ·
                                </span>
                                <a
                                    href={url}
                                    target="_blank"
                                    rel="noreferrer"
                                    className="relative z-10 hidden min-w-0 items-center gap-1 hover:text-foreground sm:flex"
                                >
                                    <GlobeIcon className="size-3 shrink-0" />
                                    <span className="truncate">{app.domain}</span>
                                </a>
                            </>
                        )}
                        <span aria-hidden className="hidden text-muted-foreground/50 sm:inline">
                            ·
                        </span>
                        <span
                            className="hidden shrink-0 sm:inline"
                            title={deploy ? formatDateTime(deploy.created_at) : undefined}
                        >
                            {deploy ? timeAgo(deploy.created_at) : 'Never deployed'}
                        </span>
                    </p>
                </div>
            </div>
            <Figures live={live} />
            <span className="relative z-10 flex justify-center">
                <LiveStatusDot status={app.status} />
            </span>
            <ChevronRightIcon className="size-4 text-muted-foreground/60 transition-transform group-hover/lead:translate-x-0.5 group-hover/lead:text-muted-foreground" />
        </div>
    );
}

// A database or service in the group, hung from the app's icon by a line.
function Member({ app, live, first, last }: { app: AppSummary; live: Live; first: boolean; last: boolean }) {
    const Icon = templateIcon(app.template);

    return (
        <Link
            href={`/apps/${app.id}`}
            className={cn(
                columns,
                'group/member relative px-4 py-2 text-sm transition-colors hover:bg-muted/40 focus-visible:bg-muted/40 focus-visible:outline-none',
            )}
        >
            <span className="flex min-w-0 items-center gap-3">
                {/* The branch: down from the app's icon, then across to this one. */}
                <span aria-hidden className="relative w-9 shrink-0 self-stretch">
                    <span
                        className={cn(
                            'absolute left-[1.125rem] w-3 rounded-bl-md border-b border-l border-foreground/15',
                            first ? 'top-[-1.25rem] h-[calc(50%+1.25rem)]' : 'top-[-0.5rem] h-[calc(50%+0.5rem)]',
                        )}
                    />
                    {!last && (
                        <span className="absolute top-1/2 bottom-[-0.5rem] left-[1.125rem] border-l border-foreground/15" />
                    )}
                </span>
                <span className="flex size-6 shrink-0 items-center justify-center">
                    <Icon className="size-3.5 text-muted-foreground" />
                </span>
                <span className="min-w-0 truncate font-medium">{app.name}</span>
                <span className="hidden min-w-0 truncate font-mono text-xs text-muted-foreground sm:inline">
                    {app.repository}
                </span>
                <Problems app={app} />
            </span>
            <Figures live={live} />
            <span className="flex justify-center">
                <LiveStatusDot status={app.status} />
            </span>
            <ChevronRightIcon className="size-4 text-muted-foreground/60 transition-transform group-hover/member:translate-x-0.5 group-hover/member:text-muted-foreground" />
        </Link>
    );
}

// Only states worth reading get words: anything but running, and a
// deployment that didn't succeed.
function Problems({ app }: { app: AppSummary }) {
    const deploy = app.last_deployment;
    const settled = app.status.state === 'running' && !app.status.unhealthy_since;

    return (
        <>
            {!settled && (
                <span className="relative z-10">
                    <LiveStatusBadge status={app.status} />
                </span>
            )}
            {deploy && deploy.status !== 'succeeded' && (
                <span className="relative z-10">
                    <DeploymentStatusBadge status={deploy.status} />
                </span>
            )}
        </>
    );
}

function Figures({ live }: { live: Live }) {
    const { reading, cpu: points, window } = live;
    const cpu = reading?.cpu_percent;
    const memory = reading?.memory_used_bytes;
    const limit = reading?.memory_limit_bytes;
    const none = <span className="text-muted-foreground/50">—</span>;
    // A floor of 10% of a core, so an idle app draws a flat line rather than
    // its noise scaled up to look busy.
    const peak = Math.max(0, ...points.map((p) => p.max ?? p.avg ?? 0));

    return (
        <>
            <span className="hidden md:block">
                {window && points.length > 0 && (
                    <Sparkline
                        points={points}
                        from={window.from}
                        to={window.to}
                        step={window.step_seconds}
                        max={Math.max(10, peak * 1.25)}
                        className="h-6"
                    />
                )}
            </span>
            <span
                className="hidden text-right font-mono text-xs tabular-nums sm:block"
                title={cpu != null ? `${formatPercent(cpu)} of one core` : undefined}
            >
                {cpu != null ? formatPercent(cpu) : none}
            </span>
            <span
                className="hidden text-right font-mono text-xs tabular-nums sm:block"
                title={memory != null && limit ? `${formatBytes(memory)} of ${formatBytes(limit)}` : undefined}
            >
                {memory != null ? formatBytes(memory) : none}
            </span>
        </>
    );
}
