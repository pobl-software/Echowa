import { useForm } from '@inertiajs/react';
import { FileTextIcon, FolderSearchIcon, HistoryIcon, RefreshCwIcon, TriangleAlertIcon } from 'lucide-react';
import { type FormEvent, useEffect, useState } from 'react';

import { NewAppRestoreForm, Refusal, type RestoreSource, sourceQuery } from '@/Components/NewAppRestore';
import { Panel } from '@/Components/Panel';
import { RunStatusBadge } from '@/Components/StatusBadge';
import { Alert, AlertDescription, AlertTitle } from '@/Components/ui/alert';
import { Button } from '@/Components/ui/button';
import { Checkbox } from '@/Components/ui/checkbox';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from '@/Components/ui/dialog';
import { Field, FieldError, FieldLabel } from '@/Components/ui/field';
import { Input } from '@/Components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/Components/ui/select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/Components/ui/table';
import { Tabs, TabsList, TabsTrigger } from '@/Components/ui/tabs';
import { formatBytes } from '@/lib/format';
import { duration, formatDateTime, timeAgo } from '@/lib/time';
import { cn } from '@/lib/utils';
import type { App, BucketBackup, RestoreDetail, RestorePreview, RestoreSummary, StorageChoice } from '@/types/models';

// RestoreDialog checks a backup against the app, says what restoring it
// replaces, and restores it once the app's name is typed (ADR 0034). Or it
// restores the backup into a new app, which replaces nothing.
export function RestoreDialog({
    app,
    source,
    onClose,
}: {
    app: App;
    source: RestoreSource | null;
    onClose: () => void;
}) {
    const [preview, setPreview] = useState<RestorePreview | null>(null);
    const [problem, setProblem] = useState<string | null>(null);
    const [mode, setMode] = useState<'here' | 'new'>('here');
    const form = useForm({ backup: 0, location: 0, key: '', safety: true, confirm: '' });

    // biome-ignore lint/correctness/useExhaustiveDependencies: a new source resets the form
    useEffect(() => {
        setPreview(null);
        setProblem(null);
        setMode('here');
        if (source === null) {
            return;
        }
        form.reset();
        form.clearErrors();
        form.setData({
            backup: 'backup' in source ? source.backup : 0,
            location: 'location' in source ? source.location : 0,
            key: 'key' in source ? source.key : '',
            safety: true,
            confirm: '',
        });
        const controller = new AbortController();
        fetch(`/apps/${app.id}/restores/preview?${sourceQuery(source)}`, {
            headers: { Accept: 'application/json' },
            signal: controller.signal,
        })
            .then(async (response) => {
                const body = await response.json();
                if (response.ok) {
                    setPreview(body);
                } else {
                    setProblem(body.error ?? 'Couldn’t check this backup.');
                }
            })
            .catch(() => {
                if (!controller.signal.aborted) {
                    setProblem('Couldn’t check this backup.');
                }
            });
        return () => controller.abort();
    }, [app.id, source]);

    const submit = (e: FormEvent) => {
        e.preventDefault();
        form.post(`/apps/${app.id}/restores`, { preserveState: true, preserveScroll: true, onSuccess: onClose });
    };
    const errors = form.errors as Record<string, string | undefined>;
    const ready = preview !== null && !preview.refusal && form.data.confirm === app.name;

    return (
        <Dialog open={source !== null} onOpenChange={(open) => !open && onClose()}>
            <DialogContent className="sm:max-w-xl">
                <div className="grid min-w-0 gap-4">
                    <DialogHeader>
                        <DialogTitle>{mode === 'new' ? 'Restore into a new app' : `Restore ${app.name}`}</DialogTitle>
                        <DialogDescription>
                            {preview
                                ? `From ${preview.name} in ${preview.location}.`
                                : problem
                                  ? 'This backup can’t be restored.'
                                  : 'Checking the backup…'}
                        </DialogDescription>
                    </DialogHeader>
                    {problem && <Refusal reason={problem} />}
                    {preview && (
                        <Tabs value={mode} onValueChange={(value) => setMode(value === 'new' ? 'new' : 'here')}>
                            <TabsList aria-label="Restore into" className="w-full">
                                <TabsTrigger value="here" className="min-w-0 flex-1">
                                    <span className="truncate">Into {app.name}</span>
                                </TabsTrigger>
                                <TabsTrigger value="new" className="flex-1">
                                    Into a new app
                                </TabsTrigger>
                            </TabsList>
                        </Tabs>
                    )}
                    {preview && <BackupFacts preview={preview} newApp={mode === 'new'} />}
                    {preview && source && mode === 'new' && (
                        <NewAppRestoreForm
                            key={sourceQuery(source).toString()}
                            source={source}
                            name={`${app.name}-restored`}
                            tag={preview.default_tag}
                            template={preview.template}
                            onCancel={onClose}
                        />
                    )}
                </div>
                {mode === 'here' && (
                    <form onSubmit={submit} className="grid min-w-0 gap-4">
                        {preview &&
                            (preview.refusal ? (
                                <Refusal reason={preview.refusal} />
                            ) : (
                                <>
                                    {preview.warning && (
                                        <Alert>
                                            <TriangleAlertIcon className="text-warning" />
                                            <AlertTitle>A newer major version</AlertTitle>
                                            <AlertDescription>{preview.warning}</AlertDescription>
                                        </Alert>
                                    )}
                                    <Replaces preview={preview} />
                                    <Field>
                                        <label className="flex items-start gap-2 text-sm">
                                            <Checkbox
                                                className="mt-0.5"
                                                aria-labelledby="safety-label"
                                                checked={form.data.safety}
                                                onCheckedChange={(checked) => form.setData('safety', checked)}
                                            />
                                            <span>
                                                <span id="safety-label" className="font-medium">
                                                    Back up {app.name} first
                                                </span>
                                                <span className="block text-muted-foreground">
                                                    {form.data.safety
                                                        ? `To ${preview.location}, so you can go back. If this backup fails, nothing is restored.`
                                                        : 'Without it, the data the restore replaces can’t be got back.'}
                                                </span>
                                            </span>
                                        </label>
                                    </Field>
                                    <Field data-invalid={!!errors.confirm || undefined}>
                                        <FieldLabel htmlFor="restore-confirm">
                                            Type <span className="font-mono">{app.name}</span> to confirm
                                        </FieldLabel>
                                        <Input
                                            id="restore-confirm"
                                            autoComplete="off"
                                            spellCheck={false}
                                            className="font-mono"
                                            value={form.data.confirm}
                                            onChange={(e) => form.setData('confirm', e.target.value)}
                                            aria-invalid={!!errors.confirm || undefined}
                                        />
                                        {errors.confirm && <FieldError>{errors.confirm}</FieldError>}
                                    </Field>
                                </>
                            ))}
                        {errors.source && <Refusal reason={errors.source} />}

                        <DialogFooter>
                            <Button type="button" variant="outline" onClick={onClose}>
                                Cancel
                            </Button>
                            <Button type="submit" variant="destructive" disabled={!ready || form.processing}>
                                <HistoryIcon />
                                Restore {app.name}
                            </Button>
                        </DialogFooter>
                    </form>
                )}
            </DialogContent>
        </Dialog>
    );
}

// BackupFacts compares the backup with the app it would restore into, or
// for a new app, just describes it.
export function BackupFacts({ preview, newApp }: { preview: RestorePreview; newApp: boolean }) {
    const sameVersion = !newApp && preview.server_version === preview.app_version;

    return (
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 rounded-lg border bg-muted/40 px-3 py-2 text-sm">
            {preview.started_at && (
                <>
                    <dt className="text-muted-foreground">Made</dt>
                    <dd title={formatDateTime(preview.started_at)}>
                        {formatDateTime(preview.started_at)}{' '}
                        <span className="text-muted-foreground">({timeAgo(preview.started_at)})</span>
                    </dd>
                </>
            )}
            <dt className="text-muted-foreground">Size</dt>
            <dd className="tabular-nums">{formatBytes(preview.size_bytes)}</dd>
            <dt className="text-muted-foreground">Database</dt>
            <dd className="font-mono text-xs leading-5 break-all">
                {newApp || preview.database === preview.app_database
                    ? preview.database
                    : `${preview.database} into ${preview.app_database}`}
            </dd>
            <dt className="text-muted-foreground">{preview.template}</dt>
            <dd className="font-mono text-xs leading-5">
                {preview.server_version}
                {!newApp && preview.app_version && !sameVersion && ` into ${preview.app_version}`}
                {sameVersion && <span className="font-sans text-muted-foreground"> on both</span>}
            </dd>
            {preview.backup_id === null && !preview.uploaded && (
                <>
                    <dt className="text-muted-foreground">Record</dt>
                    <dd className="text-muted-foreground">None. Echowa found it in the bucket.</dd>
                </>
            )}
        </dl>
    );
}

// Replaces says what the restore changes, and how it fails. MySQL's isn't
// all or nothing, so it says so before it's confirmed.
export function Replaces({ preview }: { preview: RestorePreview }) {
    const database = <code className="font-mono text-xs">{preview.app_database}</code>;
    if (preview.atomic) {
        return (
            <p className="text-sm text-muted-foreground">
                Replaces each table, view and function in the backup in the {database} database, all or nothing: if any
                of it fails, nothing changes. Tables made since the backup stay. Apps using the database wait while it
                runs.
            </p>
        );
    }

    return (
        <Alert>
            <TriangleAlertIcon className="text-warning" />
            <AlertTitle>This restore isn&apos;t all or nothing</AlertTitle>
            <AlertDescription>
                <p>
                    It drops and recreates each table in the backup in the {database} database, one at a time. If it
                    fails part way, some tables are restored and others aren&apos;t. Tables made since the backup stay.
                    Apps using the database see errors while it runs.
                </p>
            </AlertDescription>
        </Alert>
    );
}

// RestoreFromBucket lists the backups in a location's folder for the app,
// including ones Echowa has no record of, such as another server's.
export function RestoreFromBucket({
    app,
    locations,
    onRestore,
}: {
    app: App;
    locations: StorageChoice[];
    onRestore: (source: RestoreSource) => void;
}) {
    const [location, setLocation] = useState<number | null>(null);
    const [listing, setListing] = useState<{ folder: string; backups: BucketBackup[] } | null>(null);
    const [error, setError] = useState<string | null>(null);
    const [loading, setLoading] = useState(false);

    const browse = async (id: number) => {
        setLoading(true);
        setError(null);
        try {
            const response = await fetch(`/apps/${app.id}/restores/browse?location=${id}`, {
                headers: { Accept: 'application/json' },
            });
            const body = await response.json();
            if (response.ok) {
                setListing(body);
            } else {
                setListing(null);
                setError(body.error ?? 'Couldn’t list the bucket.');
            }
        } catch {
            setError('Couldn’t list the bucket.');
        } finally {
            setLoading(false);
        }
    };

    return (
        <div className="flex flex-col gap-3">
            <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                <Select
                    items={locations.map((l) => ({ value: l.id, label: l.name }))}
                    value={location}
                    onValueChange={(value) => {
                        setLocation(value);
                        if (value !== null) {
                            browse(value);
                        }
                    }}
                >
                    <SelectTrigger aria-label="Location to look in" className="w-full sm:w-64">
                        <SelectValue placeholder="Choose a location" />
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
                {location !== null && (
                    <Button variant="ghost" onClick={() => browse(location)} disabled={loading}>
                        <RefreshCwIcon className={cn(loading && 'animate-spin')} />
                        Look again
                    </Button>
                )}
            </div>
            {error && <p className="text-sm text-destructive">{error}</p>}
            {!error && listing === null && (
                <p className="flex items-center gap-2 text-sm text-muted-foreground">
                    <FolderSearchIcon className="size-4 shrink-0" />
                    {loading
                        ? 'Looking…'
                        : `Lists the backups in ${app.name}'s folder, including ones another server made or this one forgot.`}
                </p>
            )}
            {!error && listing && (
                <div className="overflow-hidden rounded-lg border">
                    <p
                        className="truncate border-b bg-muted/40 px-3 py-1.5 font-mono text-xs text-muted-foreground"
                        title={listing.folder}
                    >
                        {listing.folder}
                    </p>
                    {listing.backups.length === 0 ? (
                        <p className="px-3 py-4 text-sm text-muted-foreground">
                            No backups of {app.name} in this folder.
                        </p>
                    ) : (
                        <ul className="max-h-80 divide-y overflow-y-auto">
                            {listing.backups.map((b) => (
                                <li key={b.key} className="flex items-center gap-3 px-3 py-2">
                                    <div className="min-w-0 flex-1">
                                        <p className="truncate font-mono text-xs" title={b.name}>
                                            {b.name}
                                        </p>
                                        <p className="text-xs text-muted-foreground">
                                            <span title={formatDateTime(b.modified)}>{timeAgo(b.modified)}</span>
                                            {' · '}
                                            <span className="tabular-nums">{formatBytes(b.size_bytes)}</span>
                                            {' · '}
                                            {b.backup_id ? `Backup #${b.backup_id}` : 'No record here'}
                                        </p>
                                    </div>
                                    <Button
                                        variant="outline"
                                        size="sm"
                                        onClick={() => location !== null && onRestore({ location, key: b.key })}
                                    >
                                        Restore
                                    </Button>
                                </li>
                            ))}
                        </ul>
                    )}
                </div>
            )}
        </div>
    );
}

// RestoreProgress is the running restore, at the top of the section.
export function RestoreProgress({ restore }: { restore: RestoreSummary }) {
    const share = restore.size_bytes > 0 ? Math.min(1, restore.read_bytes / restore.size_bytes) : 0;

    return (
        <Alert>
            <HistoryIcon className="text-warning" />
            <AlertTitle>Restoring from {restore.name}</AlertTitle>
            <AlertDescription className="grid gap-2">
                <p>
                    {restore.read_bytes > 0
                        ? `${formatBytes(restore.read_bytes)} of ${formatBytes(restore.size_bytes)} read.`
                        : restore.new_app && restore.message
                          ? restore.message
                          : restore.safety_backup_id
                            ? `Backing up first, as backup #${restore.safety_backup_id}.`
                            : 'Starting.'}{' '}
                    {restore.new_app
                        ? 'This app was made for the restore, which begins once it’s running.'
                        : 'Deploys and backups of this app wait until it has finished.'}
                </p>
                <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                    <div className="h-full bg-warning transition-[width]" style={{ width: `${share * 100}%` }} />
                </div>
            </AlertDescription>
        </Alert>
    );
}

// RestoresPanel lists the newest restores, each with its output.
export function RestoresPanel({ app, restores }: { app: App; restores: RestoreSummary[] }) {
    const [opened, setOpened] = useState<RestoreSummary | null>(null);
    if (restores.length === 0) {
        return null;
    }

    return (
        <Panel title="Restores" description={`Into ${app.name}, newest first.`} flush>
            <Table>
                <TableHeader>
                    <TableRow>
                        <TableHead className="w-8">
                            <span className="sr-only">Status</span>
                        </TableHead>
                        <TableHead>Started</TableHead>
                        <TableHead className="hidden md:table-cell">From</TableHead>
                        <TableHead className="hidden lg:table-cell">Safety backup</TableHead>
                        <TableHead className="hidden text-right sm:table-cell">Took</TableHead>
                        <TableHead>
                            <span className="sr-only">Actions</span>
                        </TableHead>
                    </TableRow>
                </TableHeader>
                <TableBody>
                    {restores.map((r) => (
                        <TableRow key={r.id}>
                            <TableCell className="pr-0">
                                <RunStatusBadge status={r.status} quiet />
                            </TableCell>
                            <TableCell className="w-full max-w-0">
                                <div className="flex items-baseline gap-2">
                                    <span title={formatDateTime(r.started_at)}>{timeAgo(r.started_at)}</span>
                                    {r.new_app && (
                                        <span className="truncate text-xs text-muted-foreground">
                                            Into this new app
                                        </span>
                                    )}
                                </div>
                                <p className="truncate font-mono text-xs text-muted-foreground" title={r.name}>
                                    {r.name}
                                </p>
                                {r.message && r.status !== 'succeeded' && (
                                    <p className="line-clamp-2 text-xs text-muted-foreground" title={r.message}>
                                        {r.message}
                                    </p>
                                )}
                            </TableCell>
                            <TableCell className="hidden whitespace-nowrap md:table-cell">
                                {r.uploaded ? 'A file' : r.location}
                                {r.backup_id && (
                                    <span className="text-xs text-muted-foreground"> · backup #{r.backup_id}</span>
                                )}
                            </TableCell>
                            <TableCell className="hidden whitespace-nowrap text-muted-foreground lg:table-cell">
                                {r.new_app ? '—' : r.safety_backup_id ? `#${r.safety_backup_id}` : 'None'}
                            </TableCell>
                            <TableCell className="hidden text-right text-muted-foreground tabular-nums sm:table-cell">
                                {duration(r.started_at, r.finished_at)}
                            </TableCell>
                            <TableCell className="py-1.5 text-right">
                                <Button variant="ghost" size="sm" onClick={() => setOpened(r)} aria-label="Details">
                                    <FileTextIcon />
                                    <span className="hidden sm:inline">Details</span>
                                </Button>
                            </TableCell>
                        </TableRow>
                    ))}
                </TableBody>
            </Table>
            <RestoreOutput app={app} restore={opened} onClose={() => setOpened(null)} />
        </Panel>
    );
}

function RestoreOutput({ app, restore, onClose }: { app: App; restore: RestoreSummary | null; onClose: () => void }) {
    const [detail, setDetail] = useState<RestoreDetail | null>(null);
    const id = restore?.id;
    const status = restore?.status;

    // biome-ignore lint/correctness/useExhaustiveDependencies: status changes when the restore ends, which is when its output is complete
    useEffect(() => {
        setDetail(null);
        if (id === undefined) {
            return;
        }
        let cancelled = false;
        fetch(`/apps/${app.id}/restores/${id}`, { headers: { Accept: 'application/json' } })
            .then((response) => (response.ok ? response.json() : null))
            .then((body: RestoreDetail | null) => {
                if (!cancelled) {
                    setDetail(body);
                }
            });
        return () => {
            cancelled = true;
        };
    }, [app.id, id, status]);

    return (
        <Dialog open={restore !== null} onOpenChange={(open) => !open && onClose()}>
            <DialogContent className="sm:max-w-2xl">
                <DialogHeader>
                    <DialogTitle>Restore #{restore?.id}</DialogTitle>
                    <DialogDescription>
                        {restore &&
                            `${formatDateTime(restore.started_at)} from ${restore.uploaded ? 'an uploaded file' : restore.location}`}
                    </DialogDescription>
                </DialogHeader>
                {detail === null ? (
                    <p className="text-sm text-muted-foreground">Loading…</p>
                ) : (
                    <div className="grid min-w-0 gap-3 text-sm">
                        {detail.message && <p>{detail.message}</p>}
                        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1">
                            <dt className="text-muted-foreground">
                                {detail.storage_location_id === null ? 'File' : 'Key'}
                            </dt>
                            <dd className="font-mono text-xs break-all">{detail.key}</dd>
                            <dt className="text-muted-foreground">Read</dt>
                            <dd className="tabular-nums">
                                {formatBytes(detail.read_bytes)} of {formatBytes(detail.size_bytes)}
                            </dd>
                        </dl>
                        <pre className="max-h-80 overflow-auto rounded-lg border bg-muted/40 p-3 font-mono text-xs whitespace-pre-wrap break-all">
                            {detail.output || (detail.status === 'running' ? 'Running…' : 'The restore wrote nothing.')}
                        </pre>
                    </div>
                )}
            </DialogContent>
        </Dialog>
    );
}
