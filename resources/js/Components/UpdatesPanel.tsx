import { router, usePoll } from '@inertiajs/react';
import { CircleAlertIcon, CircleCheckIcon, DownloadIcon, Loader2Icon, RefreshCwIcon } from 'lucide-react';
import { useEffect, useState } from 'react';

import { LogView } from '@/Components/LogView';
import { Panel, Stats } from '@/Components/Panel';
import { Alert, AlertDescription, AlertTitle } from '@/Components/ui/alert';
import {
    AlertDialog,
    AlertDialogAction,
    AlertDialogCancel,
    AlertDialogContent,
    AlertDialogDescription,
    AlertDialogFooter,
    AlertDialogHeader,
    AlertDialogTitle,
    AlertDialogTrigger,
} from '@/Components/ui/alert-dialog';
import { Button } from '@/Components/ui/button';
import { formatDateTime, timeAgo } from '@/lib/time';
import type { UpdateSettings, UpdateStatus } from '@/types/models';

// UpdatesPanel shows the running and newest releases, and installs the
// newest through the root updater (ADR 0021), following its progress.
export function UpdatesPanel({ update }: { update: UpdateSettings }) {
    const [sending, setSending] = useState(false);
    // Echowa stops answering while it restarts, and a tunnel answers for it
    // with an error page. Polls ride that out instead of showing it.
    const [restarting, setRestarting] = useState(false);
    const unreachable = () => {
        setRestarting(true);
        return false;
    };
    const { start, stop } = usePoll(
        2000,
        {
            only: ['update'],
            onHttpException: unreachable,
            onNetworkError: unreachable,
            onSuccess: () => setRestarting(false),
        },
        { autoStart: false },
    );
    const watching = update.check.checking || update.busy;
    useEffect(() => {
        if (watching) {
            start();
        } else {
            stop();
        }
    }, [watching, start, stop]);

    if (update.dev) {
        return (
            <Panel title="Updates" description="Dev builds don't check for or install updates. Releases do, from here.">
                <Stats items={[{ label: 'Running', value: update.current, hint: 'dev build' }]} />
            </Panel>
        );
    }

    const { check, status } = update;
    const latest = check.latest;
    const send = (url: string, data = {}) =>
        router.post(url, data, {
            preserveScroll: true,
            onStart: () => setSending(true),
            onFinish: () => setSending(false),
        });

    return (
        <Panel
            title="Updates"
            description="Echowa checks for a new release every day. Updating backs up the database first, and rolls back if the new version doesn't start."
            actions={
                <Button
                    variant="outline"
                    onClick={() => send('/settings/update/check')}
                    disabled={check.checking || update.busy || sending}
                >
                    {check.checking ? <Loader2Icon className="animate-spin" /> : <RefreshCwIcon />}
                    {check.checking ? 'Checking…' : 'Check now'}
                </Button>
            }
        >
            <div className="space-y-5">
                <Stats
                    items={[
                        { label: 'Running', value: update.current },
                        {
                            label: 'Newest',
                            value: latest?.version ?? 'Unknown',
                            hint: latest ? `published ${timeAgo(latest.published_at)}` : 'not checked yet',
                        },
                        {
                            label: 'Last checked',
                            value: check.checked_at
                                ? timeAgo(check.checked_at)
                                : check.checking
                                  ? 'Checking…'
                                  : 'Not yet',
                            hint: check.checked_at ? formatDateTime(check.checked_at) : 'since Echowa started',
                        },
                    ]}
                />

                {check.error && (
                    <Alert variant="destructive">
                        <CircleAlertIcon />
                        <AlertTitle>The last check failed</AlertTitle>
                        <AlertDescription className="break-words">{check.error}</AlertDescription>
                    </Alert>
                )}

                {update.busy ? (
                    <Progress update={update} restarting={restarting} />
                ) : (
                    <>
                        {status && status.state !== 'running' && <LastUpdate status={status} />}
                        {update.available && latest ? (
                            <div className="space-y-3 rounded-lg border p-4">
                                <div className="flex flex-wrap items-start justify-between gap-3">
                                    <div>
                                        <h3 className="text-sm font-medium">Echowa {latest.version} is available</h3>
                                        <p className="text-sm text-muted-foreground">
                                            Published {formatDateTime(latest.published_at)}.
                                        </p>
                                    </div>
                                    <ConfirmUpdate
                                        version={latest.version}
                                        disabled={sending}
                                        onConfirm={() => send('/settings/update', { version: latest.version })}
                                    />
                                </div>
                                {latest.notes.trim() && <ReleaseNotes notes={latest.notes} />}
                            </div>
                        ) : (
                            latest && (
                                <p className="flex items-center gap-2 text-sm text-muted-foreground">
                                    <CircleCheckIcon className="size-4 text-success" />
                                    Echowa is up to date.
                                </p>
                            )
                        )}
                    </>
                )}
            </div>
        </Panel>
    );
}

function ConfirmUpdate({
    version,
    disabled,
    onConfirm,
}: {
    version: string;
    disabled: boolean;
    onConfirm: () => void;
}) {
    return (
        <AlertDialog>
            <AlertDialogTrigger
                render={
                    <Button disabled={disabled}>
                        <DownloadIcon />
                        Update to {version}
                    </Button>
                }
            />
            <AlertDialogContent>
                <AlertDialogHeader>
                    <AlertDialogTitle>Update Echowa to {version}?</AlertDialogTitle>
                    <AlertDialogDescription>
                        Echowa backs up its database, then restarts on the new version, which takes about a minute. Your
                        apps keep running. If {version} doesn't start, Echowa goes back to the version it runs now.
                    </AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                    <AlertDialogCancel>Cancel</AlertDialogCancel>
                    <AlertDialogAction onClick={onConfirm}>Update</AlertDialogAction>
                </AlertDialogFooter>
            </AlertDialogContent>
        </AlertDialog>
    );
}

function Progress({ update, restarting }: { update: UpdateSettings; restarting: boolean }) {
    const running = update.status?.state === 'running' ? update.status : null;
    const version = running?.to || update.requested;
    const lines = (running?.log ?? []).map((text, key) => ({ key, text }));

    return (
        <div className="space-y-3">
            <p className="flex items-center gap-2 text-sm">
                <Loader2Icon className="size-4 animate-spin text-muted-foreground" />
                {restarting
                    ? `Echowa is restarting on ${version}. This page reloads when it's back.`
                    : running
                      ? `Updating to ${version}…`
                      : `Update to ${version} requested. Waiting for the updater…`}
            </p>
            {running && <LogView lines={lines} empty="Starting…" fitTo={320} />}
        </div>
    );
}

function LastUpdate({ status }: { status: UpdateStatus }) {
    const lines = status.log.map((text, key) => ({ key, text }));
    if (status.state === 'succeeded') {
        return (
            <p className="flex items-center gap-2 text-sm text-muted-foreground">
                <CircleCheckIcon className="size-4 text-success" />
                <span>
                    {status.message}{' '}
                    <span title={formatDateTime(status.finished_at)}>{timeAgo(status.finished_at)}</span>
                </span>
            </p>
        );
    }

    return (
        <Alert variant="destructive">
            <CircleAlertIcon />
            <AlertTitle>
                {status.state === 'rolled_back'
                    ? `The update to ${status.to} failed, so Echowa rolled back`
                    : `The update to ${status.to || 'the newest release'} failed`}
            </AlertTitle>
            <AlertDescription className="space-y-2 break-words">
                <p>{status.message}</p>
                {lines.length > 0 && (
                    <details>
                        <summary className="cursor-pointer text-xs">
                            Updater log ({formatDateTime(status.started_at)})
                        </summary>
                        <LogView lines={lines} empty="" fitTo={320} className="mt-2 text-foreground" />
                    </details>
                )}
            </AlertDescription>
        </Alert>
    );
}

type NotesBlock = { kind: 'heading' | 'paragraph'; text: string } | { kind: 'list'; items: string[] };

// The Markdown the release notes are written in, as far as they use it:
// "## " headings, "- " or "* " bullets (an indented line carries one on),
// and paragraphs. Anything else shows as plain text.
function parseNotes(notes: string): NotesBlock[] {
    const blocks: NotesBlock[] = [];
    let last: NotesBlock | undefined;
    for (const line of notes.split('\n')) {
        const text = line.trim();
        const heading = /^#{1,6}\s+(.*)$/.exec(text);
        const bullet = /^[-*]\s+(.*)$/.exec(text);
        if (!text) {
            last = undefined;
        } else if (heading) {
            blocks.push({ kind: 'heading', text: heading[1] });
            last = undefined;
        } else if (bullet) {
            if (last?.kind !== 'list') {
                last = { kind: 'list', items: [] };
                blocks.push(last);
            }
            last.items.push(bullet[1]);
        } else if (last?.kind === 'list' && /^\s/.test(line)) {
            last.items[last.items.length - 1] += ` ${text}`;
        } else if (last?.kind === 'paragraph') {
            last.text += ` ${text}`;
        } else {
            last = { kind: 'paragraph', text };
            blocks.push(last);
        }
    }
    return blocks;
}

// biome-ignore-start lint/suspicious/noArrayIndexKey: parsed text, never reordered

// `code` spans, the one inline mark commit subjects tend to have.
function Inline({ text }: { text: string }) {
    return text.split(/(`[^`]+`)/).map((part, i) =>
        part.length > 2 && part.startsWith('`') && part.endsWith('`') ? (
            <code key={i} className="rounded bg-muted px-1 font-mono text-xs">
                {part.slice(1, -1)}
            </code>
        ) : (
            part
        ),
    );
}

function ReleaseNotes({ notes }: { notes: string }) {
    return (
        <div className="max-h-80 space-y-2 overflow-y-auto border-t pt-3 text-sm">
            {parseNotes(notes).map((block, i) =>
                block.kind === 'heading' ? (
                    <h4 key={i} className="pt-1 font-medium first:pt-0">
                        <Inline text={block.text} />
                    </h4>
                ) : block.kind === 'list' ? (
                    <ul
                        key={i}
                        className="list-disc space-y-1 pl-5 text-muted-foreground marker:text-muted-foreground/60"
                    >
                        {block.items.map((item, j) => (
                            <li key={j}>
                                <Inline text={item} />
                            </li>
                        ))}
                    </ul>
                ) : (
                    <p key={i} className="text-muted-foreground">
                        <Inline text={block.text} />
                    </p>
                ),
            )}
        </div>
    );
}
// biome-ignore-end lint/suspicious/noArrayIndexKey: parsed text, never reordered
