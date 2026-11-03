import { router } from '@inertiajs/react';
import { EyeIcon, EyeOffIcon, RefreshCwIcon, ShieldXIcon, WebhookIcon } from 'lucide-react';
import { Fragment, type ReactNode, useEffect, useState } from 'react';

import { ConfigPanel, ConfigSection } from '@/Components/ConfigPanel';
import { CopyButton } from '@/Components/CopyButton';
import { Panel } from '@/Components/Panel';
import { StatusPill, type Tone } from '@/Components/StatusBadge';
import {
    AlertDialog,
    AlertDialogAction,
    AlertDialogCancel,
    AlertDialogContent,
    AlertDialogDescription,
    AlertDialogFooter,
    AlertDialogHeader,
    AlertDialogTitle,
    AlertDialogTrigger,
} from '@/Components/ui/alert-dialog';
import { Button } from '@/Components/ui/button';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/Components/ui/tabs';
import { toast } from '@/Components/ui/toast';
import { postJson } from '@/lib/http';
import { formatDateTime, timeAgo } from '@/lib/time';
import type { App, CIInfo, HookCall, HookPostman, HookRefusal } from '@/types/models';

const masked = '••••••••••••';

// AppCIDeploys is a custom app's Webhooks section (ADR 0047): the
// webhook CI calls, the steps that call it, and the calls it refused.
export function AppCIDeploys({ app, ci }: { app: App; ci: CIInfo }) {
    // A refused call shows up without reopening the section. A partial
    // reload, so an open tab still times out (ADR 0043).
    useEffect(() => {
        const id = window.setInterval(() => router.reload({ only: ['ci'] }), 10_000);
        return () => window.clearInterval(id);
    }, []);

    return (
        <div className="space-y-6">
            <ConfigPanel>
                <ConfigSection
                    icon={WebhookIcon}
                    title="Webhook"
                    description="CI signs a request with the secret and sends it here to deploy a new image."
                >
                    <Webhook app={app} url={ci.url} />
                </ConfigSection>
            </ConfigPanel>
            <SetUp snippets={ci.snippets} postman={ci.postman} />
            <RefusedCalls calls={ci.calls} />
        </div>
    );
}

// Webhook is the URL and the secret, which stays on the server until
// Reveal or a copy asks for it.
function Webhook({ app, url }: { app: App; url: string }) {
    const [shown, setShown] = useState<string | null>(null);
    const [busy, setBusy] = useState(false);

    const fetchSecret = async () => {
        setBusy(true);
        try {
            const { secret } = await postJson<{ secret: string }>(`/apps/${app.id}/webhook/reveal`, {});
            return secret;
        } catch (error) {
            toast.add({ type: 'error', title: 'Couldn’t get the secret', description: String(error) });
            return null;
        } finally {
            setBusy(false);
        }
    };
    const reveal = async () => {
        setShown(await fetchSecret());
    };

    return (
        <>
            <dl className="grid grid-cols-[4rem_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 rounded-lg border bg-muted/40 py-2 pr-2 pl-3 text-sm">
                <dt className="text-muted-foreground">URL</dt>
                <dd className="font-mono break-all select-all">{url}</dd>
                <CopyButton label="URL" value={url} />
                <dt className="text-muted-foreground">Secret</dt>
                <dd className="font-mono break-all select-all">
                    {shown ?? <span className="text-muted-foreground">{masked}</span>}
                </dd>
                <CopyButton label="secret" value={fetchSecret} disabled={busy} onRefused={reveal} />
            </dl>
            <p className="text-sm text-muted-foreground">
                Add the secret to your CI as a secret variable named{' '}
                <code className="font-mono text-xs text-foreground">ECHOWA_SECRET</code>.
            </p>
            <div className="flex flex-wrap gap-2">
                <Button
                    variant="outline"
                    size="sm"
                    onClick={shown ? () => setShown(null) : reveal}
                    disabled={busy}
                    aria-label={shown ? 'Hide secret' : 'Reveal secret'}
                >
                    {shown ? <EyeOffIcon /> : <EyeIcon />}
                    {shown ? 'Hide' : 'Reveal'}
                </Button>
                <RotateButton app={app} onRotated={() => setShown(null)} />
            </div>
        </>
    );
}

function RotateButton({ app, onRotated }: { app: App; onRotated: () => void }) {
    const [sending, setSending] = useState(false);

    const rotate = () =>
        router.post(
            `/apps/${app.id}/webhook/rotate`,
            {},
            {
                preserveScroll: true,
                onStart: () => setSending(true),
                onSuccess: onRotated,
                onFinish: () => setSending(false),
            },
        );

    return (
        <AlertDialog>
            <AlertDialogTrigger
                render={
                    <Button variant="outline" size="sm" disabled={sending}>
                        <RefreshCwIcon />
                        Rotate
                    </Button>
                }
            />
            <AlertDialogContent>
                <AlertDialogHeader>
                    <AlertDialogTitle>Rotate {app.name}’s secret?</AlertDialogTitle>
                    <AlertDialogDescription>
                        The old secret stops working straight away. CI that still signs with it gets 401 until you
                        update <code className="font-mono text-foreground">ECHOWA_SECRET</code>.
                    </AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                    <AlertDialogCancel>Cancel</AlertDialogCancel>
                    <AlertDialogAction onClick={rotate}>Rotate secret</AlertDialogAction>
                </AlertDialogFooter>
            </AlertDialogContent>
        </AlertDialog>
    );
}

// SetUp is a CI step per kind of CI, with the app's name and URL filled
// in, and the same request for Postman, to try the webhook by hand.
function SetUp({ snippets, postman }: { snippets: CIInfo['snippets']; postman: HookPostman }) {
    const [tab, setTab] = useState(snippets[0]?.id ?? 'postman');
    const open = snippets.find((s) => s.id === tab);

    return (
        <Panel
            title="Set up your CI"
            description={
                open
                    ? 'Run this after the image is pushed. It needs date, openssl and curl 7.76 or later.'
                    : 'Send a deploy by hand. The script fills in the timestamp and signature before each send.'
            }
        >
            <Tabs value={tab} onValueChange={(value) => setTab(String(value))}>
                {/* Copy sits by the tabs, not over the code, which runs to the edge on phones. */}
                <div className="flex items-center justify-between gap-2">
                    {/* Four tabs are wider than a phone's panel, so they scroll. */}
                    <TabsList className="max-w-full min-w-0 justify-start overflow-x-auto">
                        {snippets.map((s) => (
                            <TabsTrigger key={s.id} value={s.id}>
                                {s.label}
                            </TabsTrigger>
                        ))}
                        <TabsTrigger value="postman">Postman</TabsTrigger>
                    </TabsList>
                    {open && <CopyButton label={`${open.label} step`} value={open.code} />}
                </div>
                {snippets.map((s) => (
                    <TabsContent key={s.id} value={s.id}>
                        <Code>{s.code}</Code>
                    </TabsContent>
                ))}
                <TabsContent value="postman">
                    <PostmanRequest postman={postman} />
                </TabsContent>
            </Tabs>
        </Panel>
    );
}

function Code({ children }: { children: string }) {
    return (
        <pre className="overflow-x-auto rounded-lg border bg-muted/40 p-3 font-mono text-xs leading-relaxed">
            {children}
        </pre>
    );
}

// PostmanRequest is the webhook as Postman's request editor lays it out:
// the URL, Headers, Body and the pre-request script, each to copy.
function PostmanRequest({ postman }: { postman: HookPostman }) {
    // Postman's Bulk Edit takes headers as "Name: value" lines.
    const headers = postman.headers.map((h) => `${h.name}: ${h.value}`).join('\n');

    return (
        <div className="space-y-4 text-sm">
            <PostmanPart title="Request" copy="URL" value={postman.url}>
                <div className="flex min-w-0 items-center gap-2 rounded-lg border bg-muted/40 px-3 py-2 font-mono text-xs">
                    <span className="font-semibold text-primary">POST</span>
                    <span className="break-all select-all">{postman.url}</span>
                </div>
            </PostmanPart>
            <PostmanPart title="Headers" copy="headers" value={headers}>
                <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-6 gap-y-1 rounded-lg border bg-muted/40 px-3 py-2 font-mono text-xs">
                    {postman.headers.map((h) => (
                        <Fragment key={h.name}>
                            <dt className="text-muted-foreground">{h.name}</dt>
                            <dd className="break-all">{h.value}</dd>
                        </Fragment>
                    ))}
                </dl>
            </PostmanPart>
            <PostmanPart title="Body" hint="raw, JSON" copy="body" value={postman.body}>
                <Code>{postman.body}</Code>
            </PostmanPart>
            <PostmanPart title="Scripts" hint="Pre-request" copy="script" value={postman.script}>
                <Code>{postman.script}</Code>
            </PostmanPart>
            <p className="text-muted-foreground">
                Add the secret to a Postman environment as{' '}
                <code className="font-mono text-xs text-foreground">ECHOWA_SECRET</code>, and keep the body as it is:
                the script signs it, so it can’t hold variables.
            </p>
        </div>
    );
}

function PostmanPart({
    title,
    hint,
    copy,
    value,
    children,
}: {
    title: string;
    hint?: string;
    copy: string;
    value: string;
    children: ReactNode;
}) {
    return (
        <div className="space-y-1.5">
            <div className="flex items-center justify-between gap-2">
                <h4 className="font-medium">
                    {title}
                    {hint && <span className="ml-2 font-normal text-muted-foreground">{hint}</span>}
                </h4>
                <CopyButton label={copy} value={value} />
            </div>
            {children}
        </div>
    );
}

const refusals: Record<HookRefusal, { label: string; tone: Tone }> = {
    malformed: { label: 'Bad headers', tone: 'danger' },
    clock: { label: 'Clock', tone: 'danger' },
    signature: { label: 'Bad signature', tone: 'danger' },
    too_large: { label: 'Too large', tone: 'warning' },
    body: { label: 'Bad body', tone: 'warning' },
    ref: { label: 'Bad ref', tone: 'warning' },
    stopped: { label: 'Stopped', tone: 'warning' },
    rate_limited: { label: 'Rate limited', tone: 'warning' },
};

// RefusedCalls are the last calls the webhook turned away, kept in memory
// since Echowa started, with the error each caller was sent.
function RefusedCalls({ calls }: { calls: HookCall[] }) {
    return (
        <Panel
            title="Refused calls"
            description="The last 10 calls the webhook turned away since Echowa started, with the error CI was sent."
            flush
        >
            {calls.length === 0 ? (
                <div className="flex flex-col items-center px-6 py-10 text-center">
                    <ShieldXIcon className="mb-3 size-5 text-muted-foreground" />
                    <p className="text-sm text-muted-foreground">None. A call with a bad signature would show here.</p>
                </div>
            ) : (
                <ul className="divide-y">
                    {calls.map((call, i) => {
                        const refusal = refusals[call.reason] ?? { label: call.reason, tone: 'neutral' };
                        return (
                            <li
                                // biome-ignore lint/suspicious/noArrayIndexKey: calls have no ID, and the rows hold no state.
                                key={i}
                                className="flex flex-col gap-1 px-4 py-3 sm:flex-row sm:items-start sm:gap-4 sm:px-5"
                            >
                                <div className="flex items-center gap-3 sm:w-56 sm:shrink-0">
                                    <StatusPill tone={refusal.tone}>{refusal.label}</StatusPill>
                                    <span className="text-sm text-muted-foreground" title={formatDateTime(call.at)}>
                                        {timeAgo(call.at)}
                                    </span>
                                </div>
                                <div className="min-w-0 flex-1 text-sm">
                                    <p className="break-words">{call.message}</p>
                                    <p className="font-mono text-xs text-muted-foreground">{call.ip}</p>
                                </div>
                            </li>
                        );
                    })}
                </ul>
            )}
        </Panel>
    );
}
