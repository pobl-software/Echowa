import { Head, useForm } from '@inertiajs/react';
import type { FormEvent } from 'react';

import { TextField } from '@/Components/TextField';
import { Button } from '@/Components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/Components/ui/card';
import { FieldDescription, FieldGroup } from '@/Components/ui/field';
import GuestLayout from '@/Layouts/GuestLayout';

export default function Login() {
    const form = useForm({ email: '', password: '' });

    const submit = (e: FormEvent) => {
        e.preventDefault();
        form.post('/login', { onFinish: () => form.reset('password') });
    };

    return (
        <Card className="shadow-sm">
            <Head title="Log in" />
            <CardHeader>
                <CardTitle className="text-lg">Welcome back</CardTitle>
                <CardDescription>Sign in to manage your apps.</CardDescription>
            </CardHeader>
            <CardContent>
                <form onSubmit={submit}>
                    <FieldGroup>
                        <TextField
                            id="email"
                            label="Email"
                            type="email"
                            autoComplete="username"
                            autoFocus
                            value={form.data.email}
                            onChange={(e) => form.setData('email', e.target.value)}
                            error={form.errors.email}
                        />
                        <TextField
                            id="password"
                            label="Password"
                            type="password"
                            autoComplete="current-password"
                            value={form.data.password}
                            onChange={(e) => form.setData('password', e.target.value)}
                            error={form.errors.password}
                        />
                        <Button type="submit" size="lg" disabled={form.processing}>
                            Log in
                        </Button>
                        <FieldDescription className="text-center">
                            Forgot your password? Run{' '}
                            <code className="font-mono whitespace-nowrap">sudo echowa-artisan user:create</code> on the
                            server.
                        </FieldDescription>
                    </FieldGroup>
                </form>
            </CardContent>
        </Card>
    );
}

Login.layout = GuestLayout;
