import { Link, useForm } from '@inertiajs/react';
import { DatabaseBackupIcon, FolderSearchIcon, TriangleAlertIcon } from 'lucide-react';
import { type FormEvent, useState } from 'react';

import { Panel } from '@/Components/Panel';
import { TextField } from '@/Components/TextField';
import { Alert, AlertDescription } from '@/Components/ui/alert';
import { Button } from '@/Components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/Components/ui/select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/Components/ui/table';
import { formatBytes } from '@/lib/format';
import { formatDateTime, timeAgo } from '@/lib/time';
import type { BucketBackup, NewAppPreview, StorageChoice } from '@/types/models';

// RestoreSource is what a restore reads: a backup in an app's history, or a
// manifest found in a location's folder.
export type RestoreSource = { backup: number } | { location: number; key: string };

export const sourceQuery = (source: RestoreSource) =>
    new URLSearchParams(
        'backup' in source ? { backup: String(source.backup) } : { location: String(source.location), key: source.key },
    );

export function Refusal({ reason }: { reason: string }) {
    return (
        <Alert variant="destructive">
            <TriangleAlertIcon />
            <AlertDescription>{reason}</AlertDescription>
        </Alert>
    );
}

// getJSON fetches one of the restore endpoints, with their { error } in
// words when it fails.
async function getJSON<T>(
    url: string,
    fallback: string,
): Promise<{ body: T; error: null } | { body: null; error: string }> {
    try {
        const response = await fetch(url, { headers: { Accept: 'application/json' } });
        const body = await response.json();
        return response.ok ? { body, error: null } : { body: null, error: body.error ?? fallback };
    } catch {
        return { body: null, error: fallback };
    }
}

// NewAppRestoreForm creates an app from the backup's template with the
// name and tag given, deploys it, and restores the backup into it once its
// server takes connections (ADR 0034). Nothing is replaced, so there's
// nothing to confirm.
export function NewAppRestoreForm({
    source,
    name,
    tag,
    template,
    onCancel,
}: {
    source: RestoreSource;
    name: string;
    tag: string;
    template: string;
    onCancel?: () => void;
}) {
    const form = useForm({
        backup: 'backup' in source ? source.backup : 0,
        location: 'location' in source ? source.location : 0,
        key: 'key' in source ? source.key : '',
        name,
        tag,
    });
    const errors = form.errors as Record<string, string | undefined>;

    const submit = (e: FormEvent) => {
        e.preventDefault();
        form.post('/apps/restore', { preserveState: true, preserveScroll: true });
    };

    return (
        <form onSubmit={submit} className="grid min-w-0 gap-4">
            <div className="grid gap-4 sm:grid-cols-2">
                <TextField
                    id="restore-name"
                    label="New app's name"
                    className="font-mono"
                    autoComplete="off"
                    spellCheck={false}
                    value={form.data.name}
                    onChange={(e) => form.setData('name', e.target.value)}
                    error={form.errors.name}
                />
                <TextField
                    id="restore-tag"
                    label={`${template} tag`}
                    className="font-mono"
                    autoComplete="off"
                    spellCheck={false}
                    value={form.data.tag}
                    onChange={(e) => form.setData('tag', e.target.value)}
                    error={form.errors.tag}
                    description="The backup's version or a newer one."
                />
            </div>
            <p className="text-sm text-muted-foreground">
                Echowa creates the app, deploys it, and restores the backup once its server takes connections. The new
                app&apos;s Backups section shows how far it has got, and what&apos;s left if a step fails.
            </p>
            {errors.source && <Refusal reason={errors.source} />}
            <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
                {onCancel && (
                    <Button type="button" variant="outline" onClick={onCancel}>
                        Cancel
                    </Button>
                )}
                <Button type="submit" disabled={form.processing || !form.data.name || !form.data.tag}>
                    <DatabaseBackupIcon />
                    Create and restore
                </Button>
            </div>
        </form>
    );
}

// FromBackup is the New app page's From a backup: a location, an app's
// folder in it, a backup, then the new app. It needs nothing but the
// bucket, so it works after losing a server.
export function FromBackup({ locations }: { locations: StorageChoice[] }) {
    const [location, setLocation] = useState<number | null>(null);
    const [folders, setFolders] = useState<string[] | null>(null);
    const [folder, setFolder] = useState<string | null>(null);
    const [list, setList] = useState<BucketBackup[] | null>(null);
    const [picked, setPicked] = useState<{ key: string; preview: NewAppPreview | null } | null>(null);
    const [error, setError] = useState<string | null>(null);
    const [loading, setLoading] = useState(false);

    if (locations.length === 0) {
        return (
            <Panel title="From a backup">
                <p className="text-sm text-muted-foreground">
                    Backups are read from an S3 storage location. Add the one your backups are in on{' '}
                    <Link href="/settings" className="font-medium text-foreground underline underline-offset-4">
                        Settings
                    </Link>
                    , with the same bucket and prefix, then come back here.
                </p>
            </Panel>
        );
    }

    const pickLocation = async (id: number | null) => {
        setLocation(id);
        setFolders(null);
        setFolder(null);
        setList(null);
        setPicked(null);
        setError(null);
        if (id === null) {
            return;
        }
        setLoading(true);
        const result = await getJSON<{ folders: string[] }>(
            `/backups/browse?location=${id}`,
            'Couldn’t list the bucket.',
        );
        setLoading(false);
        setError(result.error);
        setFolders(result.body?.folders ?? null);
    };

    const pickFolder = async (name: string | null) => {
        setFolder(name);
        setList(null);
        setPicked(null);
        setError(null);
        if (name === null || location === null) {
            return;
        }
        setLoading(true);
        const query = new URLSearchParams({ location: String(location), folder: name });
        const result = await getJSON<{ backups: BucketBackup[] }>(
            `/backups/browse?${query}`,
            'Couldn’t list the bucket.',
        );
        setLoading(false);
        setError(result.error);
        setList(result.body?.backups ?? null);
    };

    const pickBackup = async (key: string) => {
        if (location === null) {
            return;
        }
        setPicked({ key, preview: null });
        setError(null);
        const result = await getJSON<NewAppPreview>(
            `/backups/preview?${new URLSearchParams({ location: String(location), key })}`,
            'Couldn’t read this backup.',
        );
        setError(result.error);
        setPicked(result.body ? { key, preview: result.body } : null);
    };

    const preview = picked?.preview ?? null;

    return (
        <>
            <Panel
                title="Choose a backup"
                description="Backups are kept in folders by app name. After losing a server, add the same storage location to this one and its backups are here."
                flush
            >
                <div className="grid gap-3 border-b px-4 py-3 sm:grid-cols-2">
                    <Select
                        items={locations.map((l) => ({ value: l.id, label: l.name }))}
                        value={location}
                        onValueChange={pickLocation}
                    >
                        <SelectTrigger aria-label="Storage location" className="w-full">
                            <SelectValue placeholder="Choose a location" />
                        </SelectTrigger>
                        <SelectContent>
                            {locations.map((l) => (
                                <SelectItem key={l.id} value={l.id}>
                                    {l.name}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                    {folders && folders.length > 0 && (
                        <Select
                            items={folders.map((f) => ({ value: f, label: f }))}
                            value={folder}
                            onValueChange={pickFolder}
                        >
                            <SelectTrigger aria-label="App" className="w-full">
                                <SelectValue placeholder="Choose an app" />
                            </SelectTrigger>
                            <SelectContent>
                                {folders.map((f) => (
                                    <SelectItem key={f} value={f} className="font-mono">
                                        {f}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    )}
                </div>
                {error && <p className="px-4 py-6 text-sm text-destructive">{error}</p>}
                {!error && (folders === null || (folders.length > 0 && list === null)) && (
                    <p className="flex items-center gap-2 px-4 py-6 text-sm text-muted-foreground">
                        <FolderSearchIcon className="size-4" />
                        {loading ? 'Looking…' : folders === null ? 'Choose a location to look in.' : 'Choose an app.'}
                    </p>
                )}
                {!error && folders?.length === 0 && (
                    <p className="px-4 py-6 text-sm text-muted-foreground">No backups in this location.</p>
                )}
                {!error && list?.length === 0 && (
                    <p className="px-4 py-6 text-sm text-muted-foreground">
                        No finished backups of {folder}. A dump without its manifest is left out.
                    </p>
                )}
                {!error && list && list.length > 0 && (
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>Dump</TableHead>
                                <TableHead className="hidden sm:table-cell">Uploaded</TableHead>
                                <TableHead className="text-right">Size</TableHead>
                                <TableHead className="text-right">
                                    <span className="sr-only">Actions</span>
                                </TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {list.map((b) => (
                                <TableRow key={b.key} data-state={picked?.key === b.key ? 'selected' : undefined}>
                                    <TableCell className="w-full max-w-0">
                                        <div className="truncate font-mono text-xs" title={b.name}>
                                            {b.name}
                                        </div>
                                        <div className="text-xs text-muted-foreground sm:hidden">
                                            {timeAgo(b.modified)}
                                        </div>
                                    </TableCell>
                                    <TableCell
                                        className="hidden text-sm sm:table-cell"
                                        title={formatDateTime(b.modified)}
                                    >
                                        {timeAgo(b.modified)}
                                    </TableCell>
                                    <TableCell className="text-right tabular-nums">
                                        {formatBytes(b.size_bytes)}
                                    </TableCell>
                                    <TableCell className="text-right">
                                        <Button
                                            variant={picked?.key === b.key ? 'default' : 'outline'}
                                            size="sm"
                                            onClick={() => pickBackup(b.key)}
                                        >
                                            {picked?.key === b.key ? 'Chosen' : 'Choose'}
                                        </Button>
                                    </TableCell>
                                </TableRow>
                            ))}
                        </TableBody>
                    </Table>
                )}
            </Panel>
            {picked && location !== null && (
                <Panel
                    title={preview ? `Restore into a new ${preview.template_name} app` : 'Reading the backup…'}
                    description={preview ? `From ${preview.name} in ${preview.location}.` : undefined}
                >
                    {preview && (
                        <div className="grid gap-4">
                            <NewAppFacts preview={preview} />
                            <NewAppRestoreForm
                                key={picked.key}
                                source={{ location, key: picked.key }}
                                name={preview.app}
                                tag={preview.default_tag}
                                template={preview.template_name}
                            />
                        </div>
                    )}
                </Panel>
            )}
        </>
    );
}

function NewAppFacts({ preview }: { preview: NewAppPreview }) {
    return (
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 rounded-lg border bg-muted/40 px-3 py-2 text-sm">
            <dt className="text-muted-foreground">App</dt>
            <dd className="font-mono text-xs leading-5 break-all">{preview.app}</dd>
            <dt className="text-muted-foreground">Made</dt>
            <dd>
                {formatDateTime(preview.started_at)}{' '}
                <span className="text-muted-foreground">({timeAgo(preview.started_at)})</span>
            </dd>
            <dt className="text-muted-foreground">Size</dt>
            <dd className="tabular-nums">{formatBytes(preview.size_bytes)}</dd>
            <dt className="text-muted-foreground">Database</dt>
            <dd className="font-mono text-xs leading-5 break-all">{preview.database}</dd>
            <dt className="text-muted-foreground">{preview.template_name}</dt>
            <dd className="font-mono text-xs leading-5">{preview.server_version}</dd>
        </dl>
    );
}
