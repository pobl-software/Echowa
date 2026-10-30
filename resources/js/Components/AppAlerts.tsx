import { Link } from '@inertiajs/react';

import { AddAlertButton, AlertRuleTable, NoAlerts, SuggestedAlerts } from '@/Components/AlertRules';
import { Panel } from '@/Components/Panel';
import { StatusPill } from '@/Components/StatusBadge';
import { buttonVariants } from '@/Components/ui/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/Components/ui/table';
import { formatDateTime, timeAgo } from '@/lib/time';
import type { NotificationStatus, NotificationSummary, AppAlerts as Section } from '@/types/models';

// AppAlerts is an app's Alerts section (ADR 0040): its own alerts, the
// every-app ones that cover it, suggestions, and what was sent.
export function AppAlerts({ app, alerts }: { app: { id: number; name: string }; alerts: Section }) {
    const base = `/apps/${app.id}/alerts`;
    const ready = alerts.providers.length > 0;

    if (!ready) {
        return (
            <Panel title="Alerts">
                <NoAlerts
                    action={
                        <Link href="/settings" className={buttonVariants()}>
                            Go to Settings
                        </Link>
                    }
                >
                    Add a notification provider on Settings first: Slack, Discord, Teams, ntfy or email. Then choose
                    which of {app.name}’s alerts go there.
                </NoAlerts>
            </Panel>
        );
    }

    return (
        <div className="space-y-6">
            <Panel
                flush={alerts.rules.length > 0}
                title="Alerts"
                description={`Sent when something happens to ${app.name}.`}
                actions={
                    alerts.rules.length > 0 && (
                        <AddAlertButton base={base} triggers={alerts.triggers} providers={alerts.providers} />
                    )
                }
            >
                {alerts.rules.length > 0 ? (
                    <AlertRuleTable
                        rules={alerts.rules}
                        triggers={alerts.triggers}
                        providers={alerts.providers}
                        base={base}
                    />
                ) : (
                    <NoAlerts
                        action={<AddAlertButton base={base} triggers={alerts.triggers} providers={alerts.providers} />}
                    >
                        {alerts.every_app.length > 0
                            ? `No alerts of its own. The every-app alerts below cover ${app.name}.`
                            : `No alerts for ${app.name} yet.`}
                    </NoAlerts>
                )}
            </Panel>

            {alerts.suggestions.length > 0 && (
                <SuggestedAlerts
                    key={alerts.suggestions.map((s) => s.id).join()}
                    suggestions={alerts.suggestions}
                    providers={alerts.providers}
                    base={base}
                    description={`Alerts that usually matter for an app like ${app.name}. Choose where they go, then add them in one go.`}
                />
            )}

            {alerts.every_app.length > 0 && (
                <Panel
                    flush
                    title="Every app"
                    description={`Alerts on Settings that cover every app, ${app.name} included.`}
                >
                    <AlertRuleTable rules={alerts.every_app} triggers={alerts.triggers} providers={alerts.providers} />
                </Panel>
            )}

            <Panel
                flush={alerts.recent.length > 0}
                title="Recent notifications"
                description={`The last 50 sent about ${app.name}, newest first.`}
            >
                {alerts.recent.length > 0 ? (
                    <RecentNotifications notifications={alerts.recent} />
                ) : (
                    <p className="text-sm text-muted-foreground">Nothing sent yet.</p>
                )}
            </Panel>
        </div>
    );
}

const statuses: Record<NotificationStatus, { label: string; tone: 'success' | 'danger' | 'neutral' | 'primary' }> = {
    sent: { label: 'Sent', tone: 'success' },
    failed: { label: 'Failed', tone: 'danger' },
    // The storm guard kept it (ADR 0040).
    suppressed: { label: 'Held back', tone: 'neutral' },
    pending: { label: 'Sending', tone: 'primary' },
};

function RecentNotifications({ notifications }: { notifications: NotificationSummary[] }) {
    return (
        <Table>
            <TableHeader>
                <TableRow>
                    <TableHead>Notification</TableHead>
                    <TableHead className="hidden md:table-cell">Sent to</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead className="hidden text-right sm:table-cell">When</TableHead>
                </TableRow>
            </TableHeader>
            <TableBody>
                {notifications.map((n) => {
                    const status = statuses[n.status];
                    return (
                        <TableRow key={n.id}>
                            <TableCell className="w-full max-w-0">
                                <div className="truncate font-medium">{n.title}</div>
                                {n.summary && (
                                    <div className="truncate text-xs text-muted-foreground" title={n.summary}>
                                        {n.summary}
                                    </div>
                                )}
                                {n.error && (
                                    <div className="text-xs break-words text-destructive">
                                        {n.attempts > 1 ? `After ${n.attempts} tries: ` : ''}
                                        {n.error}
                                    </div>
                                )}
                                <div className="truncate text-xs text-muted-foreground md:hidden">
                                    {n.provider.name}
                                    <span className="sm:hidden"> · {timeAgo(n.created_at)}</span>
                                </div>
                            </TableCell>
                            <TableCell className="hidden max-w-48 truncate md:table-cell">{n.provider.name}</TableCell>
                            <TableCell>
                                <StatusPill tone={status.tone} pulse={n.status === 'pending'}>
                                    {status.label}
                                </StatusPill>
                            </TableCell>
                            <TableCell
                                className="hidden text-right whitespace-nowrap text-muted-foreground sm:table-cell"
                                title={formatDateTime(n.created_at)}
                            >
                                {timeAgo(n.created_at)}
                            </TableCell>
                        </TableRow>
                    );
                })}
            </TableBody>
        </Table>
    );
}
