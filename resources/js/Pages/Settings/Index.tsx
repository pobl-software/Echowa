import { Link } from '@inertiajs/react';
import { ChevronRightIcon, DownloadIcon, Loader2Icon } from 'lucide-react';

import { Logo } from '@/Components/Logo';
import { SectionLabel } from '@/Components/SectionLabel';
import { SettingsPage, type SettingsSection, settingsGroups, settingsSections } from '@/Components/SettingsNav';
import { StatusPill, type Tone } from '@/Components/StatusBadge';
import { buttonVariants } from '@/Components/ui/button';
import { formatBytes } from '@/lib/format';
import { formatDateTime, timeAgo } from '@/lib/time';
import { cn } from '@/lib/utils';
import type { SettingsOverview, UpdateSettings } from '@/types/models';

interface Summary {
    tone: Tone;
    text: string;
    pulse?: boolean;
}

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

// What each section's card says: the state worth knowing, worst first.
const summaries: Record<Exclude<SettingsSection, 'overview' | 'updates'>, (o: SettingsOverview) => Summary> = {
    registries: ({ registries }) =>
        registries.count === 0
            ? { tone: 'neutral', text: 'None yet' }
            : {
                  tone: 'neutral',
                  text: `${plural(registries.count, 'registry', 'registries')} · used by ${plural(registries.apps, 'app')}`,
              },
    storage: ({ storage }) => {
        if (storage.locations === 0) {
            return storage.databases > 0
                ? {
                      tone: 'warning',
                      text: `Not set up, so ${plural(storage.databases, 'database')} can’t be backed up`,
                  }
                : { tone: 'neutral', text: 'Not set up' };
        }
        return {
            tone: 'neutral',
            text: `${plural(storage.locations, 'location')} · ${plural(storage.backups, 'backup')}`,
        };
    },
    pruning: ({ prune }) => {
        if (prune.running) {
            return { tone: 'primary', text: 'Pruning now', pulse: true };
        }
        if (!prune.last) {
            return { tone: 'neutral', text: 'Not run since Echowa started' };
        }
        if (prune.last.error) {
            return { tone: 'warning', text: `The last prune stopped part way, ${timeAgo(prune.last.finished_at)}` };
        }
        const freed = prune.last.space_reclaimed > 0 ? ` · freed ${formatBytes(prune.last.space_reclaimed)}` : '';
        return { tone: 'neutral', text: `Last ran ${timeAgo(prune.last.finished_at)}${freed}` };
    },
    notifications: ({ notifications }) => {
        if (notifications.providers === 0) {
            return { tone: 'warning', text: 'Not set up, so alerts have nowhere to go' };
        }
        if (notifications.failing.length > 0) {
            return {
                tone: 'danger',
                text:
                    notifications.failing.length === 1
                        ? `The last send to ${notifications.failing[0]} failed`
                        : `${notifications.failing.length} providers’ last sends failed`,
            };
        }
        if (notifications.enabled === 0) {
            return { tone: 'warning', text: 'Every provider is off' };
        }
        const off = notifications.providers - notifications.enabled;
        return {
            tone: 'neutral',
            text: plural(notifications.providers, 'provider') + (off > 0 ? ` · ${off} off` : ''),
        };
    },
    alerts: ({ alerts }) => {
        if (alerts.firing > 0) {
            return { tone: 'danger', text: `${plural(alerts.firing, 'alert')} firing`, pulse: true };
        }
        if (alerts.rules === 0) {
            return {
                tone: 'neutral',
                text: alerts.suggestions > 0 ? `None yet · ${alerts.suggestions} suggested` : 'None yet',
            };
        }
        return {
            tone: 'neutral',
            text: `${plural(alerts.rules, 'alert')}${alerts.enabled < alerts.rules ? ` · ${alerts.rules - alerts.enabled} off` : ''} · none firing`,
        };
    },
};

const dot: Record<Tone, string> = {
    success: 'bg-success',
    warning: 'bg-warning',
    danger: 'bg-destructive',
    neutral: 'bg-muted-foreground/50',
    primary: 'bg-primary',
};

const text: Record<Tone, string> = {
    success: 'text-muted-foreground',
    warning: 'text-warning',
    danger: 'text-destructive',
    neutral: 'text-muted-foreground',
    primary: 'text-primary',
};

export default function SettingsIndex(overview: SettingsOverview) {
    return (
        <SettingsPage section="overview">
            <div className="space-y-8">
                <Version update={overview.update} />
                {settingsGroups.map((group) => (
                    <section key={group} className="space-y-3">
                        <h2>
                            <SectionLabel>{group}</SectionLabel>
                        </h2>
                        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
                            {settingsSections
                                .filter((s) => s.group === group)
                                .map((s) => {
                                    const summary = summaries[s.id as keyof typeof summaries](overview);
                                    return (
                                        <Link
                                            key={s.id}
                                            href={s.href}
                                            className={cn(
                                                'group flex flex-col rounded-xl border bg-card p-4 transition-colors hover:border-foreground/20',
                                                summary.tone === 'warning' && 'border-warning/40',
                                                summary.tone === 'danger' && 'border-destructive/40',
                                            )}
                                        >
                                            <div className="flex items-center gap-2.5">
                                                <span className="flex size-8 shrink-0 items-center justify-center rounded-md border bg-muted/40">
                                                    <s.icon className="size-4 text-muted-foreground" />
                                                </span>
                                                <span className="font-medium">{s.label}</span>
                                                <ChevronRightIcon className="ml-auto size-4 text-muted-foreground/60 transition-transform group-hover:translate-x-0.5" />
                                            </div>
                                            <p className="mt-2.5 flex-1 text-sm text-muted-foreground">
                                                {s.description}
                                            </p>
                                            <p
                                                className={cn(
                                                    'mt-3 flex items-center gap-2 border-t pt-3 text-xs',
                                                    text[summary.tone],
                                                )}
                                            >
                                                <span className="relative flex size-1.5 shrink-0">
                                                    {summary.pulse && (
                                                        <span
                                                            className={cn(
                                                                'absolute inline-flex size-full animate-ping rounded-full opacity-70',
                                                                dot[summary.tone],
                                                            )}
                                                        />
                                                    )}
                                                    <span
                                                        className={cn(
                                                            'relative inline-flex size-1.5 rounded-full',
                                                            dot[summary.tone],
                                                        )}
                                                    />
                                                </span>
                                                <span className="truncate">{summary.text}</span>
                                            </p>
                                        </Link>
                                    );
                                })}
                        </div>
                    </section>
                ))}
            </div>
        </SettingsPage>
    );
}

// Version is the release this server runs, and what to do about a newer one.
function Version({ update }: { update: UpdateSettings }) {
    const { check, status } = update;
    const latest = check.latest;
    const failed = !update.busy && status && (status.state === 'failed' || status.state === 'rolled_back');

    let line: React.ReactNode;
    if (update.dev) {
        line = 'A dev build, which doesn’t check for updates. Releases check every day.';
    } else if (update.busy) {
        line = (
            <span className="flex items-center gap-2">
                <Loader2Icon className="size-3.5 animate-spin" />
                Updating to {update.requested || status?.to}…
            </span>
        );
    } else if (update.available && latest) {
        line = (
            <span title={formatDateTime(latest.published_at)}>
                {latest.version} came out {timeAgo(latest.published_at)}.
            </span>
        );
    } else if (check.error) {
        line = (
            <span className="text-warning" title={check.error}>
                Couldn’t check for updates{check.checked_at ? `, ${timeAgo(check.checked_at)}` : ''}.
            </span>
        );
    } else if (check.checked_at) {
        line = <span title={formatDateTime(check.checked_at)}>Up to date. Checked {timeAgo(check.checked_at)}.</span>;
    } else {
        line = 'Not checked for updates since Echowa started.';
    }

    return (
        <section className="flex flex-wrap items-center gap-x-4 gap-y-3 rounded-xl border bg-card p-4 sm:p-5">
            <Logo className="h-7" />
            <div aria-hidden="true" className="hidden h-10 w-px bg-border sm:block" />
            <div className="min-w-0 basis-full sm:basis-0 sm:flex-1">
                <div className="flex flex-wrap items-center gap-2">
                    <h2 className="font-medium">
                        <span className="sr-only">Echowa </span>
                        <span className="text-muted-foreground">Version</span>{' '}
                        <span className="font-mono">{update.current}</span>
                    </h2>
                    {update.available && !update.busy && <StatusPill tone="primary">Update available</StatusPill>}
                    {!update.dev && !update.available && !update.busy && latest && !check.error && (
                        <StatusPill tone="success">Up to date</StatusPill>
                    )}
                </div>
                <div className="mt-0.5 text-sm text-muted-foreground">{line}</div>
                {failed && (
                    <p className="mt-1 text-sm text-destructive">
                        {status.state === 'rolled_back'
                            ? `The update to ${status.to} failed, so Echowa rolled back.`
                            : `The update to ${status.to || 'the newest release'} failed.`}
                    </p>
                )}
            </div>
            {update.available && latest && !update.busy ? (
                <Link href="/settings/updates" className={buttonVariants()}>
                    <DownloadIcon />
                    Update to {latest.version}
                </Link>
            ) : (
                <Link href="/settings/updates" className={buttonVariants({ variant: 'outline' })}>
                    Updates
                </Link>
            )}
        </section>
    );
}
