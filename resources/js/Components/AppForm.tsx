import { Link, useForm } from '@inertiajs/react';
import type { FormEvent } from 'react';

import { NativeSelect } from '@/Components/NativeSelect';
import { TextField } from '@/Components/TextField';
import { Button, buttonVariants } from '@/Components/ui/button';
import { Checkbox } from '@/Components/ui/checkbox';
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/Components/ui/field';
import type { RegistryOption, TemplateOption } from '@/types/models';

interface AppFormProps {
    registries: RegistryOption[];
    // The template a new app starts from. It fills the form, and the server
    // adds its env, volumes and arguments.
    template?: TemplateOption;
}

// The create page's form. After that, the Docker config and Domains tabs
// edit an app. The name can only be set here: it names the Swarm service.
export function AppForm({ registries, template }: AppFormProps) {
    const form = useForm({
        name: '',
        repository: template?.repository ?? '',
        default_tag: template?.default_tag ?? 'latest',
        registry_id: '',
        domain: '',
        port: String(template?.port ?? 3000),
        replicas: '1',
        healthcheck_cmd: template?.healthcheck_cmd ?? '',
        tls: template?.http ?? true,
    });
    const routed = template?.http ?? true;
    const hasVolumes = (template?.volumes.length ?? 0) > 0;

    form.transform((data) => ({
        ...data,
        template: template?.slug ?? null,
        registry_id: data.registry_id === '' ? null : Number(data.registry_id),
        domain: data.domain.trim() === '' ? null : data.domain,
        healthcheck_cmd: data.healthcheck_cmd.trim() === '' ? null : data.healthcheck_cmd,
        port: data.port === '' ? null : Number(data.port),
        replicas: data.replicas === '' ? null : Number(data.replicas),
    }));

    const submit = (e: FormEvent) => {
        e.preventDefault();
        form.post('/apps');
    };

    return (
        <form onSubmit={submit}>
            <FieldGroup>
                <div>
                    <TextField
                        id="name"
                        label="Name"
                        placeholder={template ? template.slug : 'blog'}
                        autoFocus
                        value={form.data.name}
                        onChange={(e) => form.setData('name', e.target.value)}
                        error={form.errors.name}
                    />
                    <FieldDescription className="mt-2">
                        Lowercase letters, digits and hyphens. It names the Swarm service (
                        <code>echowa-{form.data.name || 'name'}</code>) and can&apos;t be changed later.
                    </FieldDescription>
                </div>
                <div className="grid gap-4 sm:grid-cols-[1fr_10rem]">
                    <TextField
                        id="repository"
                        label="Image repository"
                        placeholder="ghcr.io/owner/app"
                        value={form.data.repository}
                        onChange={(e) => form.setData('repository', e.target.value)}
                        error={form.errors.repository}
                    />
                    <TextField
                        id="default_tag"
                        label="Default tag"
                        value={form.data.default_tag}
                        onChange={(e) => form.setData('default_tag', e.target.value)}
                        error={form.errors.default_tag}
                    />
                </div>
                <Field data-invalid={!!form.errors.registry_id || undefined}>
                    <FieldLabel htmlFor="registry_id">Registry</FieldLabel>
                    <NativeSelect
                        id="registry_id"
                        value={form.data.registry_id}
                        onChange={(e) => form.setData('registry_id', e.target.value)}
                        aria-invalid={!!form.errors.registry_id || undefined}
                    >
                        <option value="">None (public image)</option>
                        {registries.map((registry) => (
                            <option key={registry.id} value={registry.id}>
                                {registry.name} ({registry.host})
                            </option>
                        ))}
                    </NativeSelect>
                    {form.errors.registry_id && <FieldError>{form.errors.registry_id}</FieldError>}
                    {registries.length === 0 && (
                        <FieldDescription>
                            Private image? <Link href="/registries/create">Add a registry</Link> first.
                        </FieldDescription>
                    )}
                </Field>
                <div className="grid gap-4 sm:grid-cols-2">
                    <TextField
                        id="port"
                        label="Container port"
                        type="number"
                        min={1}
                        max={65535}
                        value={form.data.port}
                        onChange={(e) => form.setData('port', e.target.value)}
                        error={form.errors.port}
                    />
                    <TextField
                        id="replicas"
                        label="Replicas"
                        type="number"
                        min={1}
                        max={hasVolumes ? 1 : 20}
                        disabled={hasVolumes}
                        value={form.data.replicas}
                        onChange={(e) => form.setData('replicas', e.target.value)}
                        error={form.errors.replicas}
                    />
                </div>
                {hasVolumes && (
                    <FieldDescription className="-mt-3">
                        One replica, because two tasks can&apos;t share a volume&apos;s data.
                    </FieldDescription>
                )}
                <div>
                    <TextField
                        id="healthcheck_cmd"
                        label="Healthcheck command"
                        placeholder="curl -fsS http://localhost:3000/up || exit 1"
                        value={form.data.healthcheck_cmd}
                        onChange={(e) => form.setData('healthcheck_cmd', e.target.value)}
                        error={form.errors.healthcheck_cmd}
                    />
                    <FieldDescription className="mt-2">
                        Run inside the container with its shell. A deploy that never passes it is rolled back. Leave
                        empty to use the image&apos;s own HEALTHCHECK, if any.
                    </FieldDescription>
                </div>
                {routed && (
                    <>
                        <div>
                            <TextField
                                id="domain"
                                label="Domain"
                                placeholder="app.example.com"
                                value={form.data.domain}
                                onChange={(e) => form.setData('domain', e.target.value)}
                                error={form.errors.domain}
                            />
                            <FieldDescription className="mt-2">
                                Point its DNS at this server. Leave empty for an app that isn&apos;t reachable from
                                outside. Locally, use <code>name.localhost</code>.
                            </FieldDescription>
                        </div>
                        <label className="flex items-center gap-2 text-sm">
                            <Checkbox
                                aria-labelledby="tls-label"
                                checked={form.data.tls}
                                onCheckedChange={(checked) => form.setData('tls', checked)}
                            />
                            <span id="tls-label">HTTPS with a Let&apos;s Encrypt certificate</span>
                        </label>
                    </>
                )}
                {template && <TemplateSetup template={template} name={form.data.name} port={form.data.port} />}
                <div className="flex gap-2">
                    <Button type="submit" disabled={form.processing}>
                        {template ? 'Create and deploy' : 'Create app'}
                    </Button>
                    <Link href="/dashboard" className={buttonVariants({ variant: 'outline' })}>
                        Cancel
                    </Link>
                </div>
            </FieldGroup>
        </form>
    );
}

const sources: Record<TemplateOption['env'][number]['source'], string> = {
    generated: 'a generated password',
    app_name: 'the app’s name',
    fixed: 'preset',
};

// TemplateSetup lists what the server adds that the form doesn't show. Values
// stay on the server: the Environment tab reveals them once the app exists.
function TemplateSetup({ template, name, port }: { template: TemplateOption; name: string; port: string }) {
    return (
        <div className="rounded-lg border bg-muted/40 p-3 text-sm sm:p-4">
            <p className="font-medium">Echowa also sets up</p>
            <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5">
                {template.env.map((env) => (
                    <div key={env.key} className="contents">
                        <dt className="font-mono text-xs leading-5">{env.key}</dt>
                        <dd className="text-muted-foreground">{sources[env.source]}</dd>
                    </div>
                ))}
                {template.volumes.map((target) => (
                    <div key={target} className="contents">
                        <dt className="font-mono text-xs leading-5">{target}</dt>
                        <dd className="text-muted-foreground">a volume, so data survives deploys</dd>
                    </div>
                ))}
            </dl>
            {!template.http && (
                <p className="mt-3 text-muted-foreground">
                    Not on the web. Other apps reach it at{' '}
                    <code className="text-foreground">
                        echowa-{name || 'name'}:{port || template.port}
                    </code>
                    .
                </p>
            )}
        </div>
    );
}
