import { useForm } from '@inertiajs/react';
import { Loader2Icon, ShieldCheckIcon, ShieldOffIcon } from 'lucide-react';
import { type FormEvent, useState } from 'react';

import { CopyButton } from '@/Components/CopyButton';
import { Panel } from '@/Components/Panel';
import { SettingsPage } from '@/Components/SettingsNav';
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
} from '@/Components/ui/dialog';
import { toast } from '@/Components/ui/toast';
import { postJson } from '@/lib/http';
import { formatDateTime } from '@/lib/time';
import type { TwoFactorSetup, TwoFactorStatus } from '@/types/models';

export default function SettingsSecurity({ twoFactor }: { twoFactor: TwoFactorStatus }) {
    return (
        <SettingsPage section="security">
            <Panel
                title="Two-factor authentication"
                description="Logging in takes a code from an authenticator app on your phone as well as your password, so a password that leaks isn't enough."
                actions={twoFactor.enabled ? <TurnOff /> : <SetUp />}
            >
                {twoFactor.enabled ? (
                    <div className="flex flex-wrap items-center gap-3 text-sm">
                        <StatusPill tone="success">On</StatusPill>
                        <span className="text-muted-foreground">
                            Since {formatDateTime(twoFactor.confirmed_at)}. Lost your phone? Run{' '}
                            <code className="font-mono whitespace-nowrap">
                                sudo echowa-artisan user:disable-two-factor
                            </code>{' '}
                            on the server.
                        </span>
                    </div>
                ) : (
                    <div className="flex flex-wrap items-center gap-3 text-sm">
                        <StatusPill tone="warning">Off</StatusPill>
                        <span className="text-muted-foreground">Your password is all it takes to log in.</span>
                    </div>
                )}
            </Panel>
        </SettingsPage>
    );
}

// SetUp asks for a new secret, then shows it as a QR code and as a key to
// type in, and turns two-factor authentication on once a code from the app
// matches. The secret is fetched as plain JSON, never a page prop, so it
// isn't kept in the browser's history.
function SetUp() {
    const [setup, setSetup] = useState<TwoFactorSetup | null>(null);
    const [starting, setStarting] = useState(false);
    const form = useForm({ code: '' });

    const start = async () => {
        setStarting(true);
        try {
            setSetup(await postJson<TwoFactorSetup>('/settings/security/two-factor/setup', {}));
        } catch (error) {
            toast.add({ type: 'error', title: 'Couldn’t start the setup', description: String(error) });
        } finally {
            setStarting(false);
        }
    };
    const close = () => {
        setSetup(null);
        form.reset();
        form.clearErrors();
    };
    const submit = (e: FormEvent) => {
        e.preventDefault();
        form.post('/settings/security/two-factor', { preserveScroll: true, onSuccess: close });
    };

    return (
        <>
            <Button onClick={start} disabled={starting}>
                {starting ? <Loader2Icon className="animate-spin" /> : <ShieldCheckIcon />}
                Set up
            </Button>
            <Dialog open={setup !== null} onOpenChange={(open) => !open && close()}>
                <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-md">
                    {setup && (
                        <form onSubmit={submit} className="grid gap-5">
                            <DialogHeader>
                                <DialogTitle>Set up two-factor authentication</DialogTitle>
                                <DialogDescription>
                                    Scan the QR code with an authenticator app, such as 1Password, Google Authenticator
                                    or Authy. Then enter the code it shows.
                                </DialogDescription>
                            </DialogHeader>

                            <QRCode qr={setup.qr} />

                            <div className="space-y-1.5">
                                <p className="text-sm text-muted-foreground">Can’t scan it? Enter this key instead.</p>
                                <div className="flex items-center gap-2 rounded-lg border bg-muted/40 py-1.5 pr-1.5 pl-3">
                                    <code className="flex-1 font-mono text-sm break-all select-all">
                                        {groups(setup.secret)}
                                    </code>
                                    <CopyButton label="key" value={setup.secret} />
                                </div>
                            </div>

                            <TextField
                                id="two-factor-code"
                                label="Code from the app"
                                inputMode="numeric"
                                autoComplete="one-time-code"
                                placeholder="123456"
                                className="font-mono tracking-widest"
                                value={form.data.code}
                                onChange={(e) => form.setData('code', e.target.value)}
                                error={form.errors.code}
                            />

                            <DialogFooter>
                                <Button type="submit" disabled={form.processing || form.data.code.trim() === ''}>
                                    Turn on
                                </Button>
                            </DialogFooter>
                        </form>
                    )}
                </DialogContent>
            </Dialog>
        </>
    );
}

// TurnOff takes a code, so a browser left signed in isn't enough.
function TurnOff() {
    const [open, setOpen] = useState(false);
    const form = useForm({ code: '' });

    const submit = (e: FormEvent) => {
        e.preventDefault();
        form.delete('/settings/security/two-factor', { preserveScroll: true, onSuccess: () => setOpen(false) });
    };

    return (
        <>
            <Button variant="outline" onClick={() => setOpen(true)}>
                <ShieldOffIcon />
                Turn off
            </Button>
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
                <DialogContent className="sm:max-w-md">
                    <form onSubmit={submit} className="grid gap-4">
                        <DialogHeader>
                            <DialogTitle>Turn off two-factor authentication</DialogTitle>
                            <DialogDescription>
                                Enter the code your authenticator app shows. After this, your password is all it takes
                                to log in.
                            </DialogDescription>
                        </DialogHeader>
                        <TextField
                            id="two-factor-off-code"
                            label="Code from the app"
                            inputMode="numeric"
                            autoComplete="one-time-code"
                            placeholder="123456"
                            className="font-mono tracking-widest"
                            value={form.data.code}
                            onChange={(e) => form.setData('code', e.target.value)}
                            error={form.errors.code}
                        />
                        <DialogFooter>
                            <Button
                                type="submit"
                                variant="destructive"
                                disabled={form.processing || form.data.code.trim() === ''}
                            >
                                Turn off
                            </Button>
                        </DialogFooter>
                    </form>
                </DialogContent>
            </Dialog>
        </>
    );
}

// QRCode is dark on light in either theme, with the quiet zone of four
// modules scanners need, so its colours are fixed rather than tokens.
function QRCode({ qr }: { qr: TwoFactorSetup['qr'] }) {
    const quiet = 4;
    const size = qr.size + quiet * 2;

    return (
        <svg
            viewBox={`${-quiet} ${-quiet} ${size} ${size}`}
            shapeRendering="crispEdges"
            role="img"
            aria-label="QR code for your authenticator app"
            className="mx-auto size-48 rounded-lg border bg-white"
        >
            <path d={qr.path} fill="#000" />
        </svg>
    );
}

// groups splits the key in fours, as it's easier to type that way. Apps
// ignore the spaces.
const groups = (secret: string) => secret.match(/.{1,4}/g)?.join(' ') ?? secret;
