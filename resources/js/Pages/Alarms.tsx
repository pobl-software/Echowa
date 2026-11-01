import { InfiniteScroll, Link, router, usePage } from '@inertiajs/react';
import { CheckCheckIcon } from 'lucide-react';

import { FiringAlerts, NoAlerts, SeverityPill } from '@/Components/AlertRules';
import { Page } from '@/Components/Page';
import { Panel } from '@/Components/Panel';
import { StatusPill } from '@/Components/StatusBadge';
import { Button, buttonVariants } from '@/Components/ui/button';
import { Tabs, TabsList, TabsTrigger } from '@/Components/ui/tabs';
import { formatDateTime, timeAgo } from '@/lib/time';
import { cn } from '@/lib/utils';
import type { AlarmSummary, FiringAlert } from '@/types/models';

type Filter = 'all' | 'unread';

// Alarms is every time an alert went off in the last 30 days, newest first,
// whether or not a provider was sent it, with what's firing now above.
export default function Alarms({
    filter,
    alarms,
    firing,
}: {
    filter: Filter;
    alarms: { data: AlarmSummary[] };
    firing: FiringAlert[];
}) {
    const { unreadAlarms } = usePage().props;
    const newest = alarms.data[0]?.id;

    // Full visits replace the scrolled list. Inertia's `reset` would reload
    // only the list, leaving the filter and unread count stale.
    const show = (next: Filter) => router.get('/alarms', next === 'unread' ? { filter: next } : {});

    return (
        <Page
            crumbs={[{ label: 'Alarms' }]}
            actions={
                <Button
                    variant="outline"
                    size="sm"
                    disabled={unreadAlarms === 0 || newest === undefined}
                    onClick={() => router.post('/alarms/read', { through: newest }, { preserveScroll: true })}
                >
                    <CheckCheckIcon />
                    <span className="hidden sm:inline">Mark all as read</span>
                    <span className="sm:hidden">Read all</span>
                </Button>
            }
        >
            <div className="space-y-6">
                {firing.length > 0 && <FiringAlerts firing={firing} subjects />}

                <Panel
                    flush
                    title="Alarms"
                    description="Every time an alert went off in the last 30 days, newest first."
                    actions={
                        <Tabs value={filter} onValueChange={(value) => show(value as Filter)}>
                            <TabsList aria-label="Show">
                                <TabsTrigger value="all" className="px-2.5">
                                    All
                                </TabsTrigger>
                                <TabsTrigger value="unread" className="px-2.5">
                                    Unread
                                    {unreadAlarms > 0 && (
                                        <span className="ml-1 font-mono text-xs text-muted-foreground tabular-nums">
                                            {unreadAlarms}
                                        </span>
                                    )}
                                </TabsTrigger>
                            </TabsList>
                        </Tabs>
                    }
                >
                    {alarms.data.length > 0 ? (
                        <InfiniteScroll data="alarms" as="ul" className="divide-y" preserveUrl onlyNext>
                            {alarms.data.map((alarm) => (
                                <AlarmRow key={alarm.id} alarm={alarm} />
                            ))}
                        </InfiniteScroll>
                    ) : (
                        <div className="p-4 sm:p-5">
                            <NoAlerts
                                action={
                                    filter === 'all' && (
                                        <Link
                                            href="/settings/alerts"
                                            className={buttonVariants({ variant: 'outline' })}
                                        >
                                            Set up alerts
                                        </Link>
                                    )
                                }
                            >
                                {filter === 'unread'
                                    ? 'Nothing unread. You’ve seen every alarm.'
                                    : 'No alarms in the last 30 days. They show here whenever an alert goes off.'}
                            </NoAlerts>
                        </div>
                    )}
                </Panel>
            </div>
        </Page>
    );
}

// AlarmRow opens the page the alarm is about, marking it read on the way.
function AlarmRow({ alarm }: { alarm: AlarmSummary }) {
    return (
        <li>
            <Link
                href={`/alarms/${alarm.id}/read`}
                method="post"
                as="button"
                className={cn(
                    'flex w-full items-start gap-3 px-4 py-3 text-left transition-colors hover:bg-accent/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset sm:px-5',
                    !alarm.read && 'bg-primary/[0.03]',
                )}
            >
                <span
                    className={cn('mt-1.5 size-2 shrink-0 rounded-full', alarm.read ? 'bg-transparent' : 'bg-primary')}
                    aria-hidden
                />
                <span className="min-w-0 flex-1 text-sm">
                    <span className={cn('block truncate', alarm.read ? 'text-muted-foreground' : 'font-medium')}>
                        {!alarm.read && <span className="sr-only">Unread: </span>}
                        {alarm.title}
                    </span>
                    {alarm.summary && (
                        <span className="block text-xs text-muted-foreground sm:truncate" title={alarm.summary}>
                            {alarm.summary}
                        </span>
                    )}
                    <span className="block text-xs text-muted-foreground sm:hidden">{timeAgo(alarm.created_at)}</span>
                </span>
                <span className="flex shrink-0 items-center gap-3">
                    {alarm.severity === 'resolved' ? (
                        <StatusPill tone="success" plain>
                            Resolved
                        </StatusPill>
                    ) : (
                        <SeverityPill severity={alarm.severity} />
                    )}
                    <span
                        className="hidden w-24 text-right text-xs whitespace-nowrap text-muted-foreground sm:block"
                        title={formatDateTime(alarm.created_at)}
                    >
                        {timeAgo(alarm.created_at)}
                    </span>
                </span>
            </Link>
        </li>
    );
}
