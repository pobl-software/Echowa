import { Link, router, useForm, usePoll } from '@inertiajs/react';
import { BoxesIcon, BoxIcon, PlusIcon, UnlinkIcon } from 'lucide-react';
import { type FormEvent, type ReactNode, useState } from 'react';

import { ConfirmDelete } from '@/Components/ConfirmDelete';
import { Choice } from '@/Components/LinkedDatabases';
import { NativeSelect } from '@/Components/NativeSelect';
import { Panel } from '@/Components/Panel';
import { LiveStatusBadge } from '@/Components/StatusBadge';
import { TextField } from '@/Components/TextField';
import { Button } from '@/Components/ui/button';
import { Checkbox } from '@/Components/ui/checkbox';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
    DialogTrigger,
} from '@/Components/ui/dialog';
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/Components/ui/field';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/Components/ui/table';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/Components/ui/tabs';
import type { App, LinkedService, RegistryOption, ServiceChoices } from '@/types/models';

// LinkedServices is a custom app's Services section: the custom apps that
// support it, such as a queue worker or a PDF renderer (ADR 0037). A row
// opens the service's own page.
export function LinkedServices({
    app,
    services,
    choices,
    registries,
}: {
    app: App;
    services: LinkedService[];
    choices: ServiceChoices;
    registries: RegistryOption[];
}) {
    // New services deploy straight after they're added; follow along.
    usePoll(5000, { only: ['linkedServices'] });

    if (choices.part_of.length > 0) {
        return (
            <EmptyState title={`${app.name} is a service`}>
                It supports{' '}
                {choices.part_of.map((parent, i) => (
                    <span key={parent.id}>
                        {i > 0 && ', '}
                        <Link href={`/apps/${parent.id}?tab=services`} className="text-foreground hover:underline">
                            {parent.name}
                        </Link>
                    </span>
                ))}
                , so it has no services of its own. Add them to {choices.part_of[0].name} instead.
            </EmptyState>
        );
    }

    const add = <AddServiceDialog app={app} choices={choices} registries={registries} />;
    if (services.length === 0) {
        return (
            <EmptyState title="No services linked" action={add}>
                Run a queue worker, a helper such as Gotenberg, or link an app you already run. Each stays its own app,
                with its own deploys, logs and metrics.
            </EmptyState>
        );
    }

    return (
        <Panel
            title="Services"
            description={`The apps that support ${app.name}. Open one to manage it.`}
            actions={add}
            flush
        >
            <Table>
                <TableHeader>
                    <TableRow>
                        <TableHead>Name</TableHead>
                        <TableHead className="hidden sm:table-cell">Image</TableHead>
                        <TableHead className="hidden xl:table-cell">Host</TableHead>
                        <TableHead>Status</TableHead>
                        <TableHead>
                            <span className="sr-only">Actions</span>
                        </TableHead>
                    </TableRow>
                </TableHeader>
                <TableBody>
                    {services.map((service) => (
                        <TableRow
                            key={service.id}
                            className="cursor-pointer"
                            onClick={() => router.visit(`/apps/${service.id}`)}
                        >
                            {/* The name gives way on narrow screens, so status and Unlink stay in view. */}
                            <TableCell className="w-full max-w-0">
                                <div className="flex items-center gap-2.5">
                                    <span className="flex size-7 shrink-0 items-center justify-center rounded-md border bg-muted/50">
                                        <BoxIcon className="size-3.5 text-muted-foreground" />
                                    </span>
                                    <div className="min-w-0">
                                        <Link
                                            href={`/apps/${service.id}`}
                                            className="block truncate font-medium hover:underline"
                                            onClick={(e) => e.stopPropagation()}
                                        >
                                            {service.name}
                                        </Link>
                                        {/* Phones have no Image column, and narrower screens no Host. */}
                                        <div className="truncate font-mono text-xs text-muted-foreground sm:hidden">
                                            {service.image}
                                        </div>
                                        <div className="hidden truncate font-mono text-xs text-muted-foreground sm:block xl:hidden">
                                            {service.host}:{service.port}
                                        </div>
                                    </div>
                                </div>
                            </TableCell>
                            <TableCell className="hidden max-w-56 truncate font-mono text-xs sm:table-cell">
                                {service.image}
                            </TableCell>
                            <TableCell className="hidden font-mono text-xs xl:table-cell">
                                {service.host}:{service.port}
                            </TableCell>
                            <TableCell>
                                <LiveStatusBadge status={service.status} />
                            </TableCell>
                            {/* Clicks in the dialog bubble here through the portal; keep them from opening the row. */}
                            <TableCell className="py-1 text-right" onClick={(e) => e.stopPropagation()}>
                                <ConfirmDelete
                                    title={`Unlink ${service.name}?`}
                                    confirmLabel="Unlink"
                                    variant="default"
                                    trigger={
                                        <Button
                                            variant="ghost"
                                            size="sm"
                                            aria-label={`Unlink ${service.name}`}
                                            title={`Unlink ${service.name}`}
                                        >
                                            <UnlinkIcon />
                                            <span className="hidden sm:inline">Unlink</span>
                                        </Button>
                                    }
                                    onConfirm={() =>
                                        router.delete(`/apps/${app.id}/services/${service.id}`, {
                                            preserveScroll: true,
                                        })
                                    }
                                >
                                    {app.name} stops listing <code>{service.name}</code> as one of its services. It
                                    keeps running, and you can link it again from Add service.
                                </ConfirmDelete>
                            </TableCell>
                        </TableRow>
                    ))}
                </TableBody>
            </Table>
        </Panel>
    );
}

function EmptyState({ title, action, children }: { title: string; action?: ReactNode; children: ReactNode }) {
    return (
        <div className="flex flex-col items-center rounded-xl border border-dashed px-6 py-14 text-center">
            <span className="mb-4 flex size-11 items-center justify-center rounded-xl border bg-card">
                <BoxesIcon className="size-5 text-muted-foreground" />
            </span>
            <h2 className="font-medium">{title}</h2>
            <p className="mt-1 max-w-md text-sm text-muted-foreground">{children}</p>
            {action && <div className="mt-5">{action}</div>}
        </div>
    );
}

const blank = {
    name: '',
    repository: '',
    default_tag: 'latest',
    registry_id: '',
    port: '3000',
    command: '',
    healthcheck_cmd: '',
    copy_env: false,
};

// AddServiceDialog creates a service and deploys it, or links apps that
// already run.
function AddServiceDialog({
    app,
    choices,
    registries,
}: {
    app: App;
    choices: ServiceChoices;
    registries: RegistryOption[];
}) {
    const [open, setOpen] = useState(false);
    const [tab, setTab] = useState<'new' | 'link'>('new');
    const create = useForm(blank);
    const link = useForm<{ link: number[] }>({ link: [] });

    create.transform((data) => ({
        ...data,
        replicas: 1,
        registry_id: data.registry_id === '' ? null : Number(data.registry_id),
        port: data.port === '' ? null : Number(data.port),
        healthcheck_cmd: data.healthcheck_cmd.trim() === '' ? null : data.healthcheck_cmd,
    }));

    const reset = () => {
        setTab('new');
        create.reset();
        create.clearErrors();
        link.reset();
        link.clearErrors();
    };
    const done = { preserveScroll: true, onSuccess: () => setOpen(false) };
    const submit = (e: FormEvent) => {
        e.preventDefault();
        if (tab === 'new') {
            create.post(`/apps/${app.id}/services`, done);
        } else {
            link.post(`/apps/${app.id}/services/link`, done);
        }
    };
    const toggle = (id: number, on: boolean) =>
        link.setData('link', on ? [...link.data.link, id] : link.data.link.filter((l) => l !== id));
    const errors = create.errors as Record<string, string | undefined>;
    const count = link.data.link.length;

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
            <DialogTrigger render={<Button />}>
                <PlusIcon />
                Add service
            </DialogTrigger>
            {/* The form scrolls between a fixed header and footer. */}
            <DialogContent className="max-h-[calc(100dvh-2rem)] grid-rows-[minmax(0,1fr)] sm:max-w-lg">
                <form onSubmit={submit} className="grid min-h-0 grid-rows-[auto_minmax(0,1fr)_auto] gap-4">
                    <DialogHeader>
                        <DialogTitle>Add a service to {app.name}</DialogTitle>
                        <DialogDescription>
                            A service is an app of its own that supports {app.name}, such as a queue worker. New ones
                            deploy straight away.
                        </DialogDescription>
                    </DialogHeader>

                    <Tabs
                        value={tab}
                        onValueChange={(value) => setTab(value as 'new' | 'link')}
                        className="-mx-4 min-h-0 gap-4 overflow-y-auto px-4"
                    >
                        <TabsList className="w-full">
                            <TabsTrigger value="new">New service</TabsTrigger>
                            <TabsTrigger value="link" disabled={choices.existing.length === 0}>
                                Link existing
                            </TabsTrigger>
                        </TabsList>

                        <TabsContent value="new">
                            <FieldGroup className="gap-4">
                                <TextField
                                    id="service-name"
                                    label="Name"
                                    className="font-mono"
                                    placeholder={`${app.name}-gotenberg`}
                                    value={create.data.name}
                                    onChange={(e) => create.setData('name', e.target.value)}
                                    error={errors.name}
                                />
                                <div className="grid gap-4 sm:grid-cols-[1fr_7rem]">
                                    <TextField
                                        id="service-repository"
                                        label="Image repository"
                                        className="font-mono"
                                        placeholder="gotenberg/gotenberg"
                                        value={create.data.repository}
                                        onChange={(e) => create.setData('repository', e.target.value)}
                                        error={errors.repository}
                                    />
                                    <TextField
                                        id="service-tag"
                                        label="Tag"
                                        className="font-mono"
                                        value={create.data.default_tag}
                                        onChange={(e) => create.setData('default_tag', e.target.value)}
                                        error={errors.default_tag}
                                    />
                                </div>
                                <Field data-invalid={!!errors.registry_id || undefined}>
                                    <FieldLabel htmlFor="service-registry">Registry</FieldLabel>
                                    <NativeSelect
                                        id="service-registry"
                                        value={create.data.registry_id}
                                        onChange={(e) => create.setData('registry_id', e.target.value)}
                                        aria-invalid={!!errors.registry_id || undefined}
                                    >
                                        <option value="">None (public image)</option>
                                        {registries.map((r) => (
                                            <option key={r.id} value={r.id}>
                                                {r.name} ({r.host})
                                            </option>
                                        ))}
                                    </NativeSelect>
                                    {errors.registry_id && <FieldError>{errors.registry_id}</FieldError>}
                                </Field>
                                <TextField
                                    id="service-command"
                                    label="Command"
                                    className="font-mono"
                                    placeholder="php artisan horizon"
                                    value={create.data.command}
                                    onChange={(e) => create.setData('command', e.target.value)}
                                    error={errors.command}
                                    description="Runs in place of the image's CMD, without a shell. Leave empty to keep the CMD."
                                />
                                <div className="grid gap-4 sm:grid-cols-[7rem_1fr]">
                                    <TextField
                                        id="service-port"
                                        label="Port"
                                        type="number"
                                        min={1}
                                        max={65535}
                                        className="font-mono"
                                        value={create.data.port}
                                        onChange={(e) => create.setData('port', e.target.value)}
                                        error={errors.port}
                                    />
                                    <TextField
                                        id="service-healthcheck"
                                        label="Healthcheck"
                                        className="font-mono"
                                        placeholder="The image's own HEALTHCHECK"
                                        value={create.data.healthcheck_cmd}
                                        onChange={(e) => create.setData('healthcheck_cmd', e.target.value)}
                                        error={errors.healthcheck_cmd}
                                    />
                                </div>
                                <FieldDescription className="-mt-2">
                                    {app.name} reaches it at{' '}
                                    <code>
                                        echowa-{create.data.name || 'name'}:{create.data.port || 'port'}
                                    </code>
                                    . A worker that listens on nothing can keep any port. If the image&apos;s
                                    healthcheck calls a web server the command doesn&apos;t start, give it one that
                                    passes, or the deploy rolls back.
                                </FieldDescription>
                                <label className="flex items-start gap-2 text-sm">
                                    <Checkbox
                                        aria-labelledby="copy-env-label"
                                        className="mt-0.5"
                                        checked={create.data.copy_env}
                                        onCheckedChange={(checked) => create.setData('copy_env', checked)}
                                    />
                                    <span>
                                        <span id="copy-env-label">Copy {app.name}&apos;s environment</span>
                                        <span className="block text-muted-foreground">
                                            A copy, made now. Later changes to {app.name}&apos;s don&apos;t reach it.
                                        </span>
                                    </span>
                                </label>
                            </FieldGroup>
                        </TabsContent>

                        <TabsContent value="link">
                            <fieldset className="grid gap-2">
                                <legend className="sr-only">Link existing</legend>
                                {choices.existing.map((service) => (
                                    <Choice
                                        key={service.id}
                                        icon={BoxIcon}
                                        title={service.name}
                                        detail={service.repository}
                                        mono
                                        checked={link.data.link.includes(service.id)}
                                        onCheckedChange={(on) => toggle(service.id, on)}
                                    />
                                ))}
                            </fieldset>
                            {link.errors.link && <FieldError className="mt-3">{link.errors.link}</FieldError>}
                        </TabsContent>
                    </Tabs>

                    <DialogFooter>
                        {tab === 'new' ? (
                            <Button type="submit" disabled={create.processing}>
                                Create and deploy
                            </Button>
                        ) : (
                            <Button type="submit" disabled={link.processing || count === 0}>
                                {count > 1 ? `Link ${count} services` : 'Link service'}
                            </Button>
                        )}
                    </DialogFooter>
                </form>
            </DialogContent>
        </Dialog>
    );
}
