import { useForm } from '@inertiajs/react';
import { HistoryIcon, RotateCwIcon, TriangleAlertIcon, UploadIcon } from 'lucide-react';
import { type FormEvent, useCallback, useEffect, useRef, useState } from 'react';

import { BackupFacts, Replaces } from '@/Components/AppRestores';
import { Refusal } from '@/Components/NewAppRestore';
import { Panel } from '@/Components/Panel';
import { Alert, AlertAction, AlertDescription, AlertTitle } from '@/Components/ui/alert';
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
import { formatBytes } from '@/lib/format';
import { xsrfToken } from '@/lib/http';
import type { App, BackupsSection, RestorePreview, StorageChoice } from '@/types/models';

// How much of the file goes first, for the preview: templates.DumpHeadSize.
const headSize = 64 << 10;

// How often a long upload keeps the session alive, well inside its
// two-hour lifetime.
const keepAlive = 5 * 60_000;

// RestoreFromFile restores a dump this browser has, from Export or a
// backup's download, into the app (ADR 0034). blocked says why it can't
// start now.
export function RestoreFromFile({
    app,
    section,
    blocked,
}: {
    app: App;
    section: BackupsSection;
    blocked: string | null;
}) {
    const [file, setFile] = useState<File | null>(null);
    const input = useRef<HTMLInputElement>(null);
    const extension = section.exports[0]?.extension;

    return (
        <Panel
            title="Restore from a file"
            description={
                <>
                    Uploads the {extension ? <code className="font-mono text-xs">.{extension}</code> : 'dump'} file that
                    Export or a backup&apos;s download made, checks it, then restores it into the{' '}
                    <code className="font-mono text-xs">{section.database}</code> database. The file is deleted from the
                    server when the restore ends.
                </>
            }
        >
            <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
                <input
                    ref={input}
                    type="file"
                    className="hidden"
                    aria-label="File to restore"
                    onChange={(e) => {
                        setFile(e.target.files?.[0] ?? null);
                        e.target.value = '';
                    }}
                />
                <Button variant="outline" disabled={blocked !== null} onClick={() => input.current?.click()}>
                    <UploadIcon />
                    Choose a file
                </Button>
                {blocked && <p className="text-sm text-muted-foreground">{blocked}</p>}
            </div>
            <FileRestoreDialog app={app} file={file} locations={section.locations} onClose={() => setFile(null)} />
        </Panel>
    );
}

type Upload = { id: string; chunk: number };

// Sending is how much of the file the server has, and whether more is on
// its way. error is why sending stopped.
type Sending = { sent: number; going: boolean; error: string | null };

// FileRestoreDialog uploads the file in chunks. The start goes first, for
// the preview, and the rest while the restore is read and confirmed.
function FileRestoreDialog({
    app,
    file,
    locations,
    onClose,
}: {
    app: App;
    file: File | null;
    locations: StorageChoice[];
    onClose: () => void;
}) {
    const [upload, setUpload] = useState<Upload | null>(null);
    const [sending, setSending] = useState<Sending>({ sent: 0, going: false, error: null });
    const [preview, setPreview] = useState<RestorePreview | null>(null);
    const [problem, setProblem] = useState<string | null>(null);
    const signal = useRef<AbortSignal | null>(null);
    const form = useForm({
        upload: '',
        safety: locations.length > 0,
        safety_location: locations[0]?.id ?? 0,
        confirm: '',
    });

    const sendRest = useCallback(
        async (file: File, up: Upload, from: number, abort: AbortSignal) => {
            let sent = from;
            setSending({ sent, going: true, error: null });
            try {
                while (sent < file.size) {
                    sent = await putChunk(app.id, up.id, file, sent, Math.min(file.size, sent + up.chunk), abort);
                    setSending({ sent, going: true, error: null });
                }
                setSending({ sent, going: false, error: null });
            } catch (e) {
                if (!abort.aborted) {
                    setSending({ sent, going: false, error: message(e) });
                }
            }
        },
        [app.id],
    );

    // biome-ignore lint/correctness/useExhaustiveDependencies: a new file starts a new upload
    useEffect(() => {
        setUpload(null);
        setSending({ sent: 0, going: false, error: null });
        setPreview(null);
        setProblem(null);
        if (file === null) {
            return;
        }
        form.reset();
        form.clearErrors();
        const controller = new AbortController();
        signal.current = controller.signal;
        let id = '';
        (async () => {
            try {
                const up = await startUpload(app.id, file, controller.signal);
                id = up.id;
                setUpload(up);
                form.setData('upload', up.id);
                const sent = await putChunk(app.id, up.id, file, 0, Math.min(file.size, headSize), controller.signal);
                setSending({ sent, going: sent < file.size, error: null });
                const checked = await getPreview(app.id, up.id, controller.signal);
                setPreview(checked);
                if (checked.refusal) {
                    controller.abort();
                    cancelUpload(app.id, up.id);
                    return;
                }
                await sendRest(file, up, sent, controller.signal);
            } catch (e) {
                if (!controller.signal.aborted) {
                    setProblem(message(e));
                    setSending((s) => ({ ...s, going: false }));
                }
            }
        })();

        // Closing the dialog cancels the upload. One its restore has taken
        // is no longer an upload, so that's harmless.
        return () => {
            controller.abort();
            if (id) {
                cancelUpload(app.id, id);
            }
        };
    }, [app.id, file]);

    // Chunks never save the session (routes/streams.go), so a long upload
    // checks in through the web group now and then to keep it.
    useEffect(() => {
        if (upload === null || !sending.going) {
            return;
        }
        const timer = window.setInterval(() => {
            fetch(`/apps/${app.id}/restores/uploads/${upload.id}`, {
                credentials: 'same-origin',
                headers: { Accept: 'application/json' },
            }).catch(() => {});
        }, keepAlive);
        return () => window.clearInterval(timer);
    }, [app.id, upload, sending.going]);

    const submit = (e: FormEvent) => {
        e.preventDefault();
        form.post(`/apps/${app.id}/restores`, { preserveState: true, preserveScroll: true, onSuccess: onClose });
    };
    const errors = form.errors as Record<string, string | undefined>;
    const uploaded = file !== null && sending.sent === file.size;
    const ready = preview !== null && !preview.refusal && uploaded && form.data.confirm === app.name;

    return (
        <Dialog open={file !== null} onOpenChange={(open) => !open && onClose()}>
            <DialogContent className="sm:max-w-xl">
                <form onSubmit={submit} className="grid min-w-0 gap-4">
                    <DialogHeader>
                        <DialogTitle>Restore {app.name}</DialogTitle>
                        <DialogDescription className="break-all">
                            {problem
                                ? 'This file can’t be restored.'
                                : preview
                                  ? `From ${preview.name}, uploaded from this browser.`
                                  : `Reading ${file?.name ?? 'the file'}…`}
                        </DialogDescription>
                    </DialogHeader>
                    {problem && <Refusal reason={problem} />}
                    {preview && <BackupFacts preview={preview} newApp={false} />}
                    {preview && !preview.refusal && file && (
                        <UploadProgress
                            sending={sending}
                            size={file.size}
                            onRetry={() =>
                                upload && signal.current && sendRest(file, upload, sending.sent, signal.current)
                            }
                        />
                    )}
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
                                <SafetyBackup
                                    app={app}
                                    locations={locations}
                                    safety={form.data.safety}
                                    location={form.data.safety_location}
                                    error={errors.safety_location}
                                    onSafety={(checked) => form.setData('safety', checked)}
                                    onLocation={(id) => form.setData('safety_location', id)}
                                />
                                <Field data-invalid={!!errors.confirm || undefined}>
                                    <FieldLabel htmlFor="file-restore-confirm">
                                        Type <span className="font-mono">{app.name}</span> to confirm
                                    </FieldLabel>
                                    <Input
                                        id="file-restore-confirm"
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
            </DialogContent>
        </Dialog>
    );
}

function UploadProgress({ sending, size, onRetry }: { sending: Sending; size: number; onRetry: () => void }) {
    const share = size > 0 ? sending.sent / size : 1;
    const done = sending.sent === size;
    if (sending.error) {
        return (
            <Alert variant="destructive">
                <TriangleAlertIcon />
                <AlertTitle>
                    The upload stopped at {formatBytes(sending.sent)} of {formatBytes(size)}
                </AlertTitle>
                <AlertDescription>{sending.error}</AlertDescription>
                <AlertAction>
                    <Button type="button" size="sm" variant="outline" onClick={onRetry}>
                        <RotateCwIcon />
                        Try again
                    </Button>
                </AlertAction>
            </Alert>
        );
    }

    return (
        <div className="grid gap-1.5 text-sm">
            <p className="text-muted-foreground">
                {done
                    ? `Uploaded all ${formatBytes(size)}. Nothing has changed yet.`
                    : `Uploading ${formatBytes(sending.sent)} of ${formatBytes(size)}. Nothing changes until you restore.`}
            </p>
            <div
                className="h-1.5 overflow-hidden rounded-full bg-muted"
                role="progressbar"
                aria-label="Upload"
                aria-valuemin={0}
                aria-valuemax={100}
                aria-valuenow={Math.round(share * 100)}
            >
                <div className="h-full bg-primary transition-[width]" style={{ width: `${share * 100}%` }} />
            </div>
        </div>
    );
}

// SafetyBackup backs the app up to a storage location before the restore.
// With none, it says so, and Export is the way to keep a copy.
function SafetyBackup({
    app,
    locations,
    safety,
    location,
    error,
    onSafety,
    onLocation,
}: {
    app: App;
    locations: StorageChoice[];
    safety: boolean;
    location: number;
    error: string | undefined;
    onSafety: (checked: boolean) => void;
    onLocation: (id: number) => void;
}) {
    if (locations.length === 0) {
        return (
            <p className="text-sm text-muted-foreground">
                There&apos;s no storage location, so there&apos;s no safety backup. Export {app.name} first to keep a
                copy of what this replaces.
            </p>
        );
    }
    const chosen = locations.find((l) => l.id === location);

    return (
        <Field data-invalid={!!error || undefined}>
            <label className="flex items-start gap-2 text-sm">
                <Checkbox
                    className="mt-0.5"
                    aria-labelledby="file-safety-label"
                    checked={safety}
                    onCheckedChange={(checked) => onSafety(checked)}
                />
                <span>
                    <span id="file-safety-label" className="font-medium">
                        Back up {app.name} first
                    </span>
                    <span className="block text-muted-foreground">
                        {safety
                            ? `To ${chosen?.name ?? 'a storage location'}, so you can go back. If this backup fails, nothing is restored.`
                            : 'Without it, the data the restore replaces can’t be got back.'}
                    </span>
                </span>
            </label>
            {safety && locations.length > 1 && (
                <Select
                    items={locations.map((l) => ({ value: l.id, label: l.name }))}
                    value={location}
                    onValueChange={(value) => value !== null && onLocation(value)}
                >
                    <SelectTrigger aria-label="Safety backup location" className="w-full sm:w-64">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        {locations.map((l) => (
                            <SelectItem key={l.id} value={l.id}>
                                {l.name}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
            )}
            {error && <FieldError>{error}</FieldError>}
        </Field>
    );
}

// UploadFailed is a request the dialog doesn't send again: the server
// refused it, in words for the page.
class UploadFailed extends Error {}

const message = (e: unknown) => (e instanceof Error ? e.message : 'The upload failed.');

async function startUpload(appId: number, file: File, signal: AbortSignal): Promise<Upload> {
    const response = await fetch(`/apps/${appId}/restores/uploads`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'X-XSRF-TOKEN': xsrfToken() },
        body: JSON.stringify({ name: file.name, size: file.size }),
        signal,
    });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) {
        throw new UploadFailed(body.error ?? refused(response.status));
    }

    return body;
}

async function getPreview(appId: number, id: string, signal: AbortSignal): Promise<RestorePreview> {
    const response = await fetch(`/apps/${appId}/restores/preview?upload=${id}`, {
        headers: { Accept: 'application/json' },
        signal,
    });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) {
        throw new UploadFailed(body.error ?? 'Couldn’t check this file.');
    }

    return body;
}

// putChunk sends file's bytes from start to end, and returns how much of
// the file the server has. A dropped connection or a server error is sent
// again, a few times, a little later each time.
async function putChunk(
    appId: number,
    id: string,
    file: File,
    start: number,
    end: number,
    signal: AbortSignal,
): Promise<number> {
    let reason = 'The connection dropped.';
    for (let attempt = 0; attempt < 5; attempt++) {
        if (attempt > 0) {
            await wait(1000 * 2 ** (attempt - 1), signal);
        }
        let response: Response;
        try {
            response = await fetch(`/apps/${appId}/restores/uploads/${id}?offset=${start}`, {
                method: 'PUT',
                credentials: 'same-origin',
                headers: {
                    Accept: 'application/json',
                    'Content-Type': 'application/octet-stream',
                    'X-XSRF-TOKEN': xsrfToken(),
                },
                body: file.slice(start, end),
                signal,
            });
        } catch (e) {
            if (signal.aborted) {
                throw e;
            }
            continue;
        }
        const body = await response.json().catch(() => ({}));
        // 409 says where the server has got to, which is where to go on.
        if (response.ok || response.status === 409) {
            return body.received;
        }
        reason = body.error ?? refused(response.status);
        if (response.status < 500 && response.status !== 400) {
            throw new UploadFailed(reason);
        }
    }

    throw new UploadFailed(reason);
}

function cancelUpload(appId: number, id: string) {
    fetch(`/apps/${appId}/restores/uploads/${id}`, {
        method: 'DELETE',
        credentials: 'same-origin',
        headers: { Accept: 'application/json', 'X-XSRF-TOKEN': xsrfToken() },
        keepalive: true,
    }).catch(() => {});
}

function refused(status: number): string {
    switch (status) {
        case 401:
            return 'You were signed out. Sign in again, then choose the file again.';
        case 419:
            return 'This page expired. Reload it, then choose the file again.';
        default:
            return `The server answered ${status}.`;
    }
}

function wait(ms: number, signal: AbortSignal): Promise<void> {
    return new Promise((resolve, reject) => {
        const timer = window.setTimeout(resolve, ms);
        signal.addEventListener('abort', () => {
            window.clearTimeout(timer);
            reject(signal.reason);
        });
    });
}
