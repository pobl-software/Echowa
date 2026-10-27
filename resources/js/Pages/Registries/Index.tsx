import { Head, Link, router } from '@inertiajs/react';
import { KeyRoundIcon, PencilIcon, PlugZapIcon, PlusIcon, Trash2Icon } from 'lucide-react';
import { useState } from 'react';

import { ConfirmDelete } from '@/Components/ConfirmDelete';
import { Page } from '@/Components/Page';
import { Panel } from '@/Components/Panel';
import { Button, buttonVariants } from '@/Components/ui/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/Components/ui/table';
import type { RegistryRow } from '@/types/models';

export default function RegistriesIndex({ registries }: { registries: RegistryRow[] }) {
    const [testing, setTesting] = useState<number | null>(null);

    const test = (id: number) =>
        router.post(
            `/registries/${id}/test`,
            {},
            { preserveScroll: true, onStart: () => setTesting(id), onFinish: () => setTesting(null) },
        );

    return (
        <>
            <Head title="Registries" />
            <Page
                crumbs={[{ label: 'Registries' }]}
                description="Logins for pulling private images. Public images don't need one."
                actions={
                    <Link href="/registries/create" className={buttonVariants()}>
                        <PlusIcon />
                        Add registry
                    </Link>
                }
            >
                {registries.length === 0 ? (
                    <div className="flex flex-col items-center rounded-xl border border-dashed px-6 py-14 text-center">
                        <span className="mb-4 flex size-11 items-center justify-center rounded-xl border bg-card">
                            <KeyRoundIcon className="size-5 text-muted-foreground" />
                        </span>
                        <h2 className="font-medium">No registries yet</h2>
                        <p className="mt-1 max-w-md text-sm text-muted-foreground">
                            For GHCR, create a personal access token with the{' '}
                            <code className="font-mono">read:packages</code> scope and add it here with your GitHub
                            username.
                        </p>
                        <Link href="/registries/create" className={buttonVariants({ className: 'mt-5' })}>
                            Add a registry
                        </Link>
                    </div>
                ) : (
                    <Panel flush>
                        <Table>
                            <TableHeader>
                                <TableRow>
                                    <TableHead>Name</TableHead>
                                    <TableHead className="hidden sm:table-cell">Host</TableHead>
                                    <TableHead className="hidden md:table-cell">Username</TableHead>
                                    <TableHead className="hidden sm:table-cell">Apps</TableHead>
                                    <TableHead className="text-right">Actions</TableHead>
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {registries.map((registry) => (
                                    <TableRow key={registry.id}>
                                        <TableCell>
                                            <div className="font-medium">{registry.name}</div>
                                            <div className="text-xs text-muted-foreground sm:hidden">
                                                {registry.host}
                                            </div>
                                        </TableCell>
                                        <TableCell className="hidden font-mono text-xs sm:table-cell">
                                            {registry.host}
                                        </TableCell>
                                        <TableCell className="hidden md:table-cell">{registry.username}</TableCell>
                                        <TableCell className="hidden tabular-nums sm:table-cell">
                                            {registry.apps_count}
                                        </TableCell>
                                        <TableCell>
                                            <div className="flex justify-end gap-1">
                                                <Button
                                                    variant="outline"
                                                    size="sm"
                                                    disabled={testing !== null}
                                                    onClick={() => test(registry.id)}
                                                >
                                                    <PlugZapIcon />
                                                    <span className="hidden sm:inline">
                                                        {testing === registry.id ? 'Testing…' : 'Test'}
                                                    </span>
                                                </Button>
                                                <Link
                                                    href={`/registries/${registry.id}/edit`}
                                                    className={buttonVariants({ variant: 'ghost', size: 'icon-sm' })}
                                                    aria-label={`Edit ${registry.name}`}
                                                    title="Edit"
                                                >
                                                    <PencilIcon />
                                                </Link>
                                                <ConfirmDelete
                                                    title={`Delete ${registry.name}?`}
                                                    trigger={
                                                        <Button
                                                            variant="ghost"
                                                            size="icon-sm"
                                                            className="text-destructive"
                                                            aria-label={`Delete ${registry.name}`}
                                                            title="Delete"
                                                        >
                                                            <Trash2Icon />
                                                        </Button>
                                                    }
                                                    onConfirm={() =>
                                                        router.delete(`/registries/${registry.id}`, {
                                                            preserveScroll: true,
                                                        })
                                                    }
                                                >
                                                    {registry.apps_count > 0
                                                        ? `${registry.apps_count} app(s) pull through this registry, so it can't be deleted until they use another one.`
                                                        : 'The stored token is deleted too.'}
                                                </ConfirmDelete>
                                            </div>
                                        </TableCell>
                                    </TableRow>
                                ))}
                            </TableBody>
                        </Table>
                    </Panel>
                )}
            </Page>
        </>
    );
}
