import { router, useForm } from '@inertiajs/react';
import { EllipsisIcon, HardDriveIcon, PencilIcon, PlusIcon, Trash2Icon } from 'lucide-react';
import { type FormEvent, useState } from 'react';

import { ConfirmDelete } from '@/Components/ConfirmDelete';
import { Panel } from '@/Components/Panel';
import { StatusPill } from '@/Components/StatusBadge';
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
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
} from '@/Components/ui/dropdown-menu';
import type { App, AppVolume } from '@/types/models';

// A volume's Docker name is echowa-<app>-<name>, from the server's
// templates.VolumeName.
const dockerName = (app: App, name: string) => `echowa-${app.name}-${name}`;

// volumeNameFrom suggests a name from a path's last segment, in the form
// rules.IsVolumeName accepts: lowercase letters, digits and underscores,
// starting with a letter, at most 32 characters.
export function volumeNameFrom(path: string): string {
    const last = path.split('/').filter(Boolean).pop() ?? '';
    return last
        .toLowerCase()
        .replace(/[^a-z0-9_]+/g, '_')
        .replace(/^[^a-z]+/, '')
        .slice(0, 32)
        .replace(/_+$/, '');
}

// AppVolumes is a custom app's Volumes section: the paths whose files
// survive deploys, restarts and rollbacks (ADR 0039). Each change deploys
// the image the app runs now.
export function AppVolumes({ app, volumes }: { app: App; volumes: AppVolume[] }) {
    const sorted = [...volumes].sort((a, b) => a.target.localeCompare(b.target));
    const add = <AddVolumeDialog app={app} first={volumes.length === 0} trigger={<AddButton />} />;

    if (volumes.length === 0) {
        return (
            <div className="flex flex-col items-center rounded-xl border border-dashed px-6 py-14 text-center">
                <span className="mb-4 flex size-11 items-center justify-center rounded-xl border bg-card">
                    <HardDriveIcon className="size-5 text-muted-foreground" />
                </span>
                <h2 className="font-medium">No volumes</h2>
                <p className="mt-1 max-w-md text-sm text-muted-foreground">
                    Every deploy starts {app.name} from its image, so what it writes to its own filesystem is gone after
                    a redeploy or restart. A volume keeps one path, such as{' '}
                    <code className="font-mono text-xs">/app/storage</code>.
                </p>
                <div className="mt-5">{add}</div>
            </div>
        );
    }

    return (
        <Panel
            title="Volumes"
            description={`Each keeps one path of ${app.name}'s filesystem through deploys, restarts and rollbacks.`}
            actions={add}
            flush
        >
            <ul className="divide-y">
                {sorted.map((volume) => (
                    <VolumeRow key={volume.id} app={app} volume={volume} />
                ))}
            </ul>
        </Panel>
    );
}

function AddButton(props: React.ComponentProps<typeof Button>) {
    return (
        <Button {...props}>
            <PlusIcon />
            Add volume
        </Button>
    );
}

function VolumeRow({ app, volume }: { app: App; volume: AppVolume }) {
    const removing = volume.removed_at !== null;

    return (
        <li className="flex items-center gap-3 px-4 py-3 sm:px-5">
            <div className="min-w-0 flex-1">
                <div
                    className={
                        removing ? 'truncate font-mono text-sm text-muted-foreground' : 'truncate font-mono text-sm'
                    }
                >
                    {volume.target}
                </div>
                <div className="truncate font-mono text-xs text-muted-foreground">{volume.name}</div>
            </div>
            {removing ? (
                <StatusPill tone="warning">Removed on next deploy</StatusPill>
            ) : (
                <VolumeMenu app={app} volume={volume} />
            )}
        </li>
    );
}

function VolumeMenu({ app, volume }: { app: App; volume: AppVolume }) {
    const [moving, setMoving] = useState(false);
    const [removing, setRemoving] = useState(false);

    return (
        <>
            <DropdownMenu>
                <DropdownMenuTrigger
                    render={
                        <Button variant="ghost" size="icon-sm" aria-label={`More for the volume at ${volume.target}`} />
                    }
                >
                    <EllipsisIcon />
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-44">
                    <DropdownMenuItem onClick={() => setMoving(true)}>
                        <PencilIcon />
                        Change path
                    </DropdownMenuItem>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem variant="destructive" onClick={() => setRemoving(true)}>
                        <Trash2Icon />
                        Remove
                    </DropdownMenuItem>
                </DropdownMenuContent>
            </DropdownMenu>
            <ChangePathDialog app={app} volume={volume} open={moving} onOpenChange={setMoving} />
            <ConfirmDelete
                title={`Remove the volume at ${volume.target}?`}
                confirmLabel="Remove volume"
                confirmText={volume.target}
                open={removing}
                onOpenChange={setRemoving}
                onConfirm={() => router.delete(`/apps/${app.id}/volumes/${volume.id}`, { preserveScroll: true })}
            >
                A deploy of the image {app.name} runs now starts straight away without it. Once that succeeds,{' '}
                <code className="font-mono">{volume.name}</code> and everything in it are deleted for good.
            </ConfirmDelete>
        </>
    );
}

// AddVolumeDialog asks for the path, then a name that's suggested from it.
// first is whether the app has no volume yet: its deploys change then.
function AddVolumeDialog({ app, first, trigger }: { app: App; first: boolean; trigger: React.ReactElement }) {
    const [open, setOpen] = useState(false);
    // Once the name is typed in, the path stops suggesting it.
    const [named, setNamed] = useState(false);
    const form = useForm({ target: '', name: '' });

    const reset = () => {
        form.reset();
        form.clearErrors();
        setNamed(false);
    };
    const submit = (e: FormEvent) => {
        e.preventDefault();
        form.post(`/apps/${app.id}/volumes`, { preserveScroll: true, onSuccess: () => setOpen(false) });
    };
    const changePath = (target: string) => {
        form.setData((data) => ({ ...data, target, name: named ? data.name : volumeNameFrom(target) }));
    };
    const oneWriter = first || app.replicas > 1;

    return (
        <Dialog
            open={open}
            onOpenChange={(next) => {
                setOpen(next);
                if (!next) {
                    reset();
                }
            }}
        >
            <DialogTrigger render={trigger} />
            <DialogContent className="max-h-[calc(100dvh-2rem)] grid-rows-[minmax(0,1fr)] sm:max-w-lg">
                <form onSubmit={submit} className="grid min-h-0 grid-rows-[auto_minmax(0,1fr)_auto] gap-4">
                    <DialogHeader>
                        <DialogTitle>Add a volume to {app.name}</DialogTitle>
                        <DialogDescription>
                            Saving deploys the image {app.name} runs now, with the volume mounted.
                        </DialogDescription>
                    </DialogHeader>

                    <div className="-mx-4 grid min-h-0 content-start gap-4 overflow-y-auto px-4 py-0.5">
                        <TextField
                            id="volume-target"
                            label="Path in the container"
                            className="font-mono"
                            placeholder="/app/storage"
                            autoComplete="off"
                            value={form.data.target}
                            onChange={(e) => changePath(e.target.value)}
                            error={form.errors.target}
                        />
                        <TextField
                            id="volume-name"
                            label="Name"
                            className="font-mono"
                            placeholder="storage"
                            autoComplete="off"
                            value={form.data.name}
                            onChange={(e) => {
                                setNamed(true);
                                form.setData('name', e.target.value);
                            }}
                            error={form.errors.name}
                            description={
                                <>
                                    The Docker volume is{' '}
                                    <span className="font-mono">{dockerName(app, form.data.name || 'name')}</span>. It
                                    can&apos;t be renamed later.
                                </>
                            }
                        />

                        <ul className="list-disc space-y-1.5 rounded-lg border bg-muted/40 py-3 pr-3 pl-7 text-sm text-muted-foreground">
                            <li>
                                If the image has files at the path, Docker copies them into the new, empty volume the
                                first time it&apos;s mounted. After that the volume wins.
                            </li>
                            <li>
                                A path the image doesn&apos;t have is created owned by root. An image that runs as
                                another user can&apos;t write there.
                            </li>
                            <li>
                                What the app wrote there before is lost: the deploy that mounts the volume starts from
                                the image, as every deploy does.
                            </li>
                            {oneWriter && (
                                <li>
                                    {app.name} will run one replica, since two tasks can&apos;t share a volume&apos;s
                                    data. Each deploy stops the old task before it starts the new one, so it&apos;s down
                                    for a few seconds.
                                </li>
                            )}
                        </ul>
                    </div>

                    <DialogFooter>
                        <Button type="submit" disabled={form.processing}>
                            Add volume
                        </Button>
                    </DialogFooter>
                </form>
            </DialogContent>
        </Dialog>
    );
}

// ChangePathDialog mounts the volume somewhere else. Its data comes along,
// and its name stays, since that's the Docker volume.
function ChangePathDialog({
    app,
    volume,
    open,
    onOpenChange,
}: {
    app: App;
    volume: AppVolume;
    open: boolean;
    onOpenChange: (open: boolean) => void;
}) {
    const form = useForm({ target: volume.target });

    const submit = (e: FormEvent) => {
        e.preventDefault();
        form.put(`/apps/${app.id}/volumes/${volume.id}`, {
            preserveScroll: true,
            onSuccess: () => onOpenChange(false),
        });
    };

    return (
        <Dialog
            open={open}
            onOpenChange={(next) => {
                onOpenChange(next);
                if (!next) {
                    form.reset();
                    form.clearErrors();
                }
            }}
        >
            <DialogContent className="sm:max-w-md">
                <form onSubmit={submit} className="grid gap-4">
                    <DialogHeader>
                        <DialogTitle>Change the path</DialogTitle>
                        <DialogDescription>
                            <span className="font-mono">{volume.name}</span> and its files move with it. Saving deploys
                            the image {app.name} runs now.
                        </DialogDescription>
                    </DialogHeader>
                    <TextField
                        id="volume-move-target"
                        label="Path in the container"
                        className="font-mono"
                        autoComplete="off"
                        value={form.data.target}
                        onChange={(e) => form.setData('target', e.target.value)}
                        error={form.errors.target}
                    />
                    <DialogFooter>
                        <Button type="submit" disabled={form.processing || form.data.target === volume.target}>
                            Change path
                        </Button>
                    </DialogFooter>
                </form>
            </DialogContent>
        </Dialog>
    );
}
