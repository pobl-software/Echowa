import { Link, router, useForm } from '@inertiajs/react';
import {
    ArchiveIcon,
    ChevronDownIcon,
    CloudDownloadIcon,
    DownloadIcon,
    EllipsisIcon,
    FileTextIcon,
    FileUpIcon,
    HistoryIcon,
    PencilIcon,
    PlusIcon,
    Trash2Icon,
    TriangleAlertIcon,
    XIcon,
} from 'lucide-react';
import { type FormEvent, type ReactNode, useEffect, useState } from 'react';
import { RestoreDialog, RestoreFromBucket, RestoreProgress, RestoresPanel } from '@/Components/AppRestores';
import { ConfigSection } from '@/Components/ConfigPanel';
import { ConfirmDelete } from '@/Components/ConfirmDelete';
import type { RestoreSource } from '@/Components/NewAppRestore';
import { Panel, Stats } from '@/Components/Panel';
import { RestoreFromFile } from '@/Components/RestoreFromFile';
import { browserZone, type Preset, presetLabel, ScheduleFields, ScheduleText } from '@/Components/ScheduleFields';
import { RunStatusBadge } from '@/Components/StatusBadge';
import { Alert, AlertAction, AlertDescription, AlertTitle } from '@/Components/ui/alert';
import { Button, buttonVariants } from '@/Components/ui/button';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
    DialogTrigger,
} from '@/Components/ui/dialog';
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuGroup,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
} from '@/Components/ui/dropdown-menu';
import { Field, FieldDescription, FieldError, FieldLabel } from '@/Components/ui/field';
import { InputGroup, InputGroupAddon, InputGroupInput, InputGroupText } from '@/Components/ui/input-group';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/Components/ui/select';
import { Switch } from '@/Components/ui/switch';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/Components/ui/table';
import { formatBytes } from '@/lib/format';
import { duration, formatDateTime, formatInZone, timeAgo } from '@/lib/time';
import { cn } from '@/lib/utils';
import type {
    App,
    AppWork,
    BackupDetail,
    BackupFailure,
    BackupSchedule,
    BackupSummary,
    BackupsSection,
    BackupTrigger,
    StorageChoice,
} from '@/types/models';

const triggers: Record<BackupTrigger, string> = {
    manual: 'By hand',
    schedule: 'On schedule',
    before_restore: 'Before a restore',
};

// The schedules the form offers by name: nightly, out of most apps' busy
// hours, rather than at midnight.
const presets: Preset[] = [
    { value: '0 * * * *', label: 'Every hour' },
    { value: '0 3 * * *', label: 'Every day at 03:00' },
    { value: '0 3 * * 0', label: 'Every Sunday at 03:00' },
    { value: '0 3 1 * *', label: 'The 1st of every month at 03:00' },
];

// AppBackups is a template database's Backups section (ADR 0034). From
// the top: a running restore, a summary of how well the app is covered,
// its backups with Back up now, its schedules, its restores, then the ways
// in and out that need no history: Export, a file, and the bucket. held is
// what holds the app's lock, the page's busy prop.
export function AppBackups({ app, section, held }: { app: App; section: BackupsSection; held: AppWork }) {
    const [opened, setOpened] = useState<BackupSummary | null>(null);
    const [restoring, setRestoring] = useState<RestoreSource | null>(null);

    // A running backup's size and a restore's progress go up every few
    // seconds, a schedule's backup starts on the minute, and Export waits
    // for whatever holds the app.
    const running = section.backups.some((b) => b.status === 'running');
    const restore = section.restores.find((r) => r.status === 'running');
    const scheduled = section.schedules.some((s) => s.enabled);
    const busy = running || restore !== undefined || held !== '';
    useEffect(() => {
        if (!busy && !scheduled) {
            return;
        }
        const id = window.setInterval(() => router.reload({ only: ['backups', 'busy'] }), busy ? 3000 : 30_000);
        return () => window.clearInterval(id);
    }, [busy, scheduled]);
    const fileBlocked = restore ? `A restore of ${app.name} is running.` : null;
    const stored = section.locations.length > 0;

    return (
        <div className="space-y-6">
            {restore && <RestoreProgress restore={restore} />}
            {stored ? <Coverage section={section} /> : <NoStorage app={app} />}

            {stored && (
                <Panel
                    title="Backups"
                    description={`Dumps of the ${section.database} database in S3, newest first.`}
                    actions={<BackUpButton app={app} locations={section.locations} running={running} />}
                    flush
                >
                    {section.backups.length === 0 ? (
                        <p className="px-4 py-8 text-center text-sm text-muted-foreground sm:px-5">
                            No backups yet. Back up now, or add a schedule below.
                        </p>
                    ) : (
                        <Table>
                            <TableHeader>
                                <TableRow>
                                    <TableHead className="w-8">
                                        <span className="sr-only">Status</span>
                                    </TableHead>
                                    <TableHead>Started</TableHead>
                                    <TableHead className="hidden md:table-cell">Location</TableHead>
                                    <TableHead className="hidden text-right sm:table-cell">Size</TableHead>
                                    <TableHead className="hidden text-right lg:table-cell">Took</TableHead>
                                    <TableHead className="hidden xl:table-cell">Server</TableHead>
                                    <TableHead>
                                        <span className="sr-only">Actions</span>
                                    </TableHead>
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {section.backups.map((backup) => (
                                    <BackupRow
                                        key={backup.id}
                                        app={app}
                                        backup={backup}
                                        onOpen={() => setOpened(backup)}
                                        onRestore={restore ? undefined : () => setRestoring({ backup: backup.id })}
                                    />
                                ))}
                            </TableBody>
                        </Table>
                    )}
                </Panel>
            )}

            {stored && <Schedules app={app} section={section} />}

            <Panel
                title="Export and restore"
                description={`Download the database, or restore ${app.name} from a file or straight from a bucket.`}
                flush
            >
                <div className="divide-y">
                    <ConfigSection
                        icon={DownloadIcon}
                        title="Export"
                        description="Downloads the database to this browser as it's dumped. Nothing is kept on the server or in S3."
                    >
                        <ExportControl app={app} section={section} held={held} />
                    </ConfigSection>
                    <ConfigSection
                        icon={FileUpIcon}
                        title="Restore from a file"
                        description="Uploads a dump from this computer, checks it, then restores it into the database."
                    >
                        <RestoreFromFile app={app} section={section} blocked={fileBlocked} />
                    </ConfigSection>
                    {stored && (
                        <ConfigSection
                            icon={CloudDownloadIcon}
                            title="Restore from the bucket"
                            description="For backups this server has no record of, such as a lost server's."
                        >
                            <RestoreFromBucket
                                app={app}
                                locations={section.locations}
                                onRestore={(source) => !restore && setRestoring(source)}
                            />
                        </ConfigSection>
                    )}
                </div>
            </Panel>

            <RestoresPanel app={app} restores={section.restores} />

            <BackupOutput app={app} backup={opened} onClose={() => setOpened(null)} />
            <RestoreDialog app={app} source={restoring} onClose={() => setRestoring(null)} />
        </div>
    );
}

// Coverage answers "is this app backed up?" at a glance: the last good
// backup, the next one due, what's kept, and what a backup covers.
function Coverage({ section }: { section: BackupsSection }) {
    const last = section.last_succeeded;
    const newest = section.backups.find((b) => b.status !== 'running');
    const failedSince = newest && newest.status !== 'succeeded' && (!last || newest.id > last.id) ? newest : undefined;
    const next = section.schedules
        .filter((s) => s.enabled && s.next_run_at)
        .sort((a, b) => (a.next_run_at ?? '').localeCompare(b.next_run_at ?? ''))[0];
    const kept = section.backups.filter((b) => b.status === 'succeeded');
    const more = section.backups.length >= 50 ? '+' : '';
    const version = last?.server_version;

    return (
        <Stats
            // Two across until xl: the sub nav leaves too little room for four at lg.
            className="lg:grid-cols-2 xl:grid-cols-4"
            items={[
                {
                    label: 'Last backup',
                    value: last ? (
                        <span title={formatDateTime(last.started_at)}>{timeAgo(last.started_at)}</span>
                    ) : (
                        'Never'
                    ),
                    hint: failedSince ? (
                        <span className="text-destructive">
                            The newest one {endings[failedSince.status] ?? 'failed'}
                        </span>
                    ) : last ? (
                        `${formatBytes(last.size_bytes)} to ${last.location}`
                    ) : (
                        'Back up now to make the first'
                    ),
                },
                {
                    label: 'Next backup',
                    value: next?.next_run_at ? (
                        <span title={formatInZone(next.next_run_at, next.timezone)}>{timeAgo(next.next_run_at)}</span>
                    ) : (
                        'Not scheduled'
                    ),
                    hint: next ? <ScheduleText presets={presets} schedule={next.schedule} /> : 'Only when you back up',
                },
                {
                    label: 'Stored',
                    value: `${kept.length}${more} ${kept.length === 1 && !more ? 'backup' : 'backups'}`,
                    hint:
                        kept.length > 0
                            ? `${formatBytes(kept.reduce((sum, b) => sum + b.size_bytes, 0))}${more ? ' in the newest' : ' in all'}`
                            : 'None kept yet',
                },
                {
                    label: 'Covers',
                    value: <span className="font-mono text-base">{section.database}</span>,
                    hint: (
                        <span title="A backup holds this one database, not others made by hand in the same server.">
                            {version ? `Server ${version}, this database only` : 'This database only'}
                        </span>
                    ),
                },
            ]}
        />
    );
}

// NoStorage is where the summary goes until there's somewhere to back up
// to. Export and restoring from a file still work below it.
function NoStorage({ app }: { app: App }) {
    return (
        <div className="flex flex-col items-center rounded-xl border border-dashed px-6 py-12 text-center">
            <span className="mb-4 flex size-11 items-center justify-center rounded-xl border bg-card">
                <ArchiveIcon className="size-5 text-muted-foreground" />
            </span>
            <h2 className="font-medium">Back {app.name} up to S3</h2>
            <p className="mt-1 max-w-md text-sm text-muted-foreground">
                Add a bucket in Settings, then back up by hand or on a schedule. Any S3-compatible provider works.
            </p>
            <Link href="/settings" className={buttonVariants({ className: 'mt-5' })}>
                <PlusIcon />
                Add S3 storage
            </Link>
        </div>
    );
}

// BackUpButton backs up to the location used last, or, from its menu, to
// any other.
function BackUpButton({ app, locations, running }: { app: App; locations: StorageChoice[]; running: boolean }) {
    const [starting, setStarting] = useState(false);
    const disabled = starting || running;
    const backUp = (location: number) =>
        router.post(
            `/apps/${app.id}/backups`,
            { location },
            { preserveScroll: true, onStart: () => setStarting(true), onFinish: () => setStarting(false) },
        );
    const first = locations[0];
    const label = running ? 'Backing up…' : 'Back up now';

    return (
        <div className="flex">
            <Button
                onClick={() => backUp(first.id)}
                disabled={disabled}
                title={`To ${first.name}`}
                className={cn(locations.length > 1 && 'rounded-r-none')}
            >
                <ArchiveIcon />
                {label}
            </Button>
            {locations.length > 1 && (
                <DropdownMenu>
                    <DropdownMenuTrigger
                        render={
                            <Button
                                size="icon"
                                disabled={disabled}
                                className="rounded-l-none border-l border-primary-foreground/25"
                                aria-label="Back up to another location"
                            />
                        }
                    >
                        <ChevronDownIcon />
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end" className="w-64">
                        <DropdownMenuGroup>
                            <DropdownMenuLabel>Back up to</DropdownMenuLabel>
                            {locations.map((l) => (
                                <DropdownMenuItem key={l.id} onClick={() => backUp(l.id)} className="block">
                                    <span className="block">{l.name}</span>
                                    <span className="block truncate font-mono text-xs text-muted-foreground">
                                        {l.prefix ? `${l.bucket}/${l.prefix}` : l.bucket}
                                    </span>
                                </DropdownMenuItem>
                            ))}
                        </DropdownMenuGroup>
                    </DropdownMenuContent>
                </DropdownMenu>
            )}
        </div>
    );
}

function BackupRow({
    app,
    backup,
    onOpen,
    onRestore,
}: {
    app: App;
    backup: BackupSummary;
    onOpen: () => void;
    // Unset while a restore is running.
    onRestore?: () => void;
}) {
    const done = backup.status !== 'running';
    const kept = backup.status === 'succeeded';
    const size = kept || (!done && backup.size_bytes > 0) ? formatBytes(backup.size_bytes) : null;
    const problem = backup.message && !kept ? backup.message : null;

    return (
        <TableRow>
            <TableCell className="pr-0">
                <RunStatusBadge status={backup.status} quiet />
            </TableCell>
            <TableCell className="w-full max-w-0">
                <div className="flex flex-col sm:flex-row sm:items-baseline sm:gap-2">
                    <span title={formatDateTime(backup.started_at)}>{timeAgo(backup.started_at)}</span>
                    <span className="truncate text-xs text-muted-foreground">
                        {triggers[backup.trigger]}
                        {/* Phones have no Size column. */}
                        {size && <span className="tabular-nums sm:hidden"> · {size}</span>}
                    </span>
                </div>
                {problem && (
                    <p className="truncate text-xs text-muted-foreground" title={problem}>
                        {problem}
                    </p>
                )}
            </TableCell>
            <TableCell className="hidden md:table-cell">{backup.location}</TableCell>
            <TableCell className="hidden text-right tabular-nums sm:table-cell">
                {size ?? <span className="text-muted-foreground">—</span>}
            </TableCell>
            <TableCell className="hidden text-right text-muted-foreground tabular-nums lg:table-cell">
                {duration(backup.started_at, backup.finished_at)}
            </TableCell>
            <TableCell className="hidden font-mono text-xs text-muted-foreground xl:table-cell">
                {backup.server_version || '—'}
            </TableCell>
            <TableCell className="py-1.5">
                <div className="flex items-center justify-end gap-1">
                    {kept && (
                        <Button
                            variant="outline"
                            size="sm"
                            onClick={onRestore}
                            disabled={!onRestore}
                            title={onRestore ? undefined : 'A restore is running'}
                            className="hidden sm:inline-flex"
                        >
                            <HistoryIcon />
                            Restore
                        </Button>
                    )}
                    <BackupMenu app={app} backup={backup} onOpen={onOpen} onRestore={onRestore} />
                </div>
            </TableCell>
        </TableRow>
    );
}

// BackupMenu holds a backup's less frequent actions. Restore is in it too
// on phones, where the row has no room for its button.
function BackupMenu({
    app,
    backup,
    onOpen,
    onRestore,
}: {
    app: App;
    backup: BackupSummary;
    onOpen: () => void;
    onRestore?: () => void;
}) {
    const [deleting, setDeleting] = useState(false);
    const kept = backup.status === 'succeeded';

    return (
        <>
            <DropdownMenu>
                <DropdownMenuTrigger
                    render={<Button variant="ghost" size="icon-sm" aria-label={`More for backup #${backup.id}`} />}
                >
                    <EllipsisIcon />
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-44">
                    {kept && (
                        <DropdownMenuItem onClick={onRestore} disabled={!onRestore} className="sm:hidden">
                            <HistoryIcon />
                            Restore
                        </DropdownMenuItem>
                    )}
                    {kept && (
                        <DropdownMenuItem
                            render={<a href={`/apps/${app.id}/backups/${backup.id}/download`} download={backup.name} />}
                        >
                            <DownloadIcon />
                            Download
                        </DropdownMenuItem>
                    )}
                    <DropdownMenuItem onClick={onOpen}>
                        <FileTextIcon />
                        Details
                    </DropdownMenuItem>
                    {backup.status !== 'running' && (
                        <>
                            <DropdownMenuSeparator />
                            <DropdownMenuItem variant="destructive" onClick={() => setDeleting(true)}>
                                <Trash2Icon />
                                Delete
                            </DropdownMenuItem>
                        </>
                    )}
                </DropdownMenuContent>
            </DropdownMenu>
            <ConfirmDelete
                title="Delete this backup?"
                open={deleting}
                onOpenChange={setDeleting}
                onConfirm={() => router.delete(`/apps/${app.id}/backups/${backup.id}`, { preserveScroll: true })}
            >
                {kept
                    ? `The dump and its manifest are deleted from ${backup.location}, and can't be restored afterwards.`
                    : 'Echowa forgets this backup. Nothing of it was kept in the bucket.'}
            </ConfirmDelete>
        </>
    );
}

// Schedules back the app up when they're due, each keeping its newest
// successful backups.
function Schedules({ app, section }: { app: App; section: BackupsSection }) {
    return (
        <Panel
            title="Schedules"
            description="Each keeps its newest successful backups and deletes older ones. Backups made by hand stay."
            actions={
                <ScheduleDialog
                    app={app}
                    locations={section.locations}
                    trigger={
                        <Button variant="outline">
                            <PlusIcon />
                            Add schedule
                        </Button>
                    }
                />
            }
            flush
        >
            {section.schedules.length === 0 ? (
                <p className="px-4 py-8 text-center text-sm text-muted-foreground sm:px-5">
                    Not on a schedule yet. Add one, and Echowa backs {app.name} up for you.
                </p>
            ) : (
                <Table>
                    <TableHeader>
                        <TableRow>
                            <TableHead>Schedule</TableHead>
                            <TableHead className="hidden md:table-cell">Next</TableHead>
                            <TableHead className="hidden text-right sm:table-cell">Kept</TableHead>
                            <TableHead className="hidden lg:table-cell">Last backup</TableHead>
                            <TableHead>
                                <span className="sr-only">On</span>
                            </TableHead>
                            <TableHead>
                                <span className="sr-only">Actions</span>
                            </TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {section.schedules.map((schedule) => (
                            <ScheduleRow
                                key={schedule.id}
                                app={app}
                                schedule={schedule}
                                locations={section.locations}
                            />
                        ))}
                    </TableBody>
                </Table>
            )}
        </Panel>
    );
}

function ScheduleRow({ app, schedule, locations }: { app: App; schedule: BackupSchedule; locations: StorageChoice[] }) {
    const base = `/apps/${app.id}/backup-schedules/${schedule.id}`;
    const last = schedule.last_backup;

    return (
        <TableRow className={cn(!schedule.enabled && 'text-muted-foreground')}>
            <TableCell className="w-full max-w-0">
                <div className="truncate font-medium">
                    <ScheduleText presets={presets} schedule={schedule.schedule} />
                </div>
                <div className="truncate text-xs text-muted-foreground">
                    {schedule.timezone} · to {schedule.location}
                </div>
            </TableCell>
            <TableCell className="hidden text-sm whitespace-nowrap md:table-cell">
                {schedule.next_run_at ? (
                    <span title={formatInZone(schedule.next_run_at, schedule.timezone)}>
                        {timeAgo(schedule.next_run_at)}
                    </span>
                ) : (
                    <span className="text-muted-foreground">Off</span>
                )}
            </TableCell>
            <TableCell
                className="hidden text-right whitespace-nowrap tabular-nums sm:table-cell"
                title={`Keeps its newest ${schedule.keep}`}
            >
                {schedule.kept} <span className="text-muted-foreground">of {schedule.keep}</span>
            </TableCell>
            <TableCell className="hidden lg:table-cell">
                {last ? (
                    <div className="flex items-center gap-1.5 text-sm whitespace-nowrap">
                        <RunStatusBadge status={last.status} quiet />
                        <span title={formatDateTime(last.started_at)}>{timeAgo(last.started_at)}</span>
                    </div>
                ) : (
                    <span className="text-sm text-muted-foreground">Never</span>
                )}
            </TableCell>
            <TableCell>
                <Switch
                    checked={schedule.enabled}
                    aria-label={schedule.enabled ? 'Turn this schedule off' : 'Turn this schedule on'}
                    title={schedule.enabled ? 'On' : 'Off'}
                    onCheckedChange={(enabled) => router.put(`${base}/enabled`, { enabled }, { preserveScroll: true })}
                />
            </TableCell>
            <TableCell className="py-1">
                <div className="flex justify-end gap-1">
                    <ScheduleDialog
                        app={app}
                        schedule={schedule}
                        locations={locations}
                        trigger={
                            <Button variant="ghost" size="icon-sm" aria-label="Edit schedule" title="Edit">
                                <PencilIcon />
                            </Button>
                        }
                    />
                    <ConfirmDelete
                        title="Delete this schedule?"
                        trigger={
                            <Button
                                variant="ghost"
                                size="icon-sm"
                                className="text-destructive"
                                aria-label="Delete schedule"
                                title="Delete"
                            >
                                <Trash2Icon />
                            </Button>
                        }
                        onConfirm={() => router.delete(base, { preserveScroll: true })}
                    >
                        Its {schedule.kept === 1 ? 'backup stays' : `${schedule.kept} backups stay`} in the history and
                        the bucket, and are no longer deleted as newer ones come. A backup that&apos;s running finishes.
                    </ConfirmDelete>
                </div>
            </TableCell>
        </TableRow>
    );
}

// ExportControl downloads the database straight to this browser, in a
// format a local tool reads, with no storage location and nothing kept
// (ADR 0034). A plain link, not a download one: the server's refusal,
// while a deploy holds the app say, then shows instead of a failed
// download.
function ExportControl({ app, section, held }: { app: App; section: BackupsSection; held: AppWork }) {
    const [format, setFormat] = useState(() => section.exports[0]?.format ?? '');
    const chosen = section.exports.find((e) => e.format === format) ?? section.exports[0];
    if (!chosen) {
        return null;
    }

    return (
        <div className="flex flex-col items-start gap-2">
            <div className="flex w-full flex-col gap-2 sm:flex-row sm:items-center">
                {section.exports.length > 1 && (
                    <Select
                        items={section.exports.map((e) => ({ value: e.format, label: `${e.label} (.${e.extension})` }))}
                        value={chosen.format}
                        onValueChange={(value) => value && setFormat(value)}
                    >
                        <SelectTrigger aria-label="Format" className="w-full sm:w-64">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            {section.exports.map((e) => (
                                <SelectItem key={e.format} value={e.format}>
                                    {e.label}
                                    <span className="font-mono text-xs text-muted-foreground">.{e.extension}</span>
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                )}
                {held ? (
                    <Button variant="outline" disabled>
                        <DownloadIcon />
                        Export
                    </Button>
                ) : (
                    <a
                        href={`/apps/${app.id}/export?format=${chosen.format}`}
                        className={buttonVariants({ variant: 'outline' })}
                    >
                        <DownloadIcon />
                        Export
                    </a>
                )}
            </div>
            <p className="text-sm text-muted-foreground">
                {held
                    ? `${held === 'export' ? 'An export' : `A ${held}`} of ${app.name} is running. Export once it has finished.`
                    : `${chosen.hint} Deploys of ${app.name} wait until the download ends.`}
            </p>
        </div>
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

type ScheduleForm = {
    storage_location_id: number | null;
    schedule: string;
    timezone: string;
    keep: number | string;
    enabled: boolean;
};

// ScheduleDialog adds a backup schedule, or edits one when given it.
function ScheduleDialog({
    app,
    schedule,
    locations,
    trigger,
}: {
    app: App;
    schedule?: BackupSchedule;
    locations: StorageChoice[];
    trigger: ReactNode;
}) {
    const [open, setOpen] = useState(false);
    const defaults: ScheduleForm = schedule
        ? {
              storage_location_id: schedule.storage_location_id,
              schedule: schedule.schedule,
              timezone: schedule.timezone,
              keep: schedule.keep,
              enabled: schedule.enabled,
          }
        : {
              storage_location_id: locations[0]?.id ?? null,
              schedule: presets[1].value,
              timezone: browserZone,
              keep: 7,
              enabled: true,
          };
    const form = useForm<ScheduleForm>(defaults);
    const [custom, setCustom] = useState(() => !presetLabel(presets, defaults.schedule));
    const keep = Number(form.data.keep) || 0;
    const fewer = schedule && keep > 0 && keep < schedule.kept;

    const reset = () => {
        form.setDefaults(defaults);
        form.reset();
        form.clearErrors();
        setCustom(!presetLabel(presets, defaults.schedule));
    };
    const submit = (e: FormEvent) => {
        e.preventDefault();
        const options = { preserveScroll: true, onSuccess: () => setOpen(false) };
        if (schedule) {
            form.put(`/apps/${app.id}/backup-schedules/${schedule.id}`, options);
        } else {
            form.post(`/apps/${app.id}/backup-schedules`, options);
        }
    };

    return (
        <Dialog
            open={open}
            onOpenChange={(next) => {
                if (next) {
                    reset();
                }
                setOpen(next);
            }}
        >
            <DialogTrigger render={trigger as React.ReactElement} />
            <DialogContent className="max-h-[calc(100dvh-2rem)] grid-rows-[minmax(0,1fr)] sm:max-w-lg">
                <form onSubmit={submit} className="grid min-h-0 grid-rows-[auto_minmax(0,1fr)_auto] gap-4">
                    <DialogHeader>
                        <DialogTitle>
                            {schedule ? 'Edit the schedule' : `Back ${app.name} up on a schedule`}
                        </DialogTitle>
                        <DialogDescription>
                            Echowa backs up when it&apos;s due, unless the last backup is still going.
                        </DialogDescription>
                    </DialogHeader>

                    <div className="-mx-4 grid min-h-0 content-start gap-5 overflow-y-auto px-4 py-0.5">
                        <Field data-invalid={!!form.errors.storage_location_id || undefined}>
                            <FieldLabel htmlFor="storage_location_id">Storage location</FieldLabel>
                            <Select
                                items={locations.map((l) => ({ value: l.id, label: l.name }))}
                                value={form.data.storage_location_id}
                                onValueChange={(value) => form.setData('storage_location_id', value)}
                            >
                                <SelectTrigger id="storage_location_id" className="w-full">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    {locations.map((l) => (
                                        <SelectItem key={l.id} value={l.id}>
                                            {l.name}
                                            <span className="font-mono text-xs text-muted-foreground">
                                                {l.prefix ? `${l.bucket}/${l.prefix}` : l.bucket}
                                            </span>
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                            {form.errors.storage_location_id && (
                                <FieldError>{form.errors.storage_location_id}</FieldError>
                            )}
                        </Field>

                        <ScheduleFields
                            presets={presets}
                            schedule={form.data.schedule}
                            timezone={form.data.timezone}
                            onScheduleChange={(value) => form.setData('schedule', value)}
                            onTimezoneChange={(value) => form.setData('timezone', value)}
                            custom={custom}
                            onCustomChange={setCustom}
                            errors={form.errors}
                            active={open}
                        />

                        <Field data-invalid={!!form.errors.keep || undefined}>
                            <FieldLabel htmlFor="keep">Keep</FieldLabel>
                            <InputGroup className="max-w-40">
                                <InputGroupInput
                                    id="keep"
                                    type="number"
                                    min={1}
                                    max={100}
                                    className="tabular-nums [appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                                    value={form.data.keep}
                                    onChange={(e) => form.setData('keep', e.target.value)}
                                    aria-invalid={!!form.errors.keep || undefined}
                                />
                                <InputGroupAddon align="inline-end">
                                    <InputGroupText>backups</InputGroupText>
                                </InputGroupAddon>
                            </InputGroup>
                            {form.errors.keep && <FieldError>{form.errors.keep}</FieldError>}
                            <FieldDescription>
                                The newest successful backups this schedule keeps. After each new one, Echowa deletes
                                its older ones from the bucket.
                                {fewer &&
                                    ` It has ${schedule.kept} now, so its next success deletes ${schedule.kept - keep + 1}.`}
                            </FieldDescription>
                        </Field>

                        <Field orientation="horizontal">
                            <Switch
                                id="enabled"
                                checked={form.data.enabled}
                                onCheckedChange={(enabled) => form.setData('enabled', enabled)}
                            />
                            <FieldLabel htmlFor="enabled" className="font-normal">
                                On. Off, it doesn&apos;t back up until you turn it back on.
                            </FieldLabel>
                        </Field>
                    </div>

                    <DialogFooter>
                        <Button type="submit" disabled={form.processing}>
                            {schedule ? 'Save' : 'Add schedule'}
                        </Button>
                    </DialogFooter>
                </form>
            </DialogContent>
        </Dialog>
    );
}

const endings: Partial<Record<BackupSummary['status'], string>> = {
    failed: 'failed',
    skipped: 'was skipped',
    interrupted: 'was interrupted',
};

// BackupAlert is the Overview's alert for a schedule whose newest backup
// that didn't succeed hasn't been dismissed. Dismissing names the backup,
// so a failure that came after the page loaded still shows.
export function BackupAlert({ app, failure }: { app: App; failure: BackupFailure }) {
    const { backup } = failure;
    const label = presetLabel(presets, failure.schedule) ?? failure.schedule;

    return (
        <Alert variant="destructive">
            <TriangleAlertIcon />
            <AlertTitle>
                A scheduled backup {endings[backup.status] ?? 'failed'}{' '}
                <span title={formatDateTime(backup.started_at)}>{timeAgo(backup.started_at)}</span>
            </AlertTitle>
            <AlertDescription className="flex flex-col items-start gap-2 [&_p:not(:last-child)]:mb-0">
                <p>
                    {backup.message}
                    {failure.succeeded_at && ` It has succeeded since, ${timeAgo(failure.succeeded_at)}.`}
                </p>
                <p className="text-xs">
                    {label} in {failure.timezone}, to {backup.location}.
                </p>
                <Link href={`/apps/${app.id}?tab=backups`}>See its backups</Link>
            </AlertDescription>
            <AlertAction>
                <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label="Dismiss the failed backup"
                    title="Dismiss"
                    onClick={() =>
                        router.post(
                            `/apps/${app.id}/backup-schedules/${failure.schedule_id}/dismiss`,
                            { backup: backup.id },
                            { preserveScroll: true, preserveState: true },
                        )
                    }
                >
                    <XIcon />
                </Button>
            </AlertAction>
        </Alert>
    );
}

// LastBackup is the Overview's line for when the app was last backed up.
export function LastBackup({ section }: { section: BackupsSection }) {
    const last = section.last_succeeded;
    if (!last) {
        return <span className="text-muted-foreground">Never</span>;
    }

    return (
        <span title={formatDateTime(last.started_at)}>
            {timeAgo(last.started_at)} to {last.location}, {formatBytes(last.size_bytes)}
        </span>
    );
}
