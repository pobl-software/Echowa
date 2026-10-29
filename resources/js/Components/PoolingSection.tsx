import { router } from '@inertiajs/react';
import { CircleAlertIcon, InfoIcon, WaypointsIcon } from 'lucide-react';
import { useEffect } from 'react';

import { ConfigSection } from '@/Components/ConfigPanel';
import { ConfirmDelete } from '@/Components/ConfirmDelete';
import { ConnectDetails, type ConnectSecrets } from '@/Components/ConnectSection';
import { LiveStatusBadge, StatusPill } from '@/Components/StatusBadge';
import { Alert, AlertDescription, AlertTitle } from '@/Components/ui/alert';
import { Button } from '@/Components/ui/button';
import type { App, PoolerInfo, PoolerStatus } from '@/types/models';

// PoolingSection runs PgBouncer beside a PostgreSQL app, in transaction
// mode (ADR 0035), in its Connection section. The pooler starts and goes in
// the background, so the section polls until it's done.
export function PoolingSection({ app, pooler, secrets }: { app: App; pooler: PoolerInfo; secrets: ConnectSecrets }) {
    const { status, connect } = pooler;
    const on = app.pooling;
    useEffect(() => {
        if (!status.syncing) {
            return;
        }
        const id = window.setInterval(() => router.reload({ only: ['app', 'pooler'] }), 2000);
        return () => window.clearInterval(id);
    }, [status.syncing]);

    return (
        <ConfigSection
            icon={WaypointsIcon}
            title="Connection pooling"
            description="Add PgBouncer between your apps and the database to reduce the amount of connections."
        >
            <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="flex min-w-0 flex-1 basis-64 items-center gap-2.5">
                    <PoolerBadge on={on} status={status} />
                    <p className="text-sm text-muted-foreground">
                        {on
                            ? 'On. Apps that connect through the pooler share connections to PostgreSQL, each held only for a transaction.'
                            : 'Off. Apps connect to PostgreSQL directly.'}
                    </p>
                </div>
                {on ? (
                    <ConfirmDelete
                        trigger={
                            <Button variant="outline" size="sm">
                                Turn off
                            </Button>
                        }
                        title="Turn off connection pooling?"
                        confirmLabel="Turn off"
                        onConfirm={() => router.delete(`/apps/${app.id}/pooling`, { preserveScroll: true })}
                    >
                        {connect.host} is removed, and apps connected through it lose their connection. Move them to the
                        direct URL first.
                    </ConfirmDelete>
                ) : (
                    <Button
                        size="sm"
                        disabled={status.syncing}
                        onClick={() => router.post(`/apps/${app.id}/pooling`, {}, { preserveScroll: true })}
                    >
                        <WaypointsIcon />
                        Enable connection pooling
                    </Button>
                )}
            </div>
            {status.error && !status.syncing && (
                <Alert variant="destructive">
                    <CircleAlertIcon />
                    <AlertTitle>{on ? 'The pooler didn’t start' : 'The pooler wasn’t removed'}</AlertTitle>
                    <AlertDescription>
                        {status.error}. Echowa tries again when it restarts, or turn pooling off and on.
                    </AlertDescription>
                </Alert>
            )}
            {on && <ConnectDetails connect={connect} secrets={secrets} url={(s) => s.pooled_url} />}
            {!on && (
                <Alert>
                    <InfoIcon />
                    <AlertTitle>Check your app before turning this on</AlertTitle>
                    <AlertDescription>
                        <p>
                            Each query may use a different connection, so features that depend on one session can break:{' '}
                            <code>LISTEN</code>, advisory locks and <code>SET</code> (use <code>SET LOCAL</code>{' '}
                            instead). Run migrations on the direct URL.
                        </p>
                        <p>
                            Laravel on PHP 8.3, or with libpq older than 17, also needs{' '}
                            <code>PDO::ATTR_EMULATE_PREPARES =&gt; true</code> in its <code>pgsql</code> connection.
                        </p>
                    </AlertDescription>
                </Alert>
            )}
        </ConfigSection>
    );
}

// PoolerBadge says what the pooler is doing: starting or going, failed, or
// its service's state.
function PoolerBadge({ on, status }: { on: boolean; status: PoolerStatus }) {
    if (status.syncing) {
        const label = !on ? 'Removing' : status.service?.state === 'running' ? 'Updating' : 'Starting';
        return (
            <StatusPill tone="warning" pulse>
                {label}
            </StatusPill>
        );
    }
    if (status.error) {
        return (
            <StatusPill tone="danger" title={status.error}>
                Failed
            </StatusPill>
        );
    }

    return status.service && <LiveStatusBadge status={status.service} />;
}
