import { Link } from '@inertiajs/react';

import type { Tone } from '@/Components/StatusBadge';
import { formatDateTime, parseServerTime, timeAgo } from '@/lib/time';
import { cn } from '@/lib/utils';
import type { Activity, AppSummary } from '@/types/models';

const tones: Record<string, Tone> = {
    succeeded: 'success',
    failed: 'danger',
    rolled_back: 'danger',
    timed_out: 'danger',
    queued: 'neutral',
    pulling: 'warning',
    deploying: 'warning',
    running: 'warning',
    interrupted: 'neutral',
};

const text: Record<Tone, string> = {
    success: 'text-success',
    danger: 'text-destructive',
    warning: 'text-warning',
    neutral: 'text-muted-foreground',
    primary: 'text-primary',
};

// RecentActivity is the newest deploys, backups, restores and scheduled
// task runs across every app, as a log: the time, how it went, the app and
// what it was. A task's runs show only when they didn't succeed or were
// started by hand, so an every-minute task doesn't fill it. It fits its own width,
// not the screen's: beside the apps it drops the clock, and narrower still
// the status word for a dot.
export function RecentActivity({ activity, apps }: { activity: Activity[]; apps: AppSummary[] }) {
    const byId = new Map(apps.map((app) => [app.id, app]));
    const rows = activity.flatMap((item) => {
        const app = byId.get(item.app_id);
        return app ? [{ item, app }] : [];
    });

    if (rows.length === 0) {
        return (
            <p className="rounded-xl border border-dashed px-4 py-6 text-center text-sm text-muted-foreground">
                Nothing yet. Deploys, backups and failed tasks show here.
            </p>
        );
    }

    return (
        <ul className="@container divide-y overflow-hidden rounded-xl border bg-card">
            {rows.map(({ item, app }) => {
                const tone = tones[item.status] ?? 'neutral';
                return (
                    <li key={`${item.kind}-${item.id}`}>
                        <Link
                            href={href(item)}
                            className="grid grid-cols-[minmax(0,1fr)_auto] items-baseline gap-x-4 px-4 py-2.5 text-sm transition-colors hover:bg-muted/40 @md:grid-cols-[6.5rem_minmax(0,1fr)_auto] @2xl:grid-cols-[3.5rem_6.5rem_minmax(0,1fr)_auto]"
                        >
                            <time
                                dateTime={item.created_at}
                                className="hidden font-mono text-xs text-muted-foreground tabular-nums @2xl:block"
                            >
                                {clock(item.created_at)}
                            </time>
                            <span className={cn('hidden font-mono text-xs @md:block', text[tone])}>
                                {item.status.replace('_', ' ')}
                            </span>
                            <span className="min-w-0 truncate">
                                <span
                                    className={cn('mr-2 inline-block size-1.5 rounded-full align-middle @md:hidden', {
                                        'bg-success': tone === 'success',
                                        'bg-destructive': tone === 'danger',
                                        'bg-warning': tone === 'warning',
                                        'bg-muted-foreground/60': tone === 'neutral',
                                    })}
                                />
                                <span className="font-medium">{app.name}</span>
                                <span className="text-muted-foreground"> {describe(item)}</span>
                            </span>
                            <span
                                className="text-xs text-muted-foreground whitespace-nowrap"
                                title={formatDateTime(item.created_at)}
                            >
                                {timeAgo(item.created_at)}
                            </span>
                        </Link>
                    </li>
                );
            })}
        </ul>
    );
}

function describe(item: Activity): string {
    switch (item.kind) {
        case 'deployment': {
            const ref = short(item.ref);
            const from = item.trigger === 'webhook' ? ' from CI' : '';
            if (item.trigger === 'rollback') {
                return item.status === 'succeeded' ? `rolled back to ${ref}` : `roll back to ${ref}`;
            }
            switch (item.status) {
                case 'succeeded':
                    return `deployed ${ref}${from}`;
                case 'failed':
                case 'rolled_back':
                    return `couldn't deploy ${ref}${from}`;
                case 'queued':
                    return `queued ${ref}${from}`;
                default:
                    return `deploying ${ref}${from}`;
            }
        }
        case 'backup':
            return item.trigger === 'before_restore'
                ? 'safety backup'
                : item.trigger === 'schedule'
                  ? 'scheduled backup'
                  : 'backup';
        case 'restore':
            return 'restore';
        case 'task':
            return `task ${item.name}${item.trigger === 'manual' ? ', run by hand' : ''}`;
    }
}

function href(item: Activity): string {
    switch (item.kind) {
        case 'deployment':
            return `/apps/${item.app_id}/deployments/${item.id}`;
        case 'backup':
        case 'restore':
            return `/apps/${item.app_id}?tab=backups`;
        case 'task':
            return `/apps/${item.app_id}?tab=tasks`;
    }
}

// A digest is cut to its first 12, as Docker shows it.
function short(ref: string): string {
    return ref.startsWith('sha256:') ? ref.slice(7, 19) : ref;
}

// "22:41" today, "29 Oct" before.
function clock(value: string): string {
    const at = parseServerTime(value);
    return at.toDateString() === new Date().toDateString()
        ? at.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
        : at.toLocaleDateString(undefined, { day: 'numeric', month: 'short' });
}
