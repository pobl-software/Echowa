import { Head, useForm } from '@inertiajs/react';
import type { FormEvent } from 'react';

import { TextField } from '@/Components/TextField';
import { Button } from '@/Components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/Components/ui/card';
import { FieldDescription, FieldGroup } from '@/Components/ui/field';
import GuestLayout from '@/Layouts/GuestLayout';

// The first run: creates the admin. Only a browser that opened the setup
// link, which the installer prints, gets the form (ADR 0023).
export default function Setup({ ready }: { ready: boolean }) {
    return (
        <Card className="shadow-sm">
            <Head title="Set up Echowa" />
            <CardHeader>
                <CardTitle className="text-lg">Welcome to Echowa</CardTitle>
                <CardDescription>
                    {ready
                        ? 'Create the admin account. It’s the only one, and it manages everything on this server.'
                        : 'Open the setup link to create the admin account.'}
                </CardDescription>
            </CardHeader>
            <CardContent>{ready ? <SetupForm /> : <NeedsLink />}</CardContent>
        </Card>
    );
}

function SetupForm() {
    const form = useForm({ name: '', email: '', password: '' });

    const submit = (e: FormEvent) => {
        e.preventDefault();
        form.post('/setup', { onFinish: () => form.reset('password') });
    };

    return (
        <form onSubmit={submit}>
            <FieldGroup>
                <TextField
                    id="name"
                    label="Name"
                    autoComplete="name"
                    autoFocus
                    value={form.data.name}
                    onChange={(e) => form.setData('name', e.target.value)}
                    error={form.errors.name}
                />
                <TextField
                    id="email"
                    label="Email"
                    type="email"
                    autoComplete="username"
                    value={form.data.email}
                    onChange={(e) => form.setData('email', e.target.value)}
                    error={form.errors.email}
                />
                <TextField
                    id="password"
                    label="Password"
                    type="password"
                    autoComplete="new-password"
                    value={form.data.password}
                    onChange={(e) => form.setData('password', e.target.value)}
                    error={form.errors.password}
                />
                <FieldDescription>
                    At least 8 characters. Whoever signs in controls this server, so make it a strong one.
                </FieldDescription>
                <Button type="submit" size="lg" disabled={form.processing}>
                    Create account
                </Button>
            </FieldGroup>
        </form>
    );
}

function NeedsLink() {
    return (
        <div className="flex flex-col gap-3 text-sm text-muted-foreground">
            <p>The installer printed it when it finished. It only works until the admin exists.</p>
            <p>Lost it, or it didn’t work? Print it again on the server:</p>
            <pre className="overflow-x-auto rounded-md bg-muted px-3 py-2 font-mono text-xs text-foreground">
                sudo echowa-artisan setup:link
            </pre>
        </div>
    );
}

Setup.layout = GuestLayout;
