import { Head, Link, useForm } from '@inertiajs/react';
import type { FormEvent } from 'react';
import { Page } from '@/Components/Page';
import { Panel } from '@/Components/Panel';
import { TextField } from '@/Components/TextField';
import { Button, buttonVariants } from '@/Components/ui/button';
import { FieldDescription, FieldGroup } from '@/Components/ui/field';
import type { Registry } from '@/types/models';

// Create and edit. The token is never sent to the browser: on edit the
// field starts empty, and leaving it empty keeps the stored token.
export default function RegistryForm({ registry }: { registry: Registry | null }) {
    const form = useForm({
        name: registry?.name ?? '',
        host: registry?.host ?? 'ghcr.io',
        username: registry?.username ?? '',
        token: '',
    });

    const submit = (e: FormEvent) => {
        e.preventDefault();
        const options = { onFinish: () => form.reset('token') };
        if (registry) {
            form.put(`/registries/${registry.id}`, options);
        } else {
            form.post('/registries', options);
        }
    };

    const title = registry ? `Edit ${registry.name}` : 'Add registry';

    return (
        <>
            <Head title={title} />
            <Page
                crumbs={[{ label: 'Registries', href: '/registries' }, { label: title }]}
                description="Echowa sends these credentials with each image pull. They're never stored in Docker."
            >
                <Panel className="max-w-2xl">
                    <form onSubmit={submit}>
                        <FieldGroup>
                            <TextField
                                id="name"
                                label="Name"
                                placeholder="ghcr"
                                value={form.data.name}
                                onChange={(e) => form.setData('name', e.target.value)}
                                error={form.errors.name}
                                autoFocus
                            />
                            <TextField
                                id="host"
                                label="Host"
                                placeholder="ghcr.io"
                                value={form.data.host}
                                onChange={(e) => form.setData('host', e.target.value)}
                                error={form.errors.host}
                            />
                            <TextField
                                id="username"
                                label="Username"
                                autoComplete="off"
                                value={form.data.username}
                                onChange={(e) => form.setData('username', e.target.value)}
                                error={form.errors.username}
                            />
                            <div>
                                <TextField
                                    id="token"
                                    label={registry ? 'New token' : 'Token'}
                                    type="password"
                                    autoComplete="new-password"
                                    placeholder={registry ? 'Leave empty to keep the current token' : ''}
                                    value={form.data.token}
                                    onChange={(e) => form.setData('token', e.target.value)}
                                    error={form.errors.token}
                                />
                                <FieldDescription className="mt-2">
                                    Stored encrypted and never shown again. For GHCR, use a personal access token with
                                    the <code>read:packages</code> scope.
                                </FieldDescription>
                            </div>
                            <div className="flex gap-2">
                                <Button type="submit" disabled={form.processing}>
                                    {registry ? 'Save' : 'Add registry'}
                                </Button>
                                <Link href="/registries" className={buttonVariants({ variant: 'outline' })}>
                                    Cancel
                                </Link>
                            </div>
                        </FieldGroup>
                    </form>
                </Panel>
            </Page>
        </>
    );
}
