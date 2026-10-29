import { Link, router, useForm, usePoll } from '@inertiajs/react';
import { DatabaseIcon, PlusIcon, UnlinkIcon } from 'lucide-react';
import { type FormEvent, type ReactNode, useId, useState } from 'react';

import { ConfirmDelete } from '@/Components/ConfirmDelete';
import { Panel } from '@/Components/Panel';
import { LiveStatusBadge } from '@/Components/StatusBadge';
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
import { FieldError } from '@/Components/ui/field';
import { Input } from '@/Components/ui/input';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/Components/ui/table';
import { type AppIcon, templateIcon } from '@/lib/templates';
import { cn } from '@/lib/utils';
import type { App, DatabaseChoices, LinkedDatabase } from '@/types/models';

// LinkedDatabases is a custom app's Databases section: the template apps
// it's linked to (ADR 0028). A row opens the database's own page.
export function LinkedDatabases({
    app,
    databases,
    choices,
}: {
    app: App;
    databases: LinkedDatabase[];
    choices: DatabaseChoices;
}) {
    const templateName = (slug: string) => choices.templates.find((t) => t.slug === slug)?.name ?? slug;
    const add = <AddDatabasesDialog app={app} choices={choices} />;
    // New databases deploy straight after they're added; follow along.
    usePoll(5000, { only: ['linkedDatabases'] });

    if (databases.length === 0) {
        return (
            <div className="flex flex-col items-center rounded-xl border border-dashed px-6 py-14 text-center">
                <span className="mb-4 flex size-11 items-center justify-center rounded-xl border bg-card">
                    <DatabaseIcon className="size-5 text-muted-foreground" />
                </span>
                <h2 className="font-medium">No databases linked</h2>
                <p className="mt-1 max-w-md text-sm text-muted-foreground">
                    Create a database for {app.name} from a template, or link one you already run. It stays its own app,
                    with its own deploys, logs and metrics.
                </p>
                <div className="mt-5">{add}</div>
            </div>
        );
    }

    return (
        <Panel
            title="Databases"
            description={`The databases ${app.name} uses. Open one to manage it.`}
            actions={add}
            flush
        >
            <Table>
                <TableHeader>
                    <TableRow>
                        <TableHead>Name</TableHead>
                        <TableHead className="hidden sm:table-cell">Type</TableHead>
                        <TableHead className="hidden xl:table-cell">Host</TableHead>
                        <TableHead>Status</TableHead>
                        <TableHead>
                            <span className="sr-only">Actions</span>
                        </TableHead>
                    </TableRow>
                </TableHeader>
                <TableBody>
                    {databases.map((db) => {
                        const Icon = templateIcon(db.template);
                        return (
                            <TableRow
                                key={db.id}
                                className="cursor-pointer"
                                onClick={() => router.visit(`/apps/${db.id}`)}
                            >
                                {/* The name gives way on narrow screens, so status and Unlink stay in view. */}
                                <TableCell className="w-full max-w-0">
                                    <div className="flex items-center gap-2.5">
                                        <span className="flex size-7 shrink-0 items-center justify-center rounded-md border bg-muted/50">
                                            <Icon className="size-3.5 text-muted-foreground" />
                                        </span>
                                        <div className="min-w-0">
                                            <Link
                                                href={`/apps/${db.id}`}
                                                className="block truncate font-medium hover:underline"
                                                onClick={(e) => e.stopPropagation()}
                                            >
                                                {db.name}
                                            </Link>
                                            {/* Phones have no Type column, and narrower screens no Host. */}
                                            <div className="truncate text-xs text-muted-foreground sm:hidden">
                                                {templateName(db.template)}
                                            </div>
                                            <div className="hidden font-mono text-xs text-muted-foreground sm:block xl:hidden">
                                                <Hosts db={db} />
                                            </div>
                                        </div>
                                    </div>
                                </TableCell>
                                <TableCell className="hidden sm:table-cell">{templateName(db.template)}</TableCell>
                                <TableCell className="hidden font-mono text-xs xl:table-cell">
                                    <Hosts db={db} />
                                </TableCell>
                                <TableCell>
                                    <LiveStatusBadge status={db.status} />
                                </TableCell>
                                {/* Clicks in the dialog bubble here through the portal; keep them from opening the row. */}
                                <TableCell className="py-1 text-right" onClick={(e) => e.stopPropagation()}>
                                    <ConfirmDelete
                                        title={`Unlink ${db.name}?`}
                                        confirmLabel="Unlink"
                                        variant="default"
                                        trigger={
                                            <Button
                                                variant="ghost"
                                                size="sm"
                                                aria-label={`Unlink ${db.name}`}
                                                title={`Unlink ${db.name}`}
                                            >
                                                <UnlinkIcon />
                                                <span className="hidden sm:inline">Unlink</span>
                                            </Button>
                                        }
                                        onConfirm={() =>
                                            router.delete(`/apps/${app.id}/databases/${db.id}`, {
                                                preserveScroll: true,
                                            })
                                        }
                                    >
                                        {app.name} stops listing <code>{db.name}</code> as one of its databases. The
                                        database keeps running, with its data, and you can link it again from Add
                                        databases.
                                    </ConfirmDelete>
                                </TableCell>
                            </TableRow>
                        );
                    })}
                </TableBody>
            </Table>
        </Panel>
    );
}

// Hosts is where apps reach a database: itself, and its connection pooler
// while pooling is on.
function Hosts({ db }: { db: LinkedDatabase }) {
    return (
        <>
            <div className="truncate">
                {db.host}:{db.port}
            </div>
            {db.pooler && (
                <div className="truncate">
                    {db.pooler.host}:{db.pooler.port} <span className="font-sans text-muted-foreground">pooled</span>
                </div>
            )}
        </>
    );
}

type NewDatabase = { template: string; name: string };

// The name a new database gets unless it's changed: the app's, then the
// template's, within the 40 characters a name may have.
const defaultName = (app: string, slug: string) => `${app}-${slug}`.slice(0, 40).replace(/-+$/, '');

// AddDatabasesDialog creates databases from templates and links existing
// ones, as many as are ticked, in one go.
function AddDatabasesDialog({ app, choices }: { app: App; choices: DatabaseChoices }) {
    const [open, setOpen] = useState(false);
    const form = useForm<{ databases: NewDatabase[]; link: number[] }>({
        databases: [],
        link: [],
    });
    const errors = form.errors as Record<string, string | undefined>;
    const count = form.data.databases.length + form.data.link.length;

    const toggleTemplate = (slug: string, on: boolean) =>
        form.setData(
            'databases',
            on
                ? [...form.data.databases, { template: slug, name: defaultName(app.name, slug) }]
                : form.data.databases.filter((d) => d.template !== slug),
        );
    const rename = (slug: string, name: string) =>
        form.setData(
            'databases',
            form.data.databases.map((d) => (d.template === slug ? { ...d, name } : d)),
        );
    const toggleExisting = (id: number, on: boolean) =>
        form.setData('link', on ? [...form.data.link, id] : form.data.link.filter((l) => l !== id));

    const submit = (e: FormEvent) => {
        e.preventDefault();
        form.post(`/apps/${app.id}/databases`, {
            preserveScroll: true,
            onSuccess: () => {
                setOpen(false);
                form.reset();
            },
        });
    };

    return (
        <Dialog
            open={open}
            onOpenChange={(next) => {
                setOpen(next);
                if (!next) {
                    form.reset();
                    form.clearErrors();
                }
            }}
        >
            <DialogTrigger render={<Button />}>
                <PlusIcon />
                Add databases
            </DialogTrigger>
            {/* Ticked rows grow, so the lists scroll between a fixed header and footer. */}
            <DialogContent className="max-h-[calc(100dvh-2rem)] grid-rows-[minmax(0,1fr)] sm:max-w-lg">
                <form onSubmit={submit} className="grid min-h-0 grid-rows-[auto_minmax(0,1fr)_auto] gap-4">
                    <DialogHeader>
                        <DialogTitle>Add databases to {app.name}</DialogTitle>
                        <DialogDescription>
                            New databases are created from a template and deployed straight away. Each one you add is
                            linked to {app.name}.
                        </DialogDescription>
                    </DialogHeader>

                    <div className="-mx-4 grid min-h-0 content-start gap-5 overflow-y-auto px-4">
                        <fieldset className="grid gap-2">
                            <legend className="mb-2 text-sm font-medium">Create new database</legend>
                            {choices.templates.map((t) => {
                                const index = form.data.databases.findIndex((d) => d.template === t.slug);
                                const picked = index >= 0;
                                const error = picked
                                    ? (errors[`databases.${index}.name`] ?? errors[`databases.${index}.template`])
                                    : undefined;
                                return (
                                    <Choice
                                        key={t.slug}
                                        icon={templateIcon(t.slug)}
                                        title={t.name}
                                        detail={`${t.repository}:${t.default_tag}`}
                                        mono
                                        checked={picked}
                                        onCheckedChange={(on) => toggleTemplate(t.slug, on)}
                                    >
                                        {picked && (
                                            <div className="grid gap-1.5 pl-7">
                                                <Input
                                                    aria-label={`${t.name} name`}
                                                    className="font-mono"
                                                    value={form.data.databases[index].name}
                                                    onChange={(e) => rename(t.slug, e.target.value)}
                                                    aria-invalid={!!error || undefined}
                                                />
                                                {error && <FieldError>{error}</FieldError>}
                                            </div>
                                        )}
                                    </Choice>
                                );
                            })}
                        </fieldset>

                        {choices.existing.length > 0 && (
                            <fieldset className="grid gap-2">
                                <legend className="mb-2 text-sm font-medium">Link existing database</legend>
                                {choices.existing.map((db) => (
                                    <Choice
                                        key={db.id}
                                        icon={templateIcon(db.template)}
                                        title={db.name}
                                        detail={
                                            choices.templates.find((t) => t.slug === db.template)?.name ?? db.template
                                        }
                                        checked={form.data.link.includes(db.id)}
                                        onCheckedChange={(on) => toggleExisting(db.id, on)}
                                    />
                                ))}
                            </fieldset>
                        )}

                        {(errors.databases || errors.link) && (
                            <FieldError>{errors.databases ?? errors.link}</FieldError>
                        )}
                    </div>

                    <DialogFooter>
                        <Button type="submit" disabled={form.processing || count === 0}>
                            {count > 1 ? `Add ${count} databases` : 'Add database'}
                        </Button>
                    </DialogFooter>
                </form>
            </DialogContent>
        </Dialog>
    );
}

// Choice is one tickable database in the dialog, with anything it needs
// once ticked (a new one's name) underneath.
function Choice({
    icon: Icon,
    title,
    detail,
    mono = false,
    checked,
    onCheckedChange,
    children,
}: {
    icon: AppIcon;
    title: string;
    detail: string;
    mono?: boolean;
    checked: boolean;
    onCheckedChange: (checked: boolean) => void;
    children?: ReactNode;
}) {
    const id = useId();

    return (
        <div
            className={cn(
                'grid gap-3 rounded-lg border px-3 py-2.5 transition-colors',
                checked && 'border-primary/50 bg-primary/5',
            )}
        >
            <label className="flex cursor-pointer items-center gap-3">
                <Checkbox aria-labelledby={id} checked={checked} onCheckedChange={onCheckedChange} />
                <Icon className="size-4 shrink-0 text-muted-foreground" />
                <span className="min-w-0">
                    <span id={id} className="block text-sm font-medium">
                        {title}
                    </span>
                    <span className={cn('block truncate text-xs text-muted-foreground', mono && 'font-mono')}>
                        {detail}
                    </span>
                </span>
            </label>
            {children}
        </div>
    );
}
