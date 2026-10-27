import { Head, router } from '@inertiajs/react';
import { CircleAlertIcon } from 'lucide-react';
import { useMemo, useRef, useState } from 'react';
import { AppNavAside, AppNavTabs } from '@/Components/AppNav';
import { type LogEntry, LogView } from '@/Components/LogView';
import { Page } from '@/Components/Page';
import { Panel, Stats } from '@/Components/Panel';
import { RedeployButton } from '@/Components/RedeployButton';
import { CurrentBadge, DeploymentStatusBadge } from '@/Components/StatusBadge';
import { StreamIndicator } from '@/Components/StreamIndicator';
import { Alert, AlertDescription, AlertTitle } from '@/Components/ui/alert';
import { useEventStream } from '@/lib/eventStream';
import { duration, formatDateTime, timeAgo } from '@/lib/time';
import { type Deployment, runningStatuses } from '@/types/models';

interface Props {
    app: { id: number; name: string; current_deployment_id: number | null; template: string | null };
    serviceName: string;
    deployment: Deployment;
}

// services.DeploymentStatusEvent
type StatusEvent = Pick<Deployment, 'status' | 'error' | 'digest' | 'started_at' | 'finished_at'>;

function toEntries(log: string): LogEntry[] {
    return log
        .split('\n')
        .filter((line, i, all) => line !== '' || i < all.length - 1)
        .map((text, key) => ({ key, text }));
}

export default function DeploymentShow({ app, serviceName, deployment: loaded }: Props) {
    // While the deployment runs, its log and status come live from the
    // deployment stream. Once it finishes, the page reloads its props.
    const [live, setLive] = useState<StatusEvent | null>(null);
    const [liveLines, setLiveLines] = useState<LogEntry[]>([]);
    const nextKey = useRef(0);
    const running = runningStatuses.includes(loaded.status);

    const stream = useEventStream(running ? `/deployments/${loaded.id}/stream` : null, {
        line: (data) => {
            setLiveLines((lines) => [...lines, { key: nextKey.current++, text: data as string }]);
        },
        status: (data) => {
            const status = data as StatusEvent;
            setLive(status);
            if (!runningStatuses.includes(status.status)) {
                stream.close();
                router.reload({ only: ['deployment', 'app'] });
            }
        },
    });

    const deployment: Deployment = running && live ? { ...loaded, ...live } : loaded;
    const following = running && stream.state !== 'ended';
    const staticLines = useMemo(() => toEntries(loaded.log), [loaded.log]);
    const lines = running ? liveLines : staticLines;
    const stillRunning = runningStatuses.includes(deployment.status);

    const shortRef = deployment.ref.startsWith('sha256:') ? deployment.ref.slice(0, 19) : deployment.ref;

    return (
        <>
            <Head title={`Deployment #${deployment.id} · ${app.name}`} />
            <Page
                crumbs={[
                    { label: 'Apps', href: '/dashboard' },
                    { label: app.name, href: `/apps/${app.id}` },
                    { label: 'Deployments', href: `/apps/${app.id}?tab=deployments` },
                    { label: `#${deployment.id}` },
                ]}
                phoneTitle={`${app.name} #${deployment.id}`}
                status={
                    <>
                        <DeploymentStatusBadge status={deployment.status} />
                        {deployment.id === app.current_deployment_id && <CurrentBadge />}
                        {following && <StreamIndicator state={stream.state} />}
                    </>
                }
                aside={<AppNavAside app={app} current="deployments" subtitle={serviceName} />}
                tabs={<AppNavTabs app={app} current="deployments" />}
                actions={<RedeployButton appId={app.id} deployment={deployment} />}
            >
                <div className="space-y-6">
                    <Stats
                        items={[
                            { label: 'Requested', value: <span className="font-mono text-base">{shortRef}</span> },
                            { label: 'Trigger', value: <span className="capitalize">{deployment.trigger}</span> },
                            {
                                label: 'Started',
                                value: deployment.started_at ? timeAgo(deployment.started_at) : 'Queued',
                                hint: formatDateTime(deployment.started_at) || 'Waiting for a worker',
                            },
                            {
                                label: 'Took',
                                value: deployment.started_at
                                    ? duration(deployment.started_at, deployment.finished_at)
                                    : '—',
                            },
                        ]}
                    />

                    {deployment.error && (
                        <Alert variant="destructive">
                            <CircleAlertIcon />
                            <AlertTitle>{deployment.status === 'rolled_back' ? 'Rolled back' : 'Failed'}</AlertTitle>
                            <AlertDescription className="break-words">{deployment.error}</AlertDescription>
                        </Alert>
                    )}

                    <Panel
                        title="Log"
                        description={
                            deployment.digest ? (
                                <span className="font-mono text-xs break-all">{deployment.digest}</span>
                            ) : undefined
                        }
                    >
                        <LogView lines={lines} fitTo={512} empty={stillRunning ? 'Waiting for a worker…' : 'No log.'} />
                    </Panel>
                </div>
            </Page>
        </>
    );
}
