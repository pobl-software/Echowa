import { Head, Link, useForm, usePage } from '@inertiajs/react';
import type { FormEvent } from 'react';

import { TextField } from '@/Components/TextField';
import { Button } from '@/Components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/Components/ui/card';
import { FieldDescription, FieldGroup } from '@/Components/ui/field';
import GuestLayout from '@/Layouts/GuestLayout';

// Login's second step, after the right password (ADR 0042).
export default function TwoFactorChallenge() {
    const { appName } = usePage().props;
    const form = useForm({ code: '' });

    const submit = (e: FormEvent) => {
        e.preventDefault();
        form.post('/two-factor-challenge', { onError: () => form.reset('code') });
    };

    return (
        <Card className="shadow-sm">
            <Head title="Two-factor authentication" />
            <CardHeader>
                <CardTitle className="text-lg">Enter your code</CardTitle>
                <CardDescription>
                    Open your authenticator app and enter the code it shows for {appName}.
                </CardDescription>
            </CardHeader>
            <CardContent>
                <form onSubmit={submit}>
                    <FieldGroup>
                        <TextField
                            id="code"
                            label="Code"
                            inputMode="numeric"
                            autoComplete="one-time-code"
                            autoFocus
                            placeholder="123456"
                            className="font-mono tracking-widest"
                            value={form.data.code}
                            onChange={(e) => form.setData('code', e.target.value)}
                            error={form.errors.code}
                        />
                        <Button type="submit" size="lg" disabled={form.processing || form.data.code.trim() === ''}>
                            Log in
                        </Button>
                        <FieldDescription className="text-center">
                            Lost your phone? Run{' '}
                            <code className="font-mono whitespace-nowrap">
                                sudo echowa-artisan user:disable-two-factor
                            </code>{' '}
                            on the server. <Link href="/login">Back to log in</Link>
                        </FieldDescription>
                    </FieldGroup>
                </form>
            </CardContent>
        </Card>
    );
}

TwoFactorChallenge.layout = GuestLayout;
