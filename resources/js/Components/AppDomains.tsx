import { router } from '@inertiajs/react';
import {
    CheckIcon,
    ExternalLinkIcon,
    GlobeIcon,
    PencilIcon,
    PlusIcon,
    RefreshCwIcon,
    ShieldCheckIcon,
    Trash2Icon,
    TriangleAlertIcon,
    WaypointsIcon,
    XIcon,
} from 'lucide-react';
import { type FormEvent, type ReactElement, type ReactNode, useEffect, useState } from 'react';

import { ConfigPanel, ConfigSection } from '@/Components/ConfigPanel';
import { ConfirmDelete } from '@/Components/ConfirmDelete';
import { StatusPill } from '@/Components/StatusBadge';
import { TextField } from '@/Components/TextField';
import { Alert, AlertDescription, AlertTitle } from '@/Components/ui/alert';
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
import { FieldDescription } from '@/Components/ui/field';
import { postJson } from '@/lib/http';
import { formatDate, formatDateTime, timeAgo } from '@/lib/time';
import { cn } from '@/lib/utils';
import type { App, CertificateCheck, DNSCheck, DomainStatus } from '@/types/models';

type Tone = 'success' | 'warning' | 'danger' | 'neutral';

const dnsStates: Record<DNSCheck['state'], { tone: Tone; label: string }> = {
    points_here: { tone: 'success', label: 'Points here' },
    local: { tone: 'success', label: 'Local' },
    elsewhere: { tone: 'warning', label: 'Points elsewhere' },
    missing: { tone: 'danger', label: 'No records' },
    unknown: { tone: 'neutral', label: 'Unknown' },
};

const certificateStates: Record<CertificateCheck['state'], { tone: Tone; label: string }> = {
    active: { tone: 'success', label: 'Active' },
    renewal_overdue: { tone: 'warning', label: 'Renewal overdue' },
    pending: { tone: 'warning', label: 'Not issued yet' },
    expired: { tone: 'danger', label: 'Expired' },
    invalid: { tone: 'danger', label: 'Invalid' },
    unreachable: { tone: 'danger', label: 'Unreachable' },
    off: { tone: 'neutral', label: 'HTTP only' },
};

// AppDomains is a custom app's Domains section: where it's reached from
// outside, whether DNS points here, and the certificate Traefik serves.
// Changes happen through dialogs, so its ConfigPanel has no SaveBar.
export function AppDomains({ app, status }: { app: App; status: DomainStatus | null | undefined }) {
    const [checking, setChecking] = useState(false);
    const refresh = () =>
        router.reload({
            only: ['domainStatus'],
            onStart: () => setChecking(true),
            onFinish: () => setChecking(false),
        });

    // The checks are read only when asked for, so ask on arrival and after
    // each change.
    // biome-ignore lint/correctness/useExhaustiveDependencies: a change to the domain or TLS is what reruns the checks; refresh is rebuilt every render
    useEffect(() => {
        if (app.domain) {
            refresh();
        }
    }, [app.domain, app.tls]);

    const domain = app.domain;
    // A check of the domain before a change is no check of this one.
    const current = domain && status && status.domain === domain ? status : undefined;

    return (
        <ConfigPanel
            footer={
                domain && (
                    <div className="flex items-center gap-2 border-t px-4 py-3 sm:px-5">
                        <p className="min-w-0 text-sm text-muted-foreground">
                            {checking || !current ? (
                                'Checking DNS and the certificate…'
                            ) : (
                                <span title={formatDateTime(current.checked_at)}>
                                    Checked {timeAgo(current.checked_at)}
                                </span>
                            )}
                        </p>
                        <Button variant="outline" size="sm" className="ml-auto" onClick={refresh} disabled={checking}>
                            <RefreshCwIcon className={cn(checking && 'animate-spin')} />
                            Check again
                        </Button>
                    </div>
                )
            }
        >
            <ConfigSection
                icon={GlobeIcon}
                title="Domain"
                description={`Where ${app.name} is reached from outside, through Traefik.`}
            >
                {domain ? <DomainRow app={app} domain={domain} /> : <NoDomain app={app} />}
            </ConfigSection>

            {domain && (
                <ConfigSection
                    icon={WaypointsIcon}
                    title="DNS"
                    description="Where the domain's records point. Traefik routes it once they reach this server."
                >
                    <Facts>
                        <Fact label="Status">
                            {current ? <Pill {...dnsStates[current.dns.state]} /> : <Checking />}
                        </Fact>
                        {current && <DnsFacts check={current.dns} />}
                    </Facts>
                    {current && <DnsAdvice domain={domain} check={current.dns} />}
                </ConfigSection>
            )}

            {domain && (
                <ConfigSection
                    icon={ShieldCheckIcon}
                    title="Certificate"
                    description="What Traefik serves over HTTPS. Let's Encrypt issues it, and Traefik renews it."
                >
                    <Facts>
                        <Fact label="Status">
                            {current ? <Pill {...certificateStates[current.certificate.state]} /> : <Checking />}
                        </Fact>
                        {current && <CertificateFacts check={current.certificate} />}
                    </Facts>
                    {current && <CertificateAdvice domain={domain} check={current.certificate} />}
                </ConfigSection>
            )}
        </ConfigPanel>
    );
}

function NoDomain({ app }: { app: App }) {
    return (
        <div className="flex flex-wrap items-center justify-between gap-3">
            <p className="min-w-0 flex-1 basis-64 text-sm text-muted-foreground">
                None yet. Only apps on this server reach it, at{' '}
                <code className="text-foreground">
                    echowa-{app.name}:{app.port}
                </code>
                .
            </p>
            <DomainDialog
                app={app}
                trigger={
                    <Button size="sm">
                        <PlusIcon />
                        Add domain
                    </Button>
                }
            />
        </div>
    );
}

function DomainRow({ app, domain }: { app: App; domain: string }) {
    const url = `${app.tls ? 'https' : 'http'}://${domain}`;

    return (
        <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="min-w-0 flex-1 basis-48">
                <a
                    href={url}
                    target="_blank"
                    rel="noreferrer"
                    className="group flex items-center gap-1.5 font-mono text-sm"
                >
                    <span className="truncate group-hover:underline">{url}</span>
                    <ExternalLinkIcon className="size-3.5 shrink-0 text-muted-foreground" />
                </a>
                <p className="mt-0.5 text-sm text-muted-foreground">
                    {app.tls ? "HTTPS, with a Let's Encrypt certificate." : 'Plain HTTP.'}
                </p>
            </div>
            <div className="flex gap-2">
                <DomainDialog
                    app={app}
                    trigger={<Button variant="outline" size="sm" />}
                    label={
                        <>
                            <PencilIcon />
                            Change
                        </>
                    }
                />
                <ConfirmDelete
                    title={`Remove ${domain}?`}
                    confirmLabel="Remove"
                    trigger={
                        <Button variant="outline" size="sm">
                            <Trash2Icon />
                            Remove
                        </Button>
                    }
                    onConfirm={() => router.delete(`/apps/${app.id}/domain`, { preserveScroll: true })}
                >
                    After the next deploy, Traefik stops routing <code>{domain}</code> to {app.name}, and it&apos;s
                    reachable only from apps on this server.
                </ConfirmDelete>
            </div>
        </div>
    );
}

// Facts is a check's findings, a label and a value a row. The section
// already frames them, so there's no box.
function Facts({ children }: { children: ReactNode }) {
    return <dl className="grid gap-2 text-sm">{children}</dl>;
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
    return (
        <div className="flex flex-wrap items-baseline gap-x-3">
            <dt className="w-28 shrink-0 text-xs text-muted-foreground">{label}</dt>
            <dd className="min-w-0 break-words">{children}</dd>
        </div>
    );
}

function Pill({ tone, label }: { tone: Tone; label: string }) {
    return (
        <StatusPill tone={tone} plain={tone === 'neutral'}>
            {label}
        </StatusPill>
    );
}

function Checking() {
    return <span className="animate-pulse text-muted-foreground">Checking…</span>;
}

function Muted({ children }: { children: ReactNode }) {
    return <p className="text-sm text-muted-foreground">{children}</p>;
}

function DnsFacts({ check }: { check: DNSCheck }) {
    if (check.state === 'local') {
        return <Fact label="Resolves to">this machine, as a .localhost name</Fact>;
    }
    if (check.records.length === 0) {
        return null;
    }

    return (
        <Fact label="Resolves to">
            <Records check={check} />
        </Fact>
    );
}

// Records lists the addresses a domain resolves to, each marked as this
// server's or not.
function Records({ check }: { check: DNSCheck }) {
    if (check.records.length === 0) {
        return null;
    }

    return (
        <ul className="space-y-1">
            {check.records.map((record) => (
                <li key={record.address} className="flex items-center gap-2">
                    {record.here ? (
                        <CheckIcon className="size-3.5 shrink-0 text-success" aria-label="This server" />
                    ) : (
                        <XIcon className="size-3.5 shrink-0 text-warning" aria-label="Not this server" />
                    )}
                    <span className="font-mono text-xs">{record.address}</span>
                    <span className="text-xs text-muted-foreground">
                        {record.here ? 'this server' : 'not this server'}
                    </span>
                </li>
            ))}
        </ul>
    );
}

function DnsAdvice({ domain, check }: { domain: string; check: DNSCheck }) {
    const server = check.server.length > 0 ? check.server.join(' or ') : "this server's address";
    switch (check.state) {
        case 'elsewhere':
            return (
                <Muted>
                    This server is <span className="font-mono text-xs text-foreground">{server}</span>. A record proxied
                    through Cloudflare shows Cloudflare&apos;s addresses, which is fine.
                </Muted>
            );
        case 'missing':
            return (
                <Muted>
                    Add an A record for <span className="text-foreground">{domain}</span> pointing at{' '}
                    <span className="font-mono text-xs text-foreground">{server}</span>.
                </Muted>
            );
        case 'unknown':
            return <Muted>{check.error}</Muted>;
        default:
            return null;
    }
}

function CertificateFacts({ check }: { check: CertificateCheck }) {
    if (check.state === 'off' || check.state === 'pending' || check.state === 'unreachable') {
        return null;
    }

    return (
        <>
            <Fact label="Issuer">{check.issuer}</Fact>
            <Fact label="Expires">
                <span title={formatDateTime(check.not_after)}>{formatDate(check.not_after)}</span>
            </Fact>
            <Fact label="Renews">
                <span title="Traefik renews a certificate when a third of its life is left: 30 days for Let's Encrypt's.">
                    around {formatDate(check.renews_at)}
                </span>
            </Fact>
        </>
    );
}

function CertificateAdvice({ domain, check }: { domain: string; check: CertificateCheck }) {
    switch (check.state) {
        case 'off':
            return <Muted>Served over plain HTTP. Change the domain to turn HTTPS on.</Muted>;
        case 'pending':
            return (
                <Muted>
                    Traefik serves its own placeholder for now. Let&apos;s Encrypt issues the certificate once {domain}{' '}
                    points here and the app is deployed with it, usually within a minute.
                </Muted>
            );
        case 'renewal_overdue':
            return <Muted>Traefik should have renewed it by now. Check Traefik&apos;s log for ACME errors.</Muted>;
        case 'expired':
            return <Muted>Traefik couldn&apos;t renew it. Check that DNS points here and Traefik&apos;s log.</Muted>;
        case 'unreachable':
        case 'invalid':
            return <Muted>{check.error}</Muted>;
        default:
            return null;
    }
}

// DomainDialog adds the domain, or changes it and HTTPS. A new domain's
// DNS is checked first: one that doesn't point here can still be added,
// for DNS that's still updating or a proxied record.
function DomainDialog({ app, trigger, label }: { app: App; trigger: ReactElement; label?: ReactNode }) {
    const [open, setOpen] = useState(false);
    const [domain, setDomain] = useState(app.domain ?? '');
    const [tls, setTls] = useState(app.tls);
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState<string>();
    // The check of the domain as typed, when it found a problem.
    const [warning, setWarning] = useState<DNSCheck>();

    const changeOpen = (next: boolean) => {
        setOpen(next);
        if (next) {
            setDomain(app.domain ?? '');
            setTls(app.tls);
            setError(undefined);
            setWarning(undefined);
        }
    };

    const save = () => {
        router.put(
            `/apps/${app.id}/domain`,
            { domain: domain.trim(), tls },
            {
                preserveScroll: true,
                onStart: () => setBusy(true),
                onFinish: () => setBusy(false),
                onSuccess: () => setOpen(false),
                onError: (errors) => setError(errors.domain),
            },
        );
    };

    const check = async () => {
        setBusy(true);
        setError(undefined);
        try {
            const result = await postJson<DNSCheck>(`/apps/${app.id}/domain/check`, {
                domain,
            });
            if (result.state === 'points_here' || result.state === 'local') {
                setWarning(undefined);
                save();
                return;
            }
            setWarning(result);
        } catch (e) {
            setError(e instanceof Error ? e.message : String(e));
        }
        setBusy(false);
    };

    const submit = (e: FormEvent) => {
        e.preventDefault();
        // Only HTTPS changed: the domain was checked when it was added.
        if (app.domain && domain.trim().toLowerCase() === app.domain) {
            save();
        } else {
            check();
        }
    };

    const local = /(^|\.)localhost$/.test(domain.trim().toLowerCase());

    return (
        <Dialog open={open} onOpenChange={changeOpen}>
            <DialogTrigger render={trigger}>{label}</DialogTrigger>
            <DialogContent>
                <form onSubmit={submit} className="grid gap-4">
                    <DialogHeader>
                        <DialogTitle>
                            {app.domain ? `Change ${app.name}'s domain` : `Add a domain to ${app.name}`}
                        </DialogTitle>
                        <DialogDescription>
                            Point its DNS at this server first. Echowa checks it before saving, and Traefik routes it
                            from the next deploy.
                        </DialogDescription>
                    </DialogHeader>
                    <div>
                        <TextField
                            id="domain"
                            label="Domain"
                            placeholder="app.example.com"
                            autoFocus
                            autoComplete="off"
                            value={domain}
                            onChange={(e) => {
                                setDomain(e.target.value);
                                setWarning(undefined);
                            }}
                            error={error}
                        />
                        <FieldDescription className="mt-2">
                            Locally, use <code>{app.name}.localhost</code>.
                        </FieldDescription>
                    </div>
                    <div>
                        <label className="flex items-center gap-2 text-sm">
                            <Checkbox
                                aria-labelledby="tls-label"
                                checked={tls}
                                onCheckedChange={(checked) => setTls(checked)}
                            />
                            <span id="tls-label">HTTPS with a Let&apos;s Encrypt certificate</span>
                        </label>
                        {local && tls && (
                            <FieldDescription className="mt-2">
                                Let&apos;s Encrypt can&apos;t issue for .localhost. Turn HTTPS off locally.
                            </FieldDescription>
                        )}
                    </div>
                    {warning && <DnsWarning domain={domain.trim().toLowerCase()} check={warning} />}
                    <DialogFooter>
                        {warning ? (
                            <>
                                <Button type="submit" variant="outline" disabled={busy}>
                                    <RefreshCwIcon className={cn(busy && 'animate-spin')} />
                                    Check again
                                </Button>
                                <Button type="button" onClick={save} disabled={busy}>
                                    {app.domain ? 'Save anyway' : 'Add anyway'}
                                </Button>
                            </>
                        ) : (
                            <Button type="submit" disabled={busy || domain.trim() === ''}>
                                {busy ? 'Checking DNS…' : app.domain ? 'Save' : 'Add domain'}
                            </Button>
                        )}
                    </DialogFooter>
                </form>
            </DialogContent>
        </Dialog>
    );
}

const warningTitles: Partial<Record<DNSCheck['state'], (domain: string) => string>> = {
    elsewhere: (domain) => `${domain} doesn't point at this server`,
    missing: (domain) => `${domain} has no A or AAAA records`,
    unknown: (domain) => `Couldn't check ${domain}`,
};

function DnsWarning({ domain, check }: { domain: string; check: DNSCheck }) {
    return (
        <Alert>
            <TriangleAlertIcon className="text-warning" />
            <AlertTitle>{warningTitles[check.state]?.(domain)}</AlertTitle>
            <AlertDescription className="grid gap-2">
                <Records check={check} />
                <DnsAdvice domain={domain} check={check} />
                <p>
                    DNS changes can take a few minutes to show. Let&apos;s Encrypt issues a certificate only once it
                    points here.
                </p>
            </AlertDescription>
        </Alert>
    );
}
