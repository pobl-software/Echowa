import { router, useForm } from '@inertiajs/react';
import { DownloadIcon, GlobeIcon, LockIcon, TriangleAlertIcon } from 'lucide-react';
import { type FormEvent, type ReactElement, useState } from 'react';

import { ConfigSection } from '@/Components/ConfigPanel';
import { ConfirmDelete } from '@/Components/ConfirmDelete';
import { TextField } from '@/Components/TextField';
import { Alert, AlertDescription, AlertTitle } from '@/Components/ui/alert';
import { Button, buttonVariants } from '@/Components/ui/button';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
    DialogTrigger,
} from '@/Components/ui/dialog';
import type { App, TemplateOption } from '@/types/models';

// ExternalAccessSection publishes a template database on a host port, for
// clients such as pgAdmin outside the server, with TLS on (ADR 0026), in
// its Connection section. Each change redeploys the app.
export function ExternalAccessSection({ app, template }: { app: App; template: TemplateOption }) {
    const port = app.external_port;

    return (
        <ConfigSection
            icon={GlobeIcon}
            title="External access"
            description="For clients outside the server, such as a desktop app. Off unless you turn it on."
        >
            <div className="flex flex-wrap items-center justify-between gap-3">
                <p className="min-w-0 flex-1 basis-64 text-sm text-muted-foreground">
                    {port ? (
                        <>
                            On. Clients outside the server connect to port{' '}
                            <span className="font-mono text-foreground">{port}</span> on its public IP, with the same
                            user and password.
                        </>
                    ) : (
                        'Off. Only apps on this server can connect.'
                    )}
                </p>
                <div className="flex gap-2">
                    {port ? (
                        <>
                            <PortDialog
                                app={app}
                                template={template}
                                trigger={<Button variant="outline" size="sm" />}
                                label="Change port"
                            />
                            <ConfirmDelete
                                trigger={
                                    <Button variant="outline" size="sm">
                                        Turn off
                                    </Button>
                                }
                                title="Turn off external access?"
                                confirmLabel="Turn off and redeploy"
                                onConfirm={() => router.delete(`/apps/${app.id}/external-access`)}
                            >
                                Port {port} closes, and {app.name} redeploys now. Apps on this server keep connecting as
                                before.
                            </ConfirmDelete>
                        </>
                    ) : (
                        <PortDialog
                            app={app}
                            template={template}
                            trigger={<Button size="sm" />}
                            label="Enable external access"
                        />
                    )}
                </div>
            </div>
            {port && template.tls_client && <Encryption app={app} client={template.tls_client} />}
            {port && <FirewallWarning port={port} />}
        </ConfigSection>
    );
}

function PortDialog({
    app,
    template,
    trigger,
    label,
}: {
    app: App;
    template: TemplateOption;
    trigger: ReactElement;
    label: string;
}) {
    const [open, setOpen] = useState(false);
    const form = useForm({ external_port: app.external_port ? String(app.external_port) : '' });

    const submit = (e: FormEvent) => {
        e.preventDefault();
        form.post(`/apps/${app.id}/external-access`, { onSuccess: () => setOpen(false) });
    };

    return (
        <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger render={trigger}>
                {!app.external_port && <GlobeIcon />}
                {label}
            </DialogTrigger>
            <DialogContent>
                <form onSubmit={submit} className="grid gap-4">
                    <DialogHeader>
                        <DialogTitle>External access to {app.name}</DialogTitle>
                        <DialogDescription>
                            Publishes {template.name} on a port of this server&apos;s public IP. Anyone who can reach
                            the port can try to sign in. {app.name} redeploys now, so it&apos;s unavailable for a few
                            seconds.
                        </DialogDescription>
                    </DialogHeader>
                    <TextField
                        id="external_port"
                        label="Port"
                        type="number"
                        inputMode="numeric"
                        min={1024}
                        max={65535}
                        placeholder="4040"
                        className="font-mono"
                        autoFocus
                        value={form.data.external_port}
                        onChange={(e) => form.setData('external_port', e.target.value)}
                        error={form.errors.external_port}
                    />
                    <FirewallWarning port={Number(form.data.external_port) || undefined} />
                    <DialogFooter>
                        <Button type="submit" disabled={form.processing}>
                            {app.external_port ? 'Save and redeploy' : 'Enable and redeploy'}
                        </Button>
                    </DialogFooter>
                </form>
            </DialogContent>
        </Dialog>
    );
}

// Encryption says the port serves TLS with the app's own certificate, and
// how a client verifies it with the app's CA.
function Encryption({ app, client }: { app: App; client: string }) {
    return (
        <div className="flex flex-wrap items-start justify-between gap-3 text-sm">
            <div className="flex min-w-0 flex-1 basis-64 gap-2.5">
                <LockIcon className="mt-0.5 size-4 shrink-0 text-success" />
                <div className="min-w-0">
                    <p className="font-medium">Encrypted with TLS</p>
                    <p className="mt-0.5 text-muted-foreground">
                        The certificate comes from this app&apos;s own CA. {client}
                    </p>
                </div>
            </div>
            <a
                href={`/apps/${app.id}/external-access/ca.crt`}
                download
                className={buttonVariants({ variant: 'outline', size: 'sm' })}
            >
                <DownloadIcon />
                CA certificate
            </a>
        </div>
    );
}

// Docker writes its own iptables rules for published ports, ahead of ufw's,
// so a ufw rule doesn't limit who reaches this one.
function FirewallWarning({ port }: { port?: number }) {
    return (
        <Alert>
            <TriangleAlertIcon />
            <AlertTitle>Limit it in your provider&apos;s firewall</AlertTitle>
            <AlertDescription>
                Docker opens {port ? `port ${port}` : 'the port'} ahead of ufw, so a ufw rule won&apos;t restrict it. To
                keep it private, allow it only from your own IP in your cloud provider&apos;s firewall.
            </AlertDescription>
        </Alert>
    );
}
