import { Head, Link, router, usePoll } from '@inertiajs/react';
import { ExternalLinkIcon } from 'lucide-react';
import { Fragment, type MouseEvent, type ReactNode, useEffect, useState } from 'react';

import { AppBackups, BackupAlert, LastBackup } from '@/Components/AppBackups';
import { AppDomains } from '@/Components/AppDomains';
import { AppLogs } from '@/Components/AppLogs';
import { AppMetrics } from '@/Components/AppMetrics';
import { AppNavAside, AppNavTabs, type AppSection, appSections, sectionsFor } from '@/Components/AppNav';
import { ConfigPanel } from '@/Components/ConfigPanel';
import { ConfirmDelete } from '@/Components/ConfirmDelete';
import { ConnectSection, useConnectSecrets } from '@/Components/ConnectSection';
import { DatabaseGlance, DatabaseMetrics, DatabaseWarnings } from '@/Components/DatabaseMetrics';
import { DeployDialog } from '@/Components/DeployDialog';
import { DeploymentsTable } from '@/Components/DeploymentsTable';
import { DockerConfig } from '@/Components/DockerConfig';
import { EnvEditor } from '@/Components/EnvEditor';
import { ExternalAccessSection } from '@/Components/ExternalAccessSection';
import { LinkedDatabases } from '@/Components/LinkedDatabases';
import { Page } from '@/Components/Page';
import { Panel, Stats } from '@/Components/Panel';
import { PoolingSection } from '@/Components/PoolingSection';
import { OOMAlert, ResourceLimits } from '@/Components/ResourceLimits';
import { ScheduledTaskAlert, ScheduledTasks } from '@/Components/ScheduledTasks';
import { DeploymentStatusBadge, LiveStatusBadge } from '@/Components/StatusBadge';
import { Button, buttonVariants } from '@/Components/ui/button';
import { formatDateTime, timeAgo } from '@/lib/time';
import { cn } from '@/lib/utils';
import type { AppLive, AppSeries, DatabaseLive, DatabaseSeries } from '@/types/metrics';
import {
    type App,
    type AppVolume,
    type AppWork,
    type BackupsSection,
    type ConnectInfo,
    type DatabaseChoices,
    type DeploymentSummary,
    type DomainStatus,
    type LinkedDatabase,
    type LiveStatus,
    type LogDeploy,
    type PoolerInfo,
    type RegistryOption,
    type ResourcesInfo,
    runningStatuses,
    type ScheduledTask,
    type SlowQueryResult,
    type TaskFailure,
    type TemplateOption,
} from '@/types/models';

interface Props {
    app: App;
    serviceName: string;
    status: LiveStatus;
    lastDeployment: DeploymentSummary | null;
    deployments: DeploymentSummary[];
    envKeys: string[];
    registries: RegistryOption[];
    // The Resource limits section's; null when Docker doesn't answer.
    resources: ResourcesInfo | null;
    // The template the app was made from, and how other apps reach it.
    // Null for a custom app.
    template: TemplateOption | null;
    connect: ConnectInfo | null;
    // The Connection section's pooling, for a PostgreSQL app (ADR 0035).
    pooler: PoolerInfo | null;
    // The Metrics section's: range picker choices, charts and the newest
    // reading (null before the first, or while no task runs).
    ranges: string[];
    series: AppSeries;
    metrics: AppLive | null;
    // What a template database reports about itself, also in its Metrics
    // section (ADR 0027). Null for a custom app; database is also null
    // before the first check.
    databaseSeries: DatabaseSeries | null;
    database: DatabaseLive | null;
    // Read only when the Metrics section asks for them; null when the
    // database can't list them, or its switch is off.
    slowQueries?: SlowQueryResult | null;
    // The Domains section's checks of the saved domain, read only when it
    // asks; null without a domain.
    domainStatus?: DomainStatus | null;
    // The Logs section's deploy picker, read only when it asks.
    logDeploys?: LogDeploy[];
    // The Databases section's, for a custom app (ADR 0028): the databases
    // it's linked to, and what can be added. Empty and null for a template app.
    linkedDatabases: LinkedDatabase[];
    databaseChoices: DatabaseChoices | null;
    // The Scheduled tasks section's, for a custom app (ADR 0032). Empty for
    // a template app.
    scheduledTasks: ScheduledTask[];
    taskFailures: TaskFailure[];
    // The Backups section's (ADR 0034). Null when the app's template isn't
    // backed up.
    backups: BackupsSection | null;
    // What holds the app's lock (ADR 0034), so Deploy says what it would
    // wait for. Empty when nothing does.
    busy: AppWork;
}

// The section lives in ?tab= so redirects (e.g. after saving settings) and
// reloads land on it. Sections that were merged or split keep their old
// links working.
const movedSections: Record<string, AppSection> = { database: 'metrics', settings: 'docker' };

function sectionFromUrl(app: Parameters<typeof sectionsFor>[0]): AppSection {
    const param = new URLSearchParams(window.location.search).get('tab');
    const tab = (param && movedSections[param]) ?? param;
    return sectionsFor(app).some((s) => s.id === tab) ? (tab as AppSection) : 'overview';
}

const shortDigest = (digest: string) => digest.replace(/^.*sha256:/, '').slice(0, 12);

export default function AppsShow({
    app,
    serviceName,
    status,
    lastDeployment,
    deployments,
    envKeys,
    registries,
    resources,
    template,
    connect,
    pooler,
    ranges,
    series,
    metrics,
    databaseSeries,
    database,
    slowQueries,
    domainStatus,
    logDeploys,
    linkedDatabases,
    databaseChoices,
    scheduledTasks,
    taskFailures,
    backups,
    busy,
}: Props) {
    // The nav needs to know whether the template is one Echowa backs up.
    const navApp = { ...app, backups: template?.backups ?? false };
    const [section, setSection] = useState<AppSection>(() => sectionFromUrl(navApp));

    // Deploy says what it would wait for until the backup or restore ends.
    // The Backups section polls for itself.
    useEffect(() => {
        if (!busy || busy === 'deploy' || section === 'backups') {
            return;
        }
        const id = window.setInterval(() => router.reload({ only: ['busy', 'backups'] }), 5000);
        return () => window.clearInterval(id);
    }, [busy, section]);
    const volumes = app.volumes ?? [];
    // The Connection section's Connect and pooling share one fetch.
    const connectSecrets = useConnectSecrets(app.id, app.pooling);

    // While a deployment runs, refresh its status and the service's.
    const deploying = deployments.some((d) => runningStatuses.includes(d.status));
    const { start, stop } = usePoll(
        2000,
        { only: ['app', 'status', 'lastDeployment', 'deployments'] },
        { autoStart: false },
    );
    useEffect(() => {
        if (deploying) {
            start();
        } else {
            stop();
        }
    }, [deploying, start, stop]);

    const show = (value: AppSection) => {
        setSection(value);
        const url = new URL(window.location.href);
        url.searchParams.set('tab', value);
        // Keep Inertia's history state; only the address changes.
        window.history.replaceState(window.history.state, '', url);
    };

    const url = app.domain ? `${app.tls ? 'https' : 'http'}://${app.domain}` : null;
    const sectionLabel = appSections.find((s) => s.id === section)?.label ?? section;

    return (
        <>
            <Head title={app.name} />
            <Page
                crumbs={[
                    { label: 'Apps', href: '/dashboard' },
                    { label: app.name, href: `/apps/${app.id}` },
                    { label: sectionLabel },
                ]}
                phoneTitle={app.name}
                status={<LiveStatusBadge status={status} />}
                aside={
                    <AppNavAside
                        app={navApp}
                        current={section}
                        onSelect={show}
                        subtitle={`${app.repository}:${app.default_tag}`}
                    />
                }
                tabs={<AppNavTabs app={navApp} current={section} onSelect={show} />}
                actions={
                    <>
                        {url && (
                            <a
                                href={url}
                                target="_blank"
                                rel="noreferrer"
                                className={buttonVariants({ variant: 'outline' })}
                                aria-label="Open site"
                            >
                                <ExternalLinkIcon />
                                <span className="hidden sm:inline">Open</span>
                            </a>
                        )}
                        <DeployDialog app={app} busy={busy} />
                    </>
                }
            >
                <div className="space-y-6">
                    {section === 'overview' && (
                        <>
                            <OOMAlert status={status} />
                            {taskFailures.map((failure) => (
                                <ScheduledTaskAlert key={failure.task_id} app={app} failure={failure} />
                            ))}
                            {backups?.failures.map((failure) => (
                                <BackupAlert key={failure.schedule_id} app={app} failure={failure} />
                            ))}
                            {database && <DatabaseWarnings database={database} />}
                            <Stats
                                items={[
                                    {
                                        label: 'Status',
                                        value: <LiveStatusBadge status={status} />,
                                        hint: status.unhealthy_since
                                            ? `Short of replicas for ${timeAgo(status.unhealthy_since).replace(/ ago$/, '')}`
                                            : status.update
                                              ? `Update ${status.update.replace('_', ' ')}`
                                              : undefined,
                                    },
                                    {
                                        label: 'Replicas',
                                        value: status.desired ? `${status.running} / ${status.desired}` : app.replicas,
                                        hint: status.desired ? 'running / desired' : 'desired',
                                    },
                                    {
                                        label: 'Running image',
                                        value: (
                                            <span className="font-mono text-base">
                                                {status.digest ? shortDigest(status.digest) : '—'}
                                            </span>
                                        ),
                                        hint: 'digest',
                                    },
                                    {
                                        label: 'Last deploy',
                                        value: lastDeployment ? (
                                            <DeploymentStatusBadge status={lastDeployment.status} />
                                        ) : (
                                            'Never'
                                        ),
                                        hint: lastDeployment ? (
                                            <span title={formatDateTime(lastDeployment.created_at)}>
                                                {timeAgo(lastDeployment.created_at)}
                                            </span>
                                        ) : undefined,
                                    },
                                ]}
                            />
                            {template && databaseSeries && (
                                <DatabaseGlance
                                    app={app}
                                    template={template}
                                    series={series}
                                    metrics={metrics}
                                    databaseSeries={databaseSeries}
                                    database={database}
                                    onViewMetrics={() => show('metrics')}
                                />
                            )}
                            <Panel title="Details">
                                <Details>
                                    <Detail label="Internal address">
                                        <span className="font-mono break-all select-all">
                                            {serviceName}:{app.port}
                                        </span>
                                    </Detail>
                                    <Detail label="Domain">
                                        {url ? (
                                            <a href={url} target="_blank" rel="noreferrer" className="hover:underline">
                                                {url}
                                            </a>
                                        ) : (
                                            '—'
                                        )}
                                    </Detail>
                                    <Detail label="Port">{app.port}</Detail>
                                    {template && !template.http && (
                                        <Detail label="External access">
                                            <SectionLink app={app} section="connection" onSelect={show}>
                                                {app.external_port ? `On, port ${app.external_port}` : 'Off'}
                                            </SectionLink>
                                        </Detail>
                                    )}
                                    {backups && (
                                        <Detail label="Last backup">
                                            <SectionLink app={app} section="backups" onSelect={show}>
                                                <LastBackup section={backups} />
                                            </SectionLink>
                                        </Detail>
                                    )}
                                    <Detail label="Replicas">{app.replicas}</Detail>
                                    <Detail label="Image">
                                        <span className="font-mono break-all">
                                            {app.repository}:{app.default_tag}
                                        </span>
                                    </Detail>
                                    <Detail label="Environment">
                                        <SectionLink app={app} section="environment" onSelect={show}>
                                            {envKeys.length === 1 ? '1 variable' : `${envKeys.length} variables`}
                                        </SectionLink>
                                    </Detail>
                                    {databaseChoices && (
                                        <Detail label="Databases">
                                            {linkedDatabases.length > 0 ? (
                                                linkedDatabases.map((db, i) => (
                                                    <Fragment key={db.id}>
                                                        {i > 0 && ', '}
                                                        <Link href={`/apps/${db.id}`} className="hover:underline">
                                                            {db.name}
                                                        </Link>
                                                    </Fragment>
                                                ))
                                            ) : (
                                                <SectionLink app={app} section="databases" onSelect={show}>
                                                    None linked
                                                </SectionLink>
                                            )}
                                        </Detail>
                                    )}
                                </Details>
                            </Panel>
                            <Panel
                                title="Recent deployments"
                                flush
                                actions={
                                    deployments.length > 5 && (
                                        <Button variant="ghost" size="sm" onClick={() => show('deployments')}>
                                            View all
                                        </Button>
                                    )
                                }
                            >
                                <DeploymentsTable
                                    appId={app.id}
                                    deployments={deployments.slice(0, 5)}
                                    currentId={app.current_deployment_id}
                                />
                            </Panel>
                        </>
                    )}

                    {section === 'environment' && (
                        <Panel
                            title="Environment"
                            description="Variables passed to the container. Values stay hidden until you reveal one."
                        >
                            <EnvEditor key={envKeys.join('\n')} appId={app.id} envKeys={envKeys} />
                        </Panel>
                    )}

                    {section === 'databases' && databaseChoices && (
                        <LinkedDatabases app={app} databases={linkedDatabases} choices={databaseChoices} />
                    )}

                    {section === 'connection' && template && (
                        <ConfigPanel>
                            {connect && <ConnectSection connect={connect} secrets={connectSecrets} />}
                            {pooler && <PoolingSection app={app} pooler={pooler} secrets={connectSecrets} />}
                            {!template.http && <ExternalAccessSection app={app} template={template} />}
                        </ConfigPanel>
                    )}

                    {section === 'backups' && backups && <AppBackups app={app} section={backups} held={busy} />}

                    {section === 'docker' && (
                        <DockerConfig app={app} registries={registries} template={template ?? undefined} />
                    )}

                    {section === 'resources' && (
                        <ResourceLimits app={app} status={status} resources={resources} metrics={metrics} />
                    )}

                    {section === 'domains' && <AppDomains app={app} status={domainStatus} />}

                    {section === 'tasks' && <ScheduledTasks app={app} tasks={scheduledTasks} />}

                    {section === 'deployments' && (
                        <Panel
                            title="Deployments"
                            description="The last 30, newest first. Open one to see its log."
                            flush
                        >
                            <DeploymentsTable
                                appId={app.id}
                                deployments={deployments}
                                currentId={app.current_deployment_id}
                            />
                        </Panel>
                    )}

                    {section === 'logs' && (
                        <Panel
                            title="Logs"
                            description={
                                <>
                                    Output of <code className="font-mono">{serviceName}</code>, one deploy at a time:
                                    the current one live, or one of the two before it. Errors (stderr) are in red.
                                </>
                            }
                        >
                            <AppLogs appId={app.id} deploys={logDeploys} />
                        </Panel>
                    )}

                    {section === 'metrics' &&
                        (template && databaseSeries ? (
                            <DatabaseMetrics
                                app={app}
                                template={template}
                                ranges={ranges}
                                series={series}
                                metrics={metrics}
                                databaseSeries={databaseSeries}
                                database={database}
                                slowQueries={slowQueries}
                            />
                        ) : (
                            <AppMetrics ranges={ranges} series={series} live={metrics} />
                        ))}

                    {section === 'danger' && (
                        <Panel title="Delete app" tone="danger" description="This can't be undone.">
                            <div className="flex flex-wrap items-center justify-between gap-4">
                                <p className="max-w-prose text-sm text-muted-foreground">
                                    Removes the Swarm service{' '}
                                    <code className="font-mono text-foreground">{serviceName}</code>, so the app stops
                                    serving, then deletes the app, its environment and its deployment history.
                                    {volumes.length > 0 && (
                                        <>
                                            {' '}
                                            Its data in <VolumeNames volumes={volumes} className="text-foreground" /> is
                                            deleted too.
                                        </>
                                    )}
                                    {backups?.last_succeeded && (
                                        <>
                                            {' '}
                                            Its backups stay in the bucket. New app, From a backup restores one into a
                                            new app.
                                        </>
                                    )}
                                </p>
                                <ConfirmDelete
                                    title={`Delete ${app.name}?`}
                                    confirmLabel={volumes.length > 0 ? 'Delete app and data' : 'Delete app and service'}
                                    trigger={<Button variant="destructive">Delete app</Button>}
                                    onConfirm={() => router.delete(`/apps/${app.id}`)}
                                >
                                    The Swarm service <code>{serviceName}</code> is removed and the app stops serving.
                                    Its environment and deployment history are deleted.
                                    {volumes.length > 0 && (
                                        <>
                                            {' '}
                                            So is its data: <VolumeNames volumes={volumes} /> is deleted for good once
                                            the service has stopped.
                                        </>
                                    )}
                                    {backups?.last_succeeded && ' Its backups stay in the bucket.'} This can&apos;t be
                                    undone.
                                </ConfirmDelete>
                            </div>
                        </Panel>
                    )}
                </div>
            </Page>
        </>
    );
}

// VolumeNames lists volumes inline, for a sentence.
function VolumeNames({ volumes, className }: { volumes: AppVolume[]; className?: string }) {
    return volumes.map((volume, i) => (
        <Fragment key={volume.id}>
            {i > 0 && ', '}
            <code className={cn('font-mono', className)}>{volume.name}</code>
        </Fragment>
    ));
}

// Details is a label and value per row; from lg, two pairs to a row.
function Details({ children }: { children: ReactNode }) {
    return (
        <dl className="grid grid-cols-[7.5rem_minmax(0,1fr)] gap-x-4 gap-y-2.5 text-sm lg:grid-cols-[7.5rem_minmax(0,1fr)_7.5rem_minmax(0,1fr)] lg:gap-x-6">
            {children}
        </dl>
    );
}

function Detail({ label, children }: { label: string; children: ReactNode }) {
    return (
        <div className="contents">
            <dt className="text-muted-foreground">{label}</dt>
            <dd className="min-w-0 break-words">{children}</dd>
        </div>
    );
}

// SectionLink switches to another section in place, as the section nav does;
// a modified click opens it in a new tab.
function SectionLink({
    app,
    section,
    onSelect,
    children,
}: {
    app: App;
    section: AppSection;
    onSelect: (section: AppSection) => void;
    children: ReactNode;
}) {
    const view = (event: MouseEvent) => {
        if (!event.metaKey && !event.ctrlKey && !event.shiftKey) {
            event.preventDefault();
            onSelect(section);
        }
    };

    return (
        <a href={`/apps/${app.id}?tab=${section}`} onClick={view} className="hover:underline">
            {children}
        </a>
    );
}
