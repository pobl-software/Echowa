import { Head, router, usePoll } from '@inertiajs/react';
import { CircleAlertIcon, Loader2Icon, Trash2Icon } from 'lucide-react';
import { type ReactNode, useEffect, useState } from 'react';

import { Page } from '@/Components/Page';
import { Panel, Stats } from '@/Components/Panel';
import { UpdatesPanel } from '@/Components/UpdatesPanel';
import { Alert, AlertDescription, AlertTitle } from '@/Components/ui/alert';
import { Button } from '@/Components/ui/button';
import { formatDateTime, timeAgo } from '@/lib/time';
import type { PruneRun, PruneSettings, UpdateSettings } from '@/types/models';

const bytes = new Intl.NumberFormat(undefined, { style: 'unit', unit: 'megabyte', maximumFractionDigits: 1 });

export default function SettingsIndex({ prune, update }: { prune: PruneSettings; update: UpdateSettings }) {
    const [sending, setSending] = useState(false);

    // A prune runs in the background. Watch it until it's done.
    const { start, stop } = usePoll(1500, { only: ['prune'] }, { autoStart: false });
    const running = prune.running !== null;
    useEffect(() => {
        if (running) {
            start();
        } else {
            stop();
        }
    }, [running, start, stop]);

    const pruneNow = () =>
        router.post('/settings/prune', {}, { onStart: () => setSending(true), onFinish: () => setSending(false) });

    return (
        <>
            <Head title="Settings" />
            <Page crumbs={[{ label: 'Settings' }]}>
                <div className="space-y-6">
                    <UpdatesPanel update={update} />
                    <Panel
                        title="Image pruning"
                        description={`Every day at midnight (UTC), Echowa removes its apps' images that no container uses and that were built more than ${prune.min_age} ago. Images from other repositories are never touched.`}
                        actions={
                            <Button variant="outline" onClick={pruneNow} disabled={running || sending}>
                                {running ? <Loader2Icon className="animate-spin" /> : <Trash2Icon />}
                                {running ? 'Pruning…' : 'Prune now'}
                            </Button>
                        }
                    >
                        <div className="space-y-5">
                            <Stats
                                items={[
                                    { label: 'Kept per app', value: `Last ${prune.keep}`, hint: 'successful deploys' },
                                    { label: 'Minimum age', value: prune.min_age, hint: 'since the image was built' },
                                    {
                                        label: 'Last run',
                                        value: prune.last?.finished_at ? timeAgo(prune.last.finished_at) : 'Not yet',
                                        hint: prune.last
                                            ? formatDateTime(prune.last.finished_at)
                                            : 'since Echowa started',
                                    },
                                ]}
                            />
                            <p className="text-xs text-muted-foreground">
                                Change these with <code className="font-mono">ECHOWA_PRUNE_KEEP</code> and{' '}
                                <code className="font-mono">ECHOWA_PRUNE_MIN_AGE</code> (in{' '}
                                <code className="font-mono">/etc/echowa/env</code> on a server), then restart Echowa.
                            </p>
                            {prune.running && (
                                <p className="flex items-center gap-2 text-sm text-muted-foreground">
                                    <Loader2Icon className="size-4 animate-spin" />
                                    Pruning since {formatDateTime(prune.running.started_at)}…
                                </p>
                            )}
                            {prune.last && <LastRun run={prune.last} keep={prune.keep} />}
                        </div>
                    </Panel>
                </div>
            </Page>
        </>
    );
}

function LastRun({ run, keep }: { run: PruneRun; keep: number }) {
    return (
        <div className="space-y-4">
            {run.error && (
                <Alert variant="destructive">
                    <CircleAlertIcon />
                    <AlertTitle>The last prune stopped part way</AlertTitle>
                    <AlertDescription className="break-words">{run.error}</AlertDescription>
                </Alert>
            )}
            <p className="text-sm">
                {run.trigger === 'manual' ? 'Run by hand' : 'Scheduled run'}: removed{' '}
                <strong className="font-medium">
                    {run.removed.length} {run.removed.length === 1 ? 'image' : 'images'}
                </strong>
                {run.space_reclaimed > 0 && <>, freeing {bytes.format(run.space_reclaimed / 1e6)}</>}.
            </p>
            <div className="grid gap-4 md:grid-cols-2">
                <RefList title="Removed" empty="Nothing to remove." refs={run.removed} />
                <RefList
                    title={`Kept for rollbacks (${run.kept.length})`}
                    hint={`The last ${keep} successful deploys of each app, and any deploy in progress.`}
                    empty="No successful deploys yet."
                    refs={run.kept}
                />
            </div>
        </div>
    );
}

function RefList({ title, hint, empty, refs }: { title: string; hint?: ReactNode; empty: string; refs: string[] }) {
    return (
        <div className="min-w-0">
            <h3 className="text-xs font-medium text-muted-foreground">{title}</h3>
            {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
            {refs.length === 0 ? (
                <p className="mt-2 text-sm text-muted-foreground">{empty}</p>
            ) : (
                <ul className="mt-2 max-h-64 space-y-1 overflow-y-auto rounded-md bg-muted px-3 py-2 font-mono text-xs">
                    {refs.map((ref) => (
                        <li key={ref} className="truncate" title={ref}>
                            {ref}
                        </li>
                    ))}
                </ul>
            )}
        </div>
    );
}
