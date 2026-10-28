import { Link, router } from '@inertiajs/react';
import { ArchiveIcon, DownloadIcon, FileTextIcon, Trash2Icon } from 'lucide-react';
import { useEffect, useState } from 'react';

import { ConfirmDelete } from '@/Components/ConfirmDelete';
import { Panel } from '@/Components/Panel';
import { RunStatusBadge } from '@/Components/StatusBadge';
import { Button, buttonVariants } from '@/Components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/Components/ui/dialog';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/Components/ui/select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/Components/ui/table';
import { formatBytes } from '@/lib/format';
import { duration, formatDateTime, timeAgo } from '@/lib/time';
import type { App, BackupDetail, BackupSummary, BackupsSection, BackupTrigger } from '@/types/models';

const triggers: Record<BackupTrigger, string> = {
    manual: 'By hand',
    schedule: 'On schedule',
    before_restore: 'Before a restore',
};

// AppBackups is a template database's Backups section (ADR 0034): back up
// now to a storage location, and the backups made so far.
export function AppBackups({ app, section }: { app: App; section: BackupsSection }) {
    const [location, setLocation] = useState<number | null>(() => section.locations[0]?.id ?? null);
    const [starting, setStarting] = useState(false);
    const [opened, setOpened] = useState<BackupSummary | null>(null);

    // A running backup's size goes up every few seconds.
    const running = section.backups.some((b) => b.status === 'running');
    useEffect(() => {
        if (!running) {
            return;
        }
        const id = window.setInterval(() => router.reload({ only: ['backups'] }), 3000);
        return () => window.clearInterval(id);
    }, [running]);

    if (section.locations.length === 0) {
        return (
            <div className="flex flex-col items-center rounded-xl border border-dashed px-6 py-14 text-center">
                <span className="mb-4 flex size-11 items-center justify-center rounded-xl border bg-card">
                    <ArchiveIcon className="size-5 text-muted-foreground" />
                </span>
                <h2 className="font-medium">No storage yet</h2>
                <p className="mt-1 max-w-md text-sm text-muted-foreground">
                    Backups of {app.name} go to an S3 bucket. Add one in Settings, then come back to back up.
                </p>
                <Link href="/settings" className={buttonVariants({ className: 'mt-5' })}>
                    Add S3 storage
                </Link>
            </div>
        );
    }

    const backUp = () => {
        router.post(
            `/apps/${app.id}/backups`,
            { location },
            { preserveScroll: true, onStart: () => setStarting(true), onFinish: () => setStarting(false) },
        );
    };

    return (
        <div className="space-y-6">
            <Panel
                title="Back up now"
                description={
                    <>
                        Dumps the <code className="font-mono text-xs">{section.database}</code> database to S3. Other
                        databases in this server aren&apos;t included.
                    </>
                }
            >
                <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
                    <Select
                        items={section.locations.map((l) => ({ value: l.id, label: l.name }))}
                        value={location}
                        onValueChange={(value) => setLocation(value)}
                    >
                        <SelectTrigger aria-label="Storage location" className="w-full sm:w-64">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            {section.locations.map((l) => (
                                <SelectItem key={l.id} value={l.id}>
                                    {l.name}
                                    <span className="font-mono text-xs text-muted-foreground">
                                        {l.prefix ? `${l.bucket}/${l.prefix}` : l.bucket}
                                    </span>
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                    <Button onClick={backUp} disabled={starting || running || location === null}>
                        <ArchiveIcon />
                        Back up now
                    </Button>
                    {running && <p className="text-sm text-muted-foreground">A backup is running.</p>}
                </div>
            </Panel>

            <Panel
                title="History"
                description="Newest first. Each backup is a dump and a manifest in its bucket."
                flush
            >
                {section.backups.length === 0 ? (
                    <p className="px-4 py-6 text-sm text-muted-foreground">No backups yet.</p>
                ) : (
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>Started</TableHead>
                                <TableHead>Status</TableHead>
                                <TableHead className="hidden md:table-cell">Location</TableHead>
                                <TableHead className="text-right">Size</TableHead>
                                <TableHead className="hidden text-right sm:table-cell">Took</TableHead>
                                <TableHead className="hidden lg:table-cell">Server</TableHead>
                                <TableHead className="text-right">
                                    <span className="sr-only">Actions</span>
                                </TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {section.backups.map((backup) => (
                                <BackupRow key={backup.id} app={app} backup={backup} onOpen={() => setOpened(backup)} />
                            ))}
                        </TableBody>
                    </Table>
                )}
            </Panel>

            <BackupOutput app={app} backup={opened} onClose={() => setOpened(null)} />
        </div>
    );
}

function BackupRow({ app, backup, onOpen }: { app: App; backup: BackupSummary; onOpen: () => void }) {
    const done = backup.status !== 'running';
    const kept = backup.status === 'succeeded';

    return (
        <TableRow>
            <TableCell>
                <div title={formatDateTime(backup.started_at)}>{timeAgo(backup.started_at)}</div>
                <div className="text-xs text-muted-foreground">{triggers[backup.trigger]}</div>
            </TableCell>
            <TableCell className="max-w-64">
                <RunStatusBadge status={backup.status} />
                {backup.message && !kept && (
                    <p className="mt-1 truncate text-xs text-muted-foreground" title={backup.message}>
                        {backup.message}
                    </p>
                )}
            </TableCell>
            <TableCell className="hidden md:table-cell">{backup.location}</TableCell>
            <TableCell className="text-right tabular-nums">
                {kept || (!done && backup.size_bytes > 0) ? formatBytes(backup.size_bytes) : '—'}
            </TableCell>
            <TableCell className="hidden text-right tabular-nums sm:table-cell">
                {duration(backup.started_at, backup.finished_at)}
            </TableCell>
            <TableCell className="hidden font-mono text-xs lg:table-cell">{backup.server_version || '—'}</TableCell>
            <TableCell>
                <div className="flex justify-end gap-1">
                    {kept && (
                        <a
                            href={`/apps/${app.id}/backups/${backup.id}/download`}
                            download={backup.name}
                            className={buttonVariants({ variant: 'ghost', size: 'icon-sm' })}
                            aria-label={`Download ${backup.name}`}
                            title="Download"
                        >
                            <DownloadIcon />
                        </a>
                    )}
                    <Button variant="ghost" size="icon-sm" onClick={onOpen} aria-label="Show output" title="Output">
                        <FileTextIcon />
                    </Button>
                    {done && (
                        <ConfirmDelete
                            title="Delete this backup?"
                            trigger={
                                <Button
                                    variant="ghost"
                                    size="icon-sm"
                                    className="text-destructive"
                                    aria-label="Delete backup"
                                    title="Delete"
                                >
                                    <Trash2Icon />
                                </Button>
                            }
                            onConfirm={() =>
                                router.delete(`/apps/${app.id}/backups/${backup.id}`, { preserveScroll: true })
                            }
                        >
                            {kept
                                ? `The dump and its manifest are deleted from ${backup.location}, and can't be restored afterwards.`
                                : 'Echowa forgets this backup. Nothing of it was kept in the bucket.'}
                        </ConfirmDelete>
                    )}
                </div>
            </TableCell>
        </TableRow>
    );
}

// BackupOutput shows a backup's message, the end of the dump's stderr, and
// where it is.
function BackupOutput({ app, backup, onClose }: { app: App; backup: BackupSummary | null; onClose: () => void }) {
    const [detail, setDetail] = useState<BackupDetail | null>(null);
    const id = backup?.id;
    const status = backup?.status;

    // biome-ignore lint/correctness/useExhaustiveDependencies: status changes when the backup ends, which is when its output is complete
    useEffect(() => {
        setDetail(null);
        if (id === undefined) {
            return;
        }
        let cancelled = false;
        fetch(`/apps/${app.id}/backups/${id}`, { headers: { Accept: 'application/json' } })
            .then((response) => (response.ok ? response.json() : null))
            .then((body: BackupDetail | null) => {
                if (!cancelled) {
                    setDetail(body);
                }
            });
        return () => {
            cancelled = true;
        };
    }, [app.id, id, status]);

    return (
        <Dialog open={backup !== null} onOpenChange={(open) => !open && onClose()}>
            <DialogContent className="sm:max-w-2xl">
                <DialogHeader>
                    <DialogTitle>Backup #{backup?.id}</DialogTitle>
                    <DialogDescription>
                        {backup && `${formatDateTime(backup.started_at)} to ${backup.location}`}
                    </DialogDescription>
                </DialogHeader>
                {detail === null ? (
                    <p className="text-sm text-muted-foreground">Loading…</p>
                ) : (
                    <div className="grid min-w-0 gap-3 text-sm">
                        {detail.message && <p>{detail.message}</p>}
                        {detail.key && (
                            <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1">
                                <dt className="text-muted-foreground">Key</dt>
                                <dd className="font-mono text-xs break-all">{detail.key}</dd>
                                {detail.sha256 && (
                                    <>
                                        <dt className="text-muted-foreground">SHA-256</dt>
                                        <dd className="font-mono text-xs break-all">{detail.sha256}</dd>
                                    </>
                                )}
                            </dl>
                        )}
                        <pre className="max-h-80 overflow-auto rounded-lg border bg-muted/40 p-3 font-mono text-xs whitespace-pre-wrap break-all">
                            {detail.output ||
                                (detail.status === 'running' ? 'Running…' : 'The dump wrote nothing to stderr.')}
                        </pre>
                    </div>
                )}
            </DialogContent>
        </Dialog>
    );
}
