import { Link, router, useForm } from '@inertiajs/react';
import {
    ArchiveIcon,
    DownloadIcon,
    FileTextIcon,
    HistoryIcon,
    PencilIcon,
    PlusIcon,
    Trash2Icon,
    TriangleAlertIcon,
    XIcon,
} from 'lucide-react';
import { type FormEvent, type ReactNode, useEffect, useState } from 'react';

import { RestoreDialog, RestoreFromBucket, RestoreProgress, RestoresPanel } from '@/Components/AppRestores';
import { ConfirmDelete } from '@/Components/ConfirmDelete';
import type { RestoreSource } from '@/Components/NewAppRestore';
import { Panel } from '@/Components/Panel';
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

// AppBackups is a template database's Backups section (ADR 0034): back up
// now to a storage location, the backups made so far, and restores.
export function AppBackups({ app, section }: { app: App; section: BackupsSection }) {
    const [location, setLocation] = useState<number | null>(() => section.locations[0]?.id ?? null);
    const [starting, setStarting] = useState(false);
    const [opened, setOpened] = useState<BackupSummary | null>(null);
    const [restoring, setRestoring] = useState<RestoreSource | null>(null);

    // A running backup's size and a restore's progress go up every few
    // seconds, and a schedule's backup starts on the minute.
    const running = section.backups.some((b) => b.status === 'running');
    const restore = section.restores.find((r) => r.status === 'running');
    const scheduled = section.schedules.some((s) => s.enabled);
    const busy = running || restore !== undefined;
    useEffect(() => {
        if (!busy && !scheduled) {
            return;
        }
        const id = window.setInterval(() => router.reload({ only: ['backups', 'busy'] }), busy ? 3000 : 30_000);
        return () => window.clearInterval(id);
    }, [busy, scheduled]);

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
            {restore && <RestoreProgress restore={restore} />}
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
                title="Schedules"
                description="Each backs up on its own schedule and keeps its newest successful backups. Backups made by hand are never deleted."
                actions={<ScheduleDialog app={app} locations={section.locations} trigger={<AddScheduleButton />} />}
                flush
            >
                {section.schedules.length === 0 ? (
                    <p className="px-4 py-6 text-sm text-muted-foreground">
                        No schedules yet. {app.name} is backed up only when you back it up.
                    </p>
                ) : (
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>Schedule</TableHead>
                                <TableHead className="hidden md:table-cell">Location</TableHead>
                                <TableHead className="hidden xl:table-cell">Next backup</TableHead>
                                <TableHead className="hidden text-right sm:table-cell">Keeps</TableHead>
                                <TableHead className="hidden sm:table-cell">Last backup</TableHead>
                                <TableHead>
                                    <span className="sr-only">Enabled</span>
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
                                <TableHead className="hidden text-right sm:table-cell">Size</TableHead>
                                <TableHead className="hidden text-right sm:table-cell">Took</TableHead>
                                <TableHead className="hidden lg:table-cell">Server</TableHead>
                                <TableHead className="text-right">
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

            <RestoresPanel app={app} restores={section.restores} />
            <RestoreFromBucket
                app={app}
                locations={section.locations}
                onRestore={(source) => !restore && setRestoring(source)}
            />

            <BackupOutput app={app} backup={opened} onClose={() => setOpened(null)} />
            <RestoreDialog app={app} source={restoring} onClose={() => setRestoring(null)} />
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

    return (
        <TableRow>
            <TableCell>
                <div title={formatDateTime(backup.started_at)}>{timeAgo(backup.started_at)}</div>
                <div className="text-xs text-muted-foreground">{triggers[backup.trigger]}</div>
                {/* Phones have no Size column. */}
                {size && <div className="text-xs text-muted-foreground tabular-nums sm:hidden">{size}</div>}
            </TableCell>
            <TableCell className="w-full max-w-0 min-w-24">
                <RunStatusBadge status={backup.status} />
                {backup.message && !kept && (
                    <p className="mt-1 truncate text-xs text-muted-foreground" title={backup.message}>
                        {backup.message}
                    </p>
                )}
            </TableCell>
            <TableCell className="hidden md:table-cell">{backup.location}</TableCell>
            <TableCell className="hidden text-right tabular-nums sm:table-cell">{size ?? '—'}</TableCell>
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
                            // Phones have room for three actions; a dump is
                            // rarely wanted on one.
                            className={cn(
                                buttonVariants({ variant: 'ghost', size: 'icon-sm' }),
                                'hidden sm:inline-flex',
                            )}
                            aria-label={`Download ${backup.name}`}
                            title="Download"
                        >
                            <DownloadIcon />
                        </a>
                    )}
                    {kept && (
                        <Button
                            variant="ghost"
                            size="icon-sm"
                            onClick={onRestore}
                            disabled={!onRestore}
                            aria-label={`Restore ${backup.name}`}
                            title={onRestore ? 'Restore' : 'A restore is running'}
                        >
                            <HistoryIcon />
                        </Button>
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

function AddScheduleButton(props: React.ComponentProps<typeof Button>) {
    return (
        <Button variant="outline" {...props}>
            <PlusIcon />
            Add schedule
        </Button>
    );
}

function ScheduleRow({ app, schedule, locations }: { app: App; schedule: BackupSchedule; locations: StorageChoice[] }) {
    const base = `/apps/${app.id}/backup-schedules/${schedule.id}`;
    const last = schedule.last_backup;

    return (
        <TableRow>
            <TableCell className="w-full max-w-0">
                <div className="truncate">
                    <ScheduleText presets={presets} schedule={schedule.schedule} />
                </div>
                <div className="truncate text-xs text-muted-foreground">
                    {schedule.timezone}
                    {/* Phones have no Location column. */}
                    <span className="md:hidden"> to {schedule.location}</span>
                </div>
                {last && (
                    <div className="mt-1 sm:hidden">
                        <RunStatusBadge status={last.status} />
                    </div>
                )}
            </TableCell>
            <TableCell className="hidden md:table-cell">{schedule.location}</TableCell>
            <TableCell className="hidden text-sm xl:table-cell">
                {schedule.next_run_at ? (
                    <span title={formatInZone(schedule.next_run_at, schedule.timezone)}>
                        {timeAgo(schedule.next_run_at)}
                    </span>
                ) : (
                    <span className="text-muted-foreground">Off</span>
                )}
            </TableCell>
            <TableCell
                className="hidden text-right tabular-nums sm:table-cell"
                title={`${schedule.kept} kept of ${schedule.keep}`}
            >
                {schedule.kept} / {schedule.keep}
            </TableCell>
            <TableCell className="hidden sm:table-cell">
                {last ? (
                    <div className="flex flex-col items-start gap-0.5">
                        <RunStatusBadge status={last.status} />
                        <span className="text-xs text-muted-foreground" title={formatDateTime(last.started_at)}>
                            {timeAgo(last.started_at)}
                        </span>
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
