import { Link, useForm } from '@inertiajs/react';
import {
    HardDriveIcon,
    HeartPulseIcon,
    LayersIcon,
    MinusIcon,
    NetworkIcon,
    PackageIcon,
    PlusIcon,
    SquareTerminalIcon,
} from 'lucide-react';
import type { FormEvent, ReactNode } from 'react';

import type { AppSection } from '@/Components/AppNav';

import { ConfigPanel, ConfigSection, SaveBar } from '@/Components/ConfigPanel';
import { SectionLink } from '@/Components/SectionLink';
import { TextField } from '@/Components/TextField';
import { Field, FieldDescription, FieldError, FieldLabel } from '@/Components/ui/field';
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from '@/Components/ui/input-group';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/Components/ui/select';
import { formatCommand } from '@/lib/command';
import { cn } from '@/lib/utils';
import type { App, AppVolume, RegistryOption, TemplateOption } from '@/types/models';

const maxReplicas = 20;
const noSpinner =
    '[appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none';

// DockerConfig is an app's Docker config section: the image a deploy
// pulls and how Swarm runs it.
export function DockerConfig({
    app,
    registries,
    template,
    onSelect,
}: {
    app: App;
    registries: RegistryOption[];
    template?: TemplateOption;
    // Switches to another section, such as a custom app's Volumes.
    onSelect: (section: AppSection) => void;
}) {
    const form = useForm({
        repository: app.repository,
        default_tag: app.default_tag,
        registry_id: app.registry_id ? String(app.registry_id) : '',
        port: String(app.port),
        replicas: String(app.replicas),
        healthcheck_cmd: app.healthcheck_cmd ?? '',
        command: formatCommand(app.args),
    });
    const volumes = app.volumes ?? [];
    const hasVolumes = volumes.length > 0;
    const routed = template?.http ?? true;
    const registry = registries.find((r) => String(r.id) === form.data.registry_id);
    // null is no registry: Base UI shows a null item's label when nothing is chosen.
    const registryItems = [
        { value: null, label: 'None (public image)' },
        ...registries.map((r) => ({ value: String(r.id), label: `${r.name} (${r.host})` })),
    ];
    const replicas = Number(form.data.replicas) || 1;
    const port = form.data.port || String(app.port);

    form.transform((data) => ({
        ...data,
        registry_id: data.registry_id === '' ? null : Number(data.registry_id),
        healthcheck_cmd: data.healthcheck_cmd.trim() === '' ? null : data.healthcheck_cmd,
        port: data.port === '' ? null : Number(data.port),
        replicas: data.replicas === '' ? null : Number(data.replicas),
    }));

    const submit = (e: FormEvent) => {
        e.preventDefault();
        form.put(`/apps/${app.id}`, { preserveScroll: true, onSuccess: () => form.setDefaults() });
    };
    const discard = () => {
        form.reset();
        form.clearErrors();
    };
    const step = (by: number) => form.setData('replicas', String(Math.min(maxReplicas, Math.max(1, replicas + by))));

    return (
        <form onSubmit={submit}>
            <ConfigPanel
                footer={
                    <SaveBar
                        dirty={form.isDirty}
                        processing={form.processing}
                        onDiscard={discard}
                        note="They apply on the next deploy."
                    />
                }
            >
                <ConfigSection
                    icon={PackageIcon}
                    title="Image"
                    description="What a deploy pulls. A deploy can name another tag; this one is the default."
                >
                    <div className="grid gap-4 sm:grid-cols-[1fr_9rem]">
                        <TextField
                            id="repository"
                            label="Repository"
                            placeholder="ghcr.io/owner/app"
                            className="font-mono"
                            value={form.data.repository}
                            onChange={(e) => form.setData('repository', e.target.value)}
                            error={form.errors.repository}
                        />
                        <TextField
                            id="default_tag"
                            label="Default tag"
                            className="font-mono"
                            value={form.data.default_tag}
                            onChange={(e) => form.setData('default_tag', e.target.value)}
                            error={form.errors.default_tag}
                        />
                    </div>
                    {template && hasVolumes && (
                        <FieldDescription className="-mt-2">
                            Its volume holds data this version wrote, which another major version may not read:
                            PostgreSQL starts with an empty database. Change the tag&apos;s major version only on
                            purpose.
                        </FieldDescription>
                    )}
                    <Field data-invalid={!!form.errors.registry_id || undefined}>
                        <FieldLabel htmlFor="registry_id">Registry</FieldLabel>
                        <Select
                            items={registryItems}
                            value={form.data.registry_id || null}
                            onValueChange={(value) => form.setData('registry_id', value ?? '')}
                        >
                            <SelectTrigger
                                id="registry_id"
                                className="w-full"
                                aria-invalid={!!form.errors.registry_id || undefined}
                            >
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                {registryItems.map((item) => (
                                    <SelectItem key={item.value ?? 'none'} value={item.value}>
                                        {item.label}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                        {form.errors.registry_id && <FieldError>{form.errors.registry_id}</FieldError>}
                        <FieldDescription>
                            {registry ? (
                                <>Echowa signs in to {registry.host} before it pulls.</>
                            ) : registries.length === 0 ? (
                                <>
                                    Private image? <Link href="/registries/create">Add a registry</Link> first.
                                </>
                            ) : (
                                'Pulled without signing in.'
                            )}
                        </FieldDescription>
                    </Field>
                </ConfigSection>

                <ConfigSection
                    icon={NetworkIcon}
                    title="Networking"
                    description="The port the app listens on inside its container."
                >
                    <Field data-invalid={!!form.errors.port || undefined}>
                        <FieldLabel htmlFor="port">Container port</FieldLabel>
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
                                onChange={(e) => form.setData('port', e.target.value)}
                                aria-invalid={!!form.errors.port || undefined}
                            />
                        </InputGroup>
                        {form.errors.port && <FieldError>{form.errors.port}</FieldError>}
                    </Field>
                    <dl className="grid gap-1.5 rounded-lg border bg-muted/40 p-3 text-sm">
                        {routed && (
                            <Route label="From outside">
                                {app.domain ? (
                                    <code>
                                        {app.tls ? 'https' : 'http'}://{app.domain}
                                    </code>
                                ) : (
                                    <span className="text-muted-foreground">No domain yet</span>
                                )}
                            </Route>
                        )}
                        <Route label="From apps here">
                            <code>
                                echowa-{app.name}:{port}
                            </code>
                        </Route>
                    </dl>
                </ConfigSection>

                <ConfigSection
                    icon={LayersIcon}
                    title="Replicas"
                    description="How many copies Swarm keeps running. A deploy replaces them one at a time."
                >
                    <Field data-invalid={!!form.errors.replicas || undefined}>
                        <FieldLabel htmlFor="replicas">Replicas</FieldLabel>
                        <InputGroup className="max-w-36">
                            <InputGroupAddon>
                                <InputGroupButton
                                    size="icon-xs"
                                    aria-label="One fewer"
                                    disabled={hasVolumes}
                                    onClick={() => step(-1)}
                                >
                                    <MinusIcon />
                                </InputGroupButton>
                            </InputGroupAddon>
                            <InputGroupInput
                                id="replicas"
                                type="number"
                                inputMode="numeric"
                                min={1}
                                max={hasVolumes ? 1 : maxReplicas}
                                disabled={hasVolumes}
                                className={cn('text-center tabular-nums', noSpinner)}
                                value={form.data.replicas}
                                onChange={(e) => form.setData('replicas', e.target.value)}
                                aria-invalid={!!form.errors.replicas || undefined}
                            />
                            <InputGroupAddon align="inline-end">
                                <InputGroupButton
                                    size="icon-xs"
                                    aria-label="One more"
                                    disabled={hasVolumes}
                                    onClick={() => step(1)}
                                >
                                    <PlusIcon />
                                </InputGroupButton>
                            </InputGroupAddon>
                        </InputGroup>
                        {form.errors.replicas && <FieldError>{form.errors.replicas}</FieldError>}
                        {hasVolumes && (
                            <FieldDescription>
                                One, because two copies can&apos;t share a volume&apos;s data.
                            </FieldDescription>
                        )}
                    </Field>
                </ConfigSection>

                {/* A template's arguments come from the template. */}
                {!template && (
                    <ConfigSection
                        icon={SquareTerminalIcon}
                        title="Command"
                        description="Runs in place of the image's CMD, such as php artisan horizon for a queue worker."
                    >
                        <Field data-invalid={!!form.errors.command || undefined}>
                            <FieldLabel htmlFor="command">Command</FieldLabel>
                            <InputGroup>
                                <InputGroupAddon className="font-mono">$</InputGroupAddon>
                                <InputGroupInput
                                    id="command"
                                    className="font-mono"
                                    placeholder="php artisan horizon"
                                    value={form.data.command}
                                    onChange={(e) => form.setData('command', e.target.value)}
                                    aria-invalid={!!form.errors.command || undefined}
                                />
                            </InputGroup>
                            {form.errors.command && <FieldError>{form.errors.command}</FieldError>}
                            <FieldDescription>
                                Split at spaces, with quotes kept together, and run without a shell. Leave empty to use
                                the image&apos;s own CMD.
                            </FieldDescription>
                        </Field>
                    </ConfigSection>
                )}

                <ConfigSection
                    icon={HeartPulseIcon}
                    title="Healthcheck"
                    description="Run inside the container with its shell. A deploy that never passes it is rolled back."
                >
                    <Field data-invalid={!!form.errors.healthcheck_cmd || undefined}>
                        <FieldLabel htmlFor="healthcheck_cmd">Command</FieldLabel>
                        <InputGroup>
                            <InputGroupAddon className="font-mono">$</InputGroupAddon>
                            <InputGroupInput
                                id="healthcheck_cmd"
                                className="font-mono"
                                placeholder={`curl -fsS http://localhost:${port}/up || exit 1`}
                                value={form.data.healthcheck_cmd}
                                onChange={(e) => form.setData('healthcheck_cmd', e.target.value)}
                                aria-invalid={!!form.errors.healthcheck_cmd || undefined}
                            />
                        </InputGroup>
                        {form.errors.healthcheck_cmd && <FieldError>{form.errors.healthcheck_cmd}</FieldError>}
                        <FieldDescription>
                            Leave empty to use the image&apos;s own HEALTHCHECK, if it has one.
                        </FieldDescription>
                    </Field>
                </ConfigSection>

                {hasVolumes && template && (
                    <ConfigSection
                        icon={HardDriveIcon}
                        title="Volumes"
                        description="Data here survives deploys. Set when the app was created."
                    >
                        <VolumeList volumes={volumes} />
                    </ConfigSection>
                )}
                {hasVolumes && !template && (
                    <ConfigSection icon={HardDriveIcon} title="Volumes" description="Data here survives deploys.">
                        <p className="text-sm text-muted-foreground">
                            Add, move and remove them in{' '}
                            <SectionLink app={app} section="volumes" onSelect={onSelect}>
                                <span className="text-foreground">Volumes</span>
                            </SectionLink>
                            .
                        </p>
                    </ConfigSection>
                )}
            </ConfigPanel>
        </form>
    );
}

function Route({ label, children }: { label: string; children: ReactNode }) {
    return (
        <div className="flex flex-wrap items-baseline gap-x-3">
            <dt className="w-28 shrink-0 text-xs text-muted-foreground">{label}</dt>
            <dd className="min-w-0 break-all">{children}</dd>
        </div>
    );
}

// VolumeList lists a template app's volumes: where each is mounted, and its
// Docker name.
function VolumeList({ volumes }: { volumes: AppVolume[] }) {
    return (
        <ul className="divide-y rounded-lg border text-sm">
            {volumes.map((volume) => (
                <li
                    key={volume.id}
                    className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-0.5 px-3 py-2"
                >
                    <span className="font-mono text-xs">{volume.target}</span>
                    <span className="font-mono text-xs text-muted-foreground">{volume.name}</span>
                </li>
            ))}
        </ul>
    );
}
