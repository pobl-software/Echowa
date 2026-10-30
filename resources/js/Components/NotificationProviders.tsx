import { router, useForm } from '@inertiajs/react';
import {
    BellIcon,
    CircleCheckIcon,
    CircleXIcon,
    Loader2Icon,
    PencilIcon,
    PlusIcon,
    SendIcon,
    Trash2Icon,
} from 'lucide-react';
import { type FormEvent, type ReactElement, type ReactNode, useState } from 'react';

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
import { Field, FieldDescription, FieldError, FieldLabel } from '@/Components/ui/field';
import { Input } from '@/Components/ui/input';
import { InputGroup, InputGroupAddon, InputGroupInput } from '@/Components/ui/input-group';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/Components/ui/select';
import { Switch } from '@/Components/ui/switch';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/Components/ui/table';
import { toast } from '@/Components/ui/toast';
import { postJson, RequestError } from '@/lib/http';
import { kindOrder, notificationKinds } from '@/lib/notifications';
import { cn } from '@/lib/utils';
import type { NotificationKind, NotificationProvider, NotificationTest } from '@/types/models';

// A secret is null to keep the stored one (ADR 0040), or what to replace
// it with. An optional secret replaced with '' is removed.
type Secret = string | null;
type Security = 'starttls' | 'tls' | 'none';

interface ProviderForm {
    name: string;
    kind: NotificationKind;
    webhook_url: Secret;
    server: string;
    topic: Secret;
    token: Secret;
    host: string;
    port: number | '';
    security: Security;
    username: string;
    password: Secret;
    from: string;
    to: string;
    api_key: Secret;
}

const secretFields = ['webhook_url', 'topic', 'token', 'password', 'api_key'] as const;

// The form a saved provider fills, with every stored secret kept.
function formFor(provider?: NotificationProvider): ProviderForm {
    const stored = (name: (typeof secretFields)[number]): Secret => (provider?.stored.includes(name) ? null : '');
    const c = provider?.config ?? {};

    return {
        name: provider?.name ?? '',
        kind: provider?.kind ?? 'slack',
        webhook_url: stored('webhook_url'),
        server: c.server ?? 'https://ntfy.sh',
        topic: stored('topic'),
        token: stored('token'),
        host: c.host ?? '',
        port: c.port ?? 587,
        security: c.security ?? 'starttls',
        username: c.username ?? '',
        password: stored('password'),
        from: c.from ?? '',
        to: c.to?.join(', ') ?? '',
        api_key: stored('api_key'),
    };
}

// payload trims the secrets, which the server doesn't filter: a filter
// would turn null into ''.
function payload(data: ProviderForm) {
    const out: Record<string, unknown> = { ...data };
    for (const name of secretFields) {
        out[name] = data[name]?.trim() ?? null;
    }

    return out;
}

// sendTest sends the sample message and waits for the answer: the request
// only starts it, since a send can take longer than a request may.
async function sendTest(body: unknown): Promise<NotificationTest> {
    const { id } = await postJson<{ id: string }>('/settings/notifications/test', body);
    for (let i = 0; i < 30; i++) {
        await new Promise((resolve) => setTimeout(resolve, 500));
        const response = await fetch(`/settings/notifications/test/${id}`, { headers: { Accept: 'application/json' } });
        const result = (await response.json().catch(() => ({}))) as NotificationTest & { message?: string };
        if (!response.ok) {
            return { state: 'failed', error: result.message ?? `Request failed (${response.status})` };
        }
        if (result.state !== 'sending') {
            return result;
        }
    }

    return { state: 'failed', error: 'No answer after 15 seconds.' };
}

// NotificationProviders is Settings' Notifications section (ADR 0040):
// where alerts go. Secrets are sent, never shown.
export function NotificationProviders({ providers }: { providers: NotificationProvider[] }) {
    return (
        <Panel
            flush={providers.length > 0}
            title="Notifications"
            description="Where Echowa sends alerts: a Slack, Discord or Teams channel, a phone through ntfy, or email."
            actions={
                providers.length > 0 && (
                    <ProviderDialog
                        trigger={
                            <Button variant="outline">
                                <PlusIcon />
                                Add provider
                            </Button>
                        }
                    />
                )
            }
        >
            {providers.length === 0 ? (
                <div className="flex flex-col items-center rounded-xl border border-dashed px-6 py-10 text-center">
                    <span className="mb-4 flex size-11 items-center justify-center rounded-xl border bg-card">
                        <BellIcon className="size-5 text-muted-foreground" />
                    </span>
                    <h2 className="font-medium">No providers yet</h2>
                    <p className="mt-1 max-w-md text-sm text-muted-foreground">
                        Add one for each place you want to hear from Echowa. Test sends a sample message there.
                    </p>
                    <ProviderDialog trigger={<Button className="mt-5">Add a provider</Button>} />
                </div>
            ) : (
                <Table>
                    <TableHeader>
                        <TableRow>
                            <TableHead>Name</TableHead>
                            <TableHead className="hidden sm:table-cell">Kind</TableHead>
                            <TableHead className="hidden md:table-cell">Sends to</TableHead>
                            <TableHead>On</TableHead>
                            <TableHead className="text-right">Actions</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {providers.map((provider) => (
                            <ProviderRow key={provider.id} provider={provider} />
                        ))}
                    </TableBody>
                </Table>
            )}
        </Panel>
    );
}

function ProviderRow({ provider }: { provider: NotificationProvider }) {
    const kind = notificationKinds[provider.kind];
    const [testing, setTesting] = useState(false);

    const test = async () => {
        setTesting(true);
        try {
            const result = await sendTest({ ...payload(formFor(provider)), id: provider.id });
            if (result.state === 'sent') {
                toast.add({ type: 'success', title: `Sent a test to ${provider.name}` });
            } else {
                toast.add({ type: 'error', title: `Couldn’t send to ${provider.name}`, description: result.error });
            }
        } catch (e) {
            toast.add({
                type: 'error',
                title: `Couldn’t send to ${provider.name}`,
                description: e instanceof RequestError ? (Object.values(e.errors)[0] ?? e.message) : String(e),
            });
        }
        setTesting(false);
    };

    return (
        <TableRow>
            {/* Takes the spare width, so a long target truncates instead of
                pushing the switch off a phone's screen. */}
            <TableCell className="w-full max-w-0">
                <div className="flex items-center gap-2.5">
                    <span className="flex size-7 shrink-0 items-center justify-center rounded-md border bg-muted/50">
                        <kind.icon className="size-3.5" />
                    </span>
                    <div className="min-w-0">
                        <div className="truncate font-medium">{provider.name}</div>
                        <div className="truncate text-xs text-muted-foreground sm:hidden">{kind.label}</div>
                        <div className="truncate font-mono text-xs text-muted-foreground md:hidden">
                            {provider.target}
                        </div>
                    </div>
                </div>
            </TableCell>
            <TableCell className="hidden sm:table-cell">{kind.label}</TableCell>
            <TableCell className="hidden max-w-64 truncate font-mono text-xs md:table-cell" title={provider.target}>
                {provider.target}
            </TableCell>
            <TableCell>
                <Switch
                    checked={provider.enabled}
                    aria-label={provider.enabled ? `Turn off ${provider.name}` : `Turn on ${provider.name}`}
                    title={provider.enabled ? 'Sends notifications' : 'Off: sends nothing'}
                    onCheckedChange={(enabled) =>
                        router.put(
                            `/settings/notifications/${provider.id}/enabled`,
                            { enabled },
                            { preserveScroll: true },
                        )
                    }
                />
            </TableCell>
            <TableCell>
                <div className="flex justify-end gap-1">
                    <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={`Send a test to ${provider.name}`}
                        title="Send a test"
                        onClick={test}
                        disabled={testing}
                    >
                        {testing ? <Loader2Icon className="animate-spin" /> : <SendIcon />}
                    </Button>
                    <ProviderDialog
                        provider={provider}
                        trigger={
                            <Button variant="ghost" size="icon-sm" aria-label={`Edit ${provider.name}`} title="Edit">
                                <PencilIcon />
                            </Button>
                        }
                    />
                    <ConfirmDelete
                        title={`Delete ${provider.name}?`}
                        trigger={
                            <Button
                                variant="ghost"
                                size="icon-sm"
                                className="text-destructive"
                                aria-label={`Delete ${provider.name}`}
                                title="Delete"
                            >
                                <Trash2Icon />
                            </Button>
                        }
                        onConfirm={() =>
                            router.delete(`/settings/notifications/${provider.id}`, { preserveScroll: true })
                        }
                    >
                        Echowa forgets this provider and its secrets. Nothing changes at {kind.label}.
                    </ConfirmDelete>
                </div>
            </TableCell>
        </TableRow>
    );
}

const namePlaceholder: Record<NotificationKind, string> = {
    slack: 'Slack #ops',
    discord: 'Discord #alerts',
    teams: 'Teams Ops',
    ntfy: 'My phone',
    smtp: 'Ops email',
    resend: 'Ops email',
};

const webhookHelp: Partial<Record<NotificationKind, ReactNode>> = {
    slack: (
        <>
            Create a Slack app with Incoming Webhooks on, add a webhook to the channel and paste its URL. Mattermost’s
            incoming webhooks take the same URL.
        </>
    ),
    discord: <>In the channel’s settings: Integrations, Webhooks, New Webhook, then Copy Webhook URL.</>,
    teams: (
        <>
            In Teams, create a workflow from “Post to a channel when a webhook request is received” and paste its URL.
            Office 365 connector URLs no longer work.
        </>
    ),
};

const securityItems: { value: Security; label: string; port: number }[] = [
    { value: 'starttls', label: 'STARTTLS (usually port 587)', port: 587 },
    { value: 'tls', label: 'TLS (usually port 465)', port: 465 },
    { value: 'none', label: 'None (localhost only)', port: 25 },
];

const noSpinner =
    '[appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none';

function ProviderDialog({ provider, trigger }: { provider?: NotificationProvider; trigger: ReactElement }) {
    const [open, setOpen] = useState(false);
    const form = useForm<ProviderForm>(formFor(provider));
    const [test, setTest] = useState<NotificationTest | null>(null);

    const reset = () => {
        form.setDefaults(formFor(provider));
        form.reset();
        form.clearErrors();
        setTest(null);
    };
    const submit = (e: FormEvent) => {
        e.preventDefault();
        form.transform(payload);
        const options = { preserveScroll: true, onSuccess: () => setOpen(false) };
        if (provider) {
            form.put(`/settings/notifications/${provider.id}`, options);
        } else {
            form.post('/settings/notifications', options);
        }
    };
    const runTest = async () => {
        setTest({ state: 'sending' });
        form.clearErrors();
        try {
            setTest(await sendTest({ ...payload(form.data), id: provider?.id }));
        } catch (e) {
            if (e instanceof RequestError && Object.keys(e.errors).length > 0) {
                form.setError(e.errors as Partial<Record<keyof ProviderForm, string>>);
                setTest(null);
            } else {
                setTest({ state: 'failed', error: e instanceof Error ? e.message : String(e) });
            }
        }
    };

    const kind = form.data.kind;
    const text = (key: 'name' | 'server' | 'host' | 'username' | 'from' | 'to') => ({
        value: form.data[key],
        onChange: (e: React.ChangeEvent<HTMLInputElement>) => form.setData(key, e.target.value),
        error: form.errors[key],
    });
    const secret = (key: (typeof secretFields)[number]) => ({
        id: key,
        value: form.data[key],
        stored: provider?.stored.includes(key) ?? false,
        onChange: (value: Secret) => form.setData(key, value),
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
                        <DialogTitle>{provider ? `Edit ${provider.name}` : 'Add a notification provider'}</DialogTitle>
                        <DialogDescription>
                            A provider is only where messages go. Which alerts go there is set on each alert.
                        </DialogDescription>
                    </DialogHeader>

                    <div className="-mx-4 grid min-h-0 content-start gap-5 overflow-y-auto px-4 py-0.5">
                        {!provider && (
                            <Field>
                                <FieldLabel>Kind</FieldLabel>
                                <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
                                    {kindOrder.map((k) => {
                                        const info = notificationKinds[k];
                                        return (
                                            <button
                                                key={k}
                                                type="button"
                                                aria-pressed={kind === k}
                                                onClick={() => {
                                                    form.setData('kind', k);
                                                    form.clearErrors();
                                                    setTest(null);
                                                }}
                                                className={cn(
                                                    'flex flex-col items-center gap-2 rounded-lg border px-2 py-3 text-sm transition-colors outline-none hover:bg-muted/50 focus-visible:ring-3 focus-visible:ring-ring/50',
                                                    kind === k && 'border-primary bg-primary/5 ring-1 ring-primary',
                                                )}
                                            >
                                                <info.icon className="size-5" />
                                                {info.label}
                                            </button>
                                        );
                                    })}
                                </div>
                                {form.errors.kind && <FieldError>{form.errors.kind}</FieldError>}
                            </Field>
                        )}

                        <TextField id="name" label="Name" placeholder={namePlaceholder[kind]} {...text('name')} />

                        {(kind === 'slack' || kind === 'discord' || kind === 'teams') && (
                            <SecretField
                                label="Webhook URL"
                                placeholder="https://"
                                description={webhookHelp[kind]}
                                {...secret('webhook_url')}
                            />
                        )}

                        {kind === 'ntfy' && (
                            <>
                                <TextField
                                    id="server"
                                    label="Server"
                                    className="font-mono"
                                    autoComplete="off"
                                    description="ntfy.sh, or your own ntfy server."
                                    {...text('server')}
                                />
                                <SecretField
                                    label="Topic"
                                    description="On ntfy.sh anyone who knows the topic can read it, so make it long. Subscribe to it in the ntfy app."
                                    suggest={() => `echowa-${randomName()}`}
                                    {...secret('topic')}
                                />
                                <SecretField
                                    label="Access token"
                                    optional
                                    placeholder="Optional"
                                    description="For a topic that needs signing in to, such as on your own server."
                                    {...secret('token')}
                                />
                            </>
                        )}

                        {kind === 'smtp' && (
                            <>
                                <div className="grid gap-5 sm:grid-cols-[minmax(0,1fr)_auto]">
                                    <TextField
                                        id="host"
                                        label="Host"
                                        className="font-mono"
                                        placeholder="smtp.example.com"
                                        autoComplete="off"
                                        {...text('host')}
                                    />
                                    <Field data-invalid={!!form.errors.port || undefined}>
                                        <FieldLabel htmlFor="port">Port</FieldLabel>
                                        <InputGroup className="max-w-36">
                                            <InputGroupAddon className="font-mono">:</InputGroupAddon>
                                            <InputGroupInput
                                                id="port"
                                                type="number"
                                                inputMode="numeric"
                                                min={1}
                                                max={65535}
                                                className={cn('font-mono', noSpinner)}
                                                value={form.data.port}
                                                onChange={(e) =>
                                                    form.setData(
                                                        'port',
                                                        e.target.value === '' ? '' : Number(e.target.value),
                                                    )
                                                }
                                                aria-invalid={!!form.errors.port || undefined}
                                            />
                                        </InputGroup>
                                        {form.errors.port && <FieldError>{form.errors.port}</FieldError>}
                                    </Field>
                                </div>
                                <Field data-invalid={!!form.errors.security || undefined}>
                                    <FieldLabel htmlFor="security">Encryption</FieldLabel>
                                    <Select
                                        items={securityItems}
                                        value={form.data.security}
                                        onValueChange={(value) => {
                                            const item = securityItems.find((i) => i.value === value);
                                            if (!item) {
                                                return;
                                            }
                                            // Move the port with it while it's still one of the defaults.
                                            const defaults = securityItems.map((i) => i.port);
                                            form.setData((data) => ({
                                                ...data,
                                                security: item.value,
                                                port:
                                                    data.port === '' || defaults.includes(data.port)
                                                        ? item.port
                                                        : data.port,
                                            }));
                                        }}
                                    >
                                        <SelectTrigger
                                            id="security"
                                            className="w-full"
                                            aria-invalid={!!form.errors.security || undefined}
                                        >
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent>
                                            {securityItems.map((item) => (
                                                <SelectItem key={item.value} value={item.value}>
                                                    {item.label}
                                                </SelectItem>
                                            ))}
                                        </SelectContent>
                                    </Select>
                                    {form.errors.security && <FieldError>{form.errors.security}</FieldError>}
                                </Field>
                                <div className="grid gap-5 sm:grid-cols-2">
                                    <TextField
                                        id="username"
                                        label="Username"
                                        placeholder="Optional"
                                        autoComplete="off"
                                        {...text('username')}
                                    />
                                    <SecretField
                                        label="Password"
                                        optional
                                        placeholder="Optional"
                                        {...secret('password')}
                                    />
                                </div>
                            </>
                        )}

                        {kind === 'resend' && (
                            <SecretField
                                label="API key"
                                placeholder="re_"
                                description="A key with sending access, from Resend’s API Keys page."
                                {...secret('api_key')}
                            />
                        )}

                        {(kind === 'smtp' || kind === 'resend') && (
                            <>
                                <TextField
                                    id="from"
                                    label="From"
                                    type="email"
                                    placeholder="alerts@example.com"
                                    description={
                                        kind === 'resend'
                                            ? 'An address on a domain you’ve verified in Resend.'
                                            : 'Sent as “Echowa”.'
                                    }
                                    {...text('from')}
                                />
                                <TextField
                                    id="to"
                                    label="To"
                                    placeholder="you@example.com"
                                    description="Up to 10 addresses, separated by commas."
                                    {...text('to')}
                                />
                            </>
                        )}

                        {test && <TestResult test={test} />}
                    </div>

                    <DialogFooter>
                        <Button type="button" variant="outline" onClick={runTest} disabled={test?.state === 'sending'}>
                            {test?.state === 'sending' ? <Loader2Icon className="animate-spin" /> : <SendIcon />}
                            {test?.state === 'sending' ? 'Sending…' : 'Send a test'}
                        </Button>
                        <Button type="submit" disabled={form.processing}>
                            {provider ? 'Save' : 'Add provider'}
                        </Button>
                    </DialogFooter>
                </form>
            </DialogContent>
        </Dialog>
    );
}

function randomName() {
    const bytes = new Uint8Array(9);
    crypto.getRandomValues(bytes);

    return Array.from(bytes, (b) => b.toString(36).padStart(2, '0'))
        .join('')
        .slice(0, 16);
}

// SecretField is a secret's input. A stored secret shows as kept until
// Replace; an optional one replaced with nothing is removed on save.
function SecretField({
    id,
    label,
    value,
    stored,
    optional,
    onChange,
    error,
    description,
    placeholder,
    suggest,
}: {
    id: string;
    label: string;
    value: Secret;
    stored: boolean;
    optional?: boolean;
    onChange: (value: Secret) => void;
    error?: string;
    description?: ReactNode;
    placeholder?: string;
    // Fills in a fresh value, such as a random ntfy topic.
    suggest?: () => string;
}) {
    const action =
        value === null ? (
            <Button type="button" variant="link" size="xs" className="h-auto p-0" onClick={() => onChange('')}>
                Replace
            </Button>
        ) : stored ? (
            <Button type="button" variant="link" size="xs" className="h-auto p-0" onClick={() => onChange(null)}>
                Keep the stored one
            </Button>
        ) : suggest ? (
            <Button type="button" variant="link" size="xs" className="h-auto p-0" onClick={() => onChange(suggest())}>
                Suggest one
            </Button>
        ) : null;

    return (
        <Field data-invalid={!!error || undefined}>
            <div className="flex items-center justify-between gap-2">
                <FieldLabel htmlFor={id}>{label}</FieldLabel>
                {action}
            </div>
            {value === null ? (
                <div className="flex h-8 items-center rounded-lg border bg-muted/40 px-2.5 text-sm text-muted-foreground">
                    Stored, and never shown
                </div>
            ) : (
                <Input
                    id={id}
                    name={id}
                    type={suggest ? 'text' : 'password'}
                    className="font-mono"
                    autoComplete="new-password"
                    placeholder={stored && optional ? 'Leave empty to remove it' : placeholder}
                    value={value}
                    onChange={(e) => onChange(e.target.value)}
                    aria-invalid={!!error || undefined}
                />
            )}
            {error && <FieldError>{error}</FieldError>}
            {description && <FieldDescription>{description}</FieldDescription>}
        </Field>
    );
}

function TestResult({ test }: { test: NotificationTest }) {
    if (test.state === 'sending') {
        return (
            <p className="flex items-center gap-2 text-sm text-muted-foreground">
                <Loader2Icon className="size-4 animate-spin" />
                Sending a test message…
            </p>
        );
    }

    return (
        <div className="flex gap-2 rounded-lg border bg-muted/40 px-3 py-2.5 text-sm">
            {test.state === 'sent' ? (
                <>
                    <CircleCheckIcon className="mt-0.5 size-4 shrink-0 text-success" />
                    <span>Sent. Check that it arrived.</span>
                </>
            ) : (
                <>
                    <CircleXIcon className="mt-0.5 size-4 shrink-0 text-destructive" />
                    <div className="min-w-0">
                        <div className="font-medium">Couldn’t send</div>
                        <div className="break-words text-muted-foreground">{test.error}</div>
                    </div>
                </>
            )}
        </div>
    );
}
