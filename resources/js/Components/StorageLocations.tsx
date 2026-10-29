import { router, useForm } from '@inertiajs/react';
import {
    CircleCheckIcon,
    CircleXIcon,
    DatabaseBackupIcon,
    Loader2Icon,
    PencilIcon,
    PlusIcon,
    Trash2Icon,
} from 'lucide-react';
import { type FormEvent, type ReactElement, useState } from 'react';

import { ConfirmDelete } from '@/Components/ConfirmDelete';
import { Panel } from '@/Components/Panel';
import { TextField } from '@/Components/TextField';
import { Button } from '@/Components/ui/button';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
    DialogTrigger,
} from '@/Components/ui/dialog';
import { Field, FieldLabel } from '@/Components/ui/field';
import { Switch } from '@/Components/ui/switch';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/Components/ui/table';
import { postJson, RequestError } from '@/lib/http';
import { cn } from '@/lib/utils';
import type { StorageCheckStep, StorageLocation } from '@/types/models';

interface LocationForm {
    name: string;
    endpoint: string;
    region: string;
    bucket: string;
    prefix: string;
    access_key_id: string;
    secret_key: string;
    path_style: boolean;
}

// StorageLocations is Settings' S3 storage section (ADR 0034): the buckets
// database backups go to. Secret keys are sent, never shown.
export function StorageLocations({ locations, prefix }: { locations: StorageLocation[]; prefix: string }) {
    return (
        <Panel
            flush={locations.length > 0}
            title="S3 storage"
            description="Buckets that database backups go to. Any S3-compatible provider works: Cloudflare R2, Backblaze B2, Wasabi, Hetzner, AWS."
            actions={
                locations.length > 0 && (
                    <LocationDialog
                        prefix={prefix}
                        trigger={
                            <Button variant="outline">
                                <PlusIcon />
                                Add location
                            </Button>
                        }
                    />
                )
            }
        >
            {locations.length === 0 ? (
                <div className="flex flex-col items-center rounded-xl border border-dashed px-6 py-10 text-center">
                    <span className="mb-4 flex size-11 items-center justify-center rounded-xl border bg-card">
                        <DatabaseBackupIcon className="size-5 text-muted-foreground" />
                    </span>
                    <h2 className="font-medium">No storage yet</h2>
                    <p className="mt-1 max-w-md text-sm text-muted-foreground">
                        Create a bucket and an access key limited to it with your provider, then add them here.
                    </p>
                    <LocationDialog prefix={prefix} trigger={<Button className="mt-5">Add a location</Button>} />
                </div>
            ) : (
                <Table>
                    <TableHeader>
                        <TableRow>
                            <TableHead>Name</TableHead>
                            <TableHead className="hidden sm:table-cell">Bucket</TableHead>
                            <TableHead className="hidden md:table-cell">Endpoint</TableHead>
                            <TableHead className="hidden text-right lg:table-cell">Schedules</TableHead>
                            <TableHead className="hidden text-right lg:table-cell">Backups</TableHead>
                            <TableHead className="text-right">Actions</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {locations.map((location) => (
                            <TableRow key={location.id}>
                                <TableCell>
                                    <div className="font-medium">{location.name}</div>
                                    <div className="font-mono text-xs text-muted-foreground sm:hidden">
                                        {bucketPath(location)}
                                    </div>
                                </TableCell>
                                <TableCell className="hidden font-mono text-xs sm:table-cell">
                                    {bucketPath(location)}
                                </TableCell>
                                <TableCell className="hidden font-mono text-xs md:table-cell">
                                    {host(location.endpoint)}
                                </TableCell>
                                <TableCell className="hidden text-right tabular-nums lg:table-cell">
                                    {location.schedules}
                                </TableCell>
                                <TableCell className="hidden text-right tabular-nums lg:table-cell">
                                    {location.backups}
                                </TableCell>
                                <TableCell>
                                    <div className="flex justify-end gap-1">
                                        <LocationDialog
                                            location={location}
                                            prefix={prefix}
                                            trigger={
                                                <Button
                                                    variant="ghost"
                                                    size="icon-sm"
                                                    aria-label={`Edit ${location.name}`}
                                                    title="Edit"
                                                >
                                                    <PencilIcon />
                                                </Button>
                                            }
                                        />
                                        <ConfirmDelete
                                            title={`Delete ${location.name}?`}
                                            trigger={
                                                <Button
                                                    variant="ghost"
                                                    size="icon-sm"
                                                    className="text-destructive"
                                                    aria-label={`Delete ${location.name}`}
                                                    // A schedule needs it; the server refuses too.
                                                    disabled={location.schedules > 0}
                                                    title={
                                                        location.schedules > 0
                                                            ? 'Backup schedules use it. Change or delete them first.'
                                                            : 'Delete'
                                                    }
                                                >
                                                    <Trash2Icon />
                                                </Button>
                                            }
                                            onConfirm={() =>
                                                router.delete(`/settings/storage/${location.id}`, {
                                                    preserveScroll: true,
                                                })
                                            }
                                        >
                                            Echowa forgets this bucket and its keys
                                            {location.backups > 0 &&
                                                `, and its records of ${location.backups === 1 ? '1 backup' : `${location.backups} backups`}`}
                                            . Nothing in the bucket is deleted, so a backup there can still be restored
                                            once you add it again.
                                        </ConfirmDelete>
                                    </div>
                                </TableCell>
                            </TableRow>
                        ))}
                    </TableBody>
                </Table>
            )}
        </Panel>
    );
}

function bucketPath(location: StorageLocation) {
    return location.prefix ? `${location.bucket}/${location.prefix}` : location.bucket;
}

function host(endpoint: string) {
    try {
        return new URL(endpoint).host;
    } catch {
        return endpoint;
    }
}

function LocationDialog({
    location,
    prefix,
    trigger,
}: {
    location?: StorageLocation;
    prefix: string;
    trigger: ReactElement;
}) {
    const [open, setOpen] = useState(false);
    const defaults: LocationForm = location
        ? {
              name: location.name,
              endpoint: location.endpoint,
              region: location.region,
              bucket: location.bucket,
              prefix: location.prefix,
              access_key_id: location.access_key_id,
              secret_key: '',
              path_style: location.path_style,
          }
        : {
              name: '',
              endpoint: '',
              region: '',
              bucket: '',
              prefix,
              access_key_id: '',
              secret_key: '',
              path_style: false,
          };
    const form = useForm<LocationForm>(defaults);
    const [checking, setChecking] = useState(false);
    const [steps, setSteps] = useState<StorageCheckStep[] | null>(null);
    const [checkError, setCheckError] = useState<string>();

    const reset = () => {
        form.setDefaults(defaults);
        form.reset();
        form.clearErrors();
        setSteps(null);
        setCheckError(undefined);
    };
    const submit = (e: FormEvent) => {
        e.preventDefault();
        const options = { preserveScroll: true, onSuccess: () => setOpen(false) };
        if (location) {
            form.put(`/settings/storage/${location.id}`, options);
        } else {
            form.post('/settings/storage', options);
        }
    };
    const check = async () => {
        setChecking(true);
        setSteps(null);
        setCheckError(undefined);
        form.clearErrors();
        try {
            const result = await postJson<{ steps: StorageCheckStep[] }>('/settings/storage/test', {
                ...form.data,
                id: location?.id,
            });
            setSteps(result.steps);
        } catch (e) {
            if (e instanceof RequestError && Object.keys(e.errors).length > 0) {
                form.setError(e.errors as Partial<Record<keyof LocationForm, string>>);
            } else {
                setCheckError(e instanceof Error ? e.message : String(e));
            }
        }
        setChecking(false);
    };
    // A location with backups keeps its endpoint and bucket: their records
    // name objects there.
    const moored = (location?.backups ?? 0) > 0;
    const field = (key: keyof LocationForm) => ({
        value: form.data[key] as string,
        onChange: (e: React.ChangeEvent<HTMLInputElement>) => form.setData(key, e.target.value),
        error: form.errors[key],
    });

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
            <DialogTrigger render={trigger} />
            <DialogContent className="max-h-[calc(100dvh-2rem)] grid-rows-[minmax(0,1fr)] sm:max-w-lg">
                <form onSubmit={submit} className="grid min-h-0 grid-rows-[auto_minmax(0,1fr)_auto] gap-4">
                    <DialogHeader>
                        <DialogTitle>{location ? `Edit ${location.name}` : 'Add S3 storage'}</DialogTitle>
                        <DialogDescription>
                            Test checks that Echowa can write, read, list and delete in the bucket with these keys.
                        </DialogDescription>
                    </DialogHeader>

                    <div className="-mx-4 grid min-h-0 content-start gap-5 overflow-y-auto px-4 py-0.5">
                        <TextField id="name" label="Name" placeholder="Cloudflare R2" {...field('name')} />

                        <TextField
                            id="endpoint"
                            label="Endpoint"
                            className="font-mono"
                            placeholder="https://<account>.r2.cloudflarestorage.com"
                            autoComplete="off"
                            description={
                                <>
                                    R2: <code>https://&lt;account&gt;.r2.cloudflarestorage.com</code>. B2:{' '}
                                    <code>https://s3.&lt;region&gt;.backblazeb2.com</code>. AWS:{' '}
                                    <code>https://s3.&lt;region&gt;.amazonaws.com</code>.
                                </>
                            }
                            {...field('endpoint')}
                            disabled={moored}
                        />

                        <div className="grid gap-5 sm:grid-cols-2">
                            <TextField
                                id="bucket"
                                label="Bucket"
                                className="font-mono"
                                autoComplete="off"
                                description={moored ? "Backups name this bucket, so it can't change." : undefined}
                                {...field('bucket')}
                                disabled={moored}
                            />
                            <TextField
                                id="region"
                                label="Region"
                                className="font-mono"
                                placeholder="auto"
                                autoComplete="off"
                                description={
                                    <>
                                        <code>auto</code> on R2.
                                    </>
                                }
                                {...field('region')}
                            />
                        </div>

                        <TextField
                            id="prefix"
                            label="Folder"
                            className="font-mono"
                            placeholder="Optional"
                            autoComplete="off"
                            description={
                                <>
                                    Backups go in{' '}
                                    <code>
                                        {form.data.prefix.replace(/^\/+|\/+$/g, '') || '(the bucket root)'}/&lt;app&gt;/
                                    </code>
                                    . A folder per server lets several share a bucket.
                                </>
                            }
                            {...field('prefix')}
                        />

                        <TextField
                            id="access_key_id"
                            label="Access key ID"
                            className="font-mono"
                            autoComplete="off"
                            {...field('access_key_id')}
                        />

                        <TextField
                            id="secret_key"
                            label="Secret access key"
                            type="password"
                            className="font-mono"
                            autoComplete="new-password"
                            placeholder={location ? 'Leave blank to keep the stored key' : undefined}
                            {...field('secret_key')}
                        />

                        <Field orientation="horizontal">
                            <Switch
                                id="path_style"
                                checked={form.data.path_style}
                                onCheckedChange={(on) => form.setData('path_style', on)}
                            />
                            <FieldLabel htmlFor="path_style" className="font-normal">
                                Path-style addressing: the bucket goes in the URL&apos;s path, not its host name. Some
                                self-hosted servers need it.
                            </FieldLabel>
                        </Field>

                        {checkError && <p className="text-sm text-destructive">{checkError}</p>}
                        {steps && <CheckSteps steps={steps} />}
                    </div>

                    <DialogFooter>
                        <Button type="button" variant="outline" onClick={check} disabled={checking}>
                            {checking && <Loader2Icon className="animate-spin" />}
                            {checking ? 'Testing…' : 'Test'}
                        </Button>
                        <Button type="submit" disabled={form.processing}>
                            {location ? 'Save' : 'Add location'}
                        </Button>
                    </DialogFooter>
                </form>
            </DialogContent>
        </Dialog>
    );
}

function CheckSteps({ steps }: { steps: StorageCheckStep[] }) {
    return (
        <ul className="space-y-2 rounded-lg border bg-muted/40 px-3 py-2.5 text-sm">
            {steps.map((step) => (
                <li key={step.name} className="flex gap-2">
                    {step.error ? (
                        <CircleXIcon className="mt-0.5 size-4 shrink-0 text-destructive" />
                    ) : (
                        <CircleCheckIcon className="mt-0.5 size-4 shrink-0 text-success" />
                    )}
                    <div className="min-w-0">
                        <div className={cn(step.error && 'font-medium')}>{step.name}</div>
                        {step.error && <div className="break-words text-muted-foreground">{step.error}</div>}
                    </div>
                </li>
            ))}
        </ul>
    );
}
