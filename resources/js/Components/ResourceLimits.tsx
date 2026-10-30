import { router, useForm, usePoll } from '@inertiajs/react';
import { CpuIcon, HardDriveIcon, MemoryStickIcon, MinusIcon, PlusIcon, TriangleAlertIcon } from 'lucide-react';
import type { FormEvent, ReactNode } from 'react';

import { ConfigPanel, ConfigSection, SaveBar } from '@/Components/ConfigPanel';
import { MeterBar } from '@/Components/Meter';
import { Alert, AlertAction, AlertDescription, AlertTitle } from '@/Components/ui/alert';
import { Button } from '@/Components/ui/button';
import { Field, FieldDescription, FieldError, FieldLabel } from '@/Components/ui/field';
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from '@/Components/ui/input-group';
import { formatBytes, formatPercent } from '@/lib/format';
import { formatDateTime, timeAgo } from '@/lib/time';
import { cn } from '@/lib/utils';
import type { AppLive } from '@/types/metrics';
import type { App, ResourceLimits as Limits, LiveStatus, ResourcesInfo } from '@/types/models';

const noSpinner =
    '[appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none';

const cores = (millicores: number) => String(millicores / 1000);
const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;
const mib = 1 << 20;

// The values the plus and minus buttons step through, up to the whole
// server: fractions of a core, then whole cores.
function cpuSteps(host: number): number[] {
    const steps = [100, 250, 500, 750, 1000, 1500, 2000];
    for (let c = 3000; c < host; c += 1000) {
        steps.push(c);
    }
    return [...steps.filter((c) => c < host), host];
}

// Memory steps in the sizes RAM comes in: 64 MiB, doubling.
function memorySteps(host: number): number[] {
    const steps: number[] = [];
    for (let m = 64; m < host; m *= 2) {
        steps.push(m);
    }
    return [...steps, host];
}

// Swap steps: none, then the memory sizes, up to the server's swap.
function swapSteps(host: number): number[] {
    return host > 0 ? [0, ...memorySteps(host)].filter((m, i, all) => m <= host && all.indexOf(m) === i) : [0];
}

// step is the next value in steps above (by 1) or below (by -1) current,
// which needn't be one of them.
function step(steps: number[], current: number, by: 1 | -1): number {
    if (by > 0) {
        return steps.find((s) => s > current) ?? steps[steps.length - 1];
    }
    return [...steps].reverse().find((s) => s < current) ?? steps[0];
}

// "0.42", "under 0.01".
function formatCores(n: number): string {
    return n < 0.01 ? 'under 0.01' : String(Number(n.toFixed(2)));
}

// ResourceLimits is an app's Resource limits section: the most CPU and
// memory each of its tasks may use. An empty field is the default, half the
// server.
export function ResourceLimits({
    app,
    status,
    resources,
    metrics,
}: {
    app: App;
    status: LiveStatus;
    resources: ResourcesInfo | null;
    // The app's newest reading, summed over its tasks; null while none run.
    metrics: AppLive | null;
}) {
    usePoll(15_000, { only: ['metrics'] });
    const form = useForm({
        cpu: app.cpu_millicores ? cores(app.cpu_millicores) : '',
        memory: app.memory_mib ? String(app.memory_mib) : '',
        swap: app.swap_mib ? String(app.swap_mib) : '',
    });

    // Errors come back under the names the server validates.
    const errors = form.errors as Partial<Record<'cpu_millicores' | 'memory_mib' | 'swap_mib', string>>;
    form.transform((data) => ({
        cpu_millicores: data.cpu.trim() === '' ? null : Math.round(Number(data.cpu) * 1000),
        memory_mib: data.memory.trim() === '' ? null : Number(data.memory),
        swap_mib: data.swap.trim() === '' ? null : Number(data.swap),
    }));

    if (!resources) {
        return (
            <Alert variant="destructive">
                <TriangleAlertIcon />
                <AlertTitle>Docker isn&apos;t answering</AlertTitle>
                <AlertDescription>
                    The limits depend on how big this server is, which only Docker can say. Try again once it&apos;s
                    back.
                </AlertDescription>
            </Alert>
        );
    }

    const { defaults, host } = resources;
    const saved: Limits = {
        cpu_millicores: app.cpu_millicores ?? defaults.cpu_millicores,
        memory_mib: app.memory_mib ?? defaults.memory_mib,
        swap_mib: app.swap_mib,
    };
    const deployed = app.current_deployment_id !== null;
    // A stopped service keeps the limits it ran with until it's started.
    const stopped = status.state === 'stopped' || status.state === 'stopping';
    const live = status.state !== 'not_deployed' && status.state !== 'unknown' && !stopped;
    const pending =
        deployed &&
        live &&
        (status.limits.cpu_millicores !== saved.cpu_millicores ||
            status.limits.memory_mib !== saved.memory_mib ||
            status.limits.swap_mib !== saved.swap_mib);
    const replicas = app.replicas;

    const submit = (e: FormEvent) => {
        e.preventDefault();
        form.put(`/apps/${app.id}/resources`, { preserveScroll: true, onSuccess: () => form.setDefaults() });
    };
    const discard = () => {
        form.reset();
        form.clearErrors();
    };
    const apply = () =>
        router.put(
            `/apps/${app.id}/resources`,
            { cpu_millicores: app.cpu_millicores, memory_mib: app.memory_mib, swap_mib: app.swap_mib },
            { preserveScroll: true },
        );

    const cpuMillicores = form.data.cpu.trim() === '' ? defaults.cpu_millicores : Number(form.data.cpu) * 1000;
    const memoryMiB = form.data.memory.trim() === '' ? defaults.memory_mib : Number(form.data.memory);
    const cpuLadder = cpuSteps(host.cpu_millicores);
    const memoryLadder = memorySteps(host.memory_mib);
    const swapMiB = form.data.swap.trim() === '' ? 0 : Number(form.data.swap);
    // Unknown until the first metrics reading; the server checks it then.
    const hostSwap = resources.host_swap_mib;
    const swapLadder = swapSteps(hostSwap ?? host.memory_mib);

    // Limits are per task and readings are summed, so compare a task's share.
    const tasks = metrics?.tasks || 1;
    const each = tasks > 1 ? ' a task, on average' : '';
    const cpuUsed = metrics?.cpu_percent != null ? metrics.cpu_percent / 100 / tasks : null;
    const memoryUsed = metrics ? metrics.memory_used_bytes / tasks : null;

    return (
        <div className="space-y-6">
            <OOMAlert status={status} />
            {pending && (
                <Alert>
                    <TriangleAlertIcon className="text-warning" />
                    <AlertTitle>
                        {status.limits.cpu_millicores === 0 && status.limits.memory_mib === 0
                            ? 'Running without limits'
                            : 'Running with other limits'}
                    </AlertTitle>
                    <AlertDescription>
                        The service was last deployed before these limits were set. They apply on the next deploy, or
                        now with a redeploy of the image it runs.
                    </AlertDescription>
                    {!form.isDirty && (
                        <AlertAction>
                            <Button size="sm" variant="outline" onClick={apply}>
                                Apply now
                            </Button>
                        </AlertAction>
                    )}
                </Alert>
            )}
            <form onSubmit={submit}>
                <ConfigPanel
                    footer={
                        <SaveBar
                            dirty={form.isDirty}
                            processing={form.processing}
                            onDiscard={discard}
                            note={
                                stopped
                                    ? 'They apply when you start it.'
                                    : deployed
                                      ? 'Saving redeploys the image it runs.'
                                      : 'They apply on the first deploy.'
                            }
                        />
                    }
                >
                    <ConfigSection
                        icon={CpuIcon}
                        title="CPU"
                        description="The most CPU time each task gets. A busy task slows down at its limit; it isn't stopped."
                    >
                        <LimitField
                            id="cpu"
                            label="Cores"
                            unit="cores"
                            inputMode="decimal"
                            min={0.1}
                            max={host.cpu_millicores / 1000}
                            step={0.1}
                            placeholder={cores(defaults.cpu_millicores)}
                            value={form.data.cpu}
                            onChange={(value) => form.setData('cpu', value)}
                            onStep={(by) => form.setData('cpu', cores(step(cpuLadder, cpuMillicores, by)))}
                            atMin={cpuMillicores <= cpuLadder[0]}
                            atMax={cpuMillicores >= host.cpu_millicores}
                            error={errors.cpu_millicores}
                            usage={
                                <Usage
                                    running={!!metrics}
                                    used={cpuUsed}
                                    limit={cpuMillicores / 1000}
                                    label={(used) => `${formatCores(used)} cores${each}`}
                                />
                            }
                        >
                            Leave empty for the default, {cores(defaults.cpu_millicores)} of the server&apos;s{' '}
                            {plural(host.cpu_millicores / 1000, 'core', 'cores')}.
                            {replicas > 1 &&
                                cpuMillicores > 0 &&
                                ` With ${replicas} replicas, the app can use ${cores(cpuMillicores * replicas)} cores in all.`}
                        </LimitField>
                    </ConfigSection>

                    <ConfigSection
                        icon={MemoryStickIcon}
                        title="Memory"
                        description="The most RAM each task gets. At the limit, a task uses its swap, if it has any, and is killed once that runs out too."
                    >
                        <LimitField
                            id="memory"
                            label="Memory"
                            unit="MiB"
                            inputMode="numeric"
                            min={64}
                            max={host.memory_mib}
                            step={1}
                            placeholder={String(defaults.memory_mib)}
                            value={form.data.memory}
                            onChange={(value) => form.setData('memory', value)}
                            onStep={(by) => form.setData('memory', String(step(memoryLadder, memoryMiB, by)))}
                            atMin={memoryMiB <= memoryLadder[0]}
                            atMax={memoryMiB >= host.memory_mib}
                            error={errors.memory_mib}
                            usage={
                                <Usage
                                    running={!!metrics}
                                    used={memoryUsed}
                                    limit={memoryMiB * mib}
                                    label={(used) => `${formatBytes(used)}${each}`}
                                />
                            }
                        >
                            Leave empty for the default, {formatBytes(defaults.memory_mib * mib)} of the server&apos;s{' '}
                            {formatBytes(host.memory_mib * mib)}.
                            {replicas > 1 &&
                                memoryMiB > 0 &&
                                ` With ${replicas} replicas, the app can use ${formatBytes(memoryMiB * replicas * mib)} in all.`}
                        </LimitField>
                    </ConfigSection>

                    <ConfigSection
                        icon={HardDriveIcon}
                        title="Swap"
                        description="Room on disk past the memory limit, so a task that goes over slows down instead of being killed. Off unless set."
                    >
                        <LimitField
                            id="swap"
                            label="Swap"
                            unit="MiB"
                            inputMode="numeric"
                            min={0}
                            max={hostSwap ?? host.memory_mib}
                            step={1}
                            placeholder="0"
                            value={form.data.swap}
                            onChange={(value) => form.setData('swap', value)}
                            onStep={(by) => form.setData('swap', String(step(swapLadder, swapMiB, by)))}
                            atMin={swapMiB <= 0}
                            atMax={swapMiB >= swapLadder[swapLadder.length - 1]}
                            disabled={hostSwap === 0 && swapMiB === 0}
                            error={errors.swap_mib}
                        >
                            {hostSwap === 0 ? (
                                <>This server has no swap, so there&apos;s nothing to overflow into.</>
                            ) : (
                                <>
                                    Leave empty or 0 for none.
                                    {hostSwap != null &&
                                        ` The server has ${formatBytes(hostSwap * mib)} of swap, shared by every app.`}
                                    {swapMiB > 0 &&
                                        memoryMiB > 0 &&
                                        ` A task can use ${formatBytes((memoryMiB + swapMiB) * mib)} in all, the part past ${formatBytes(memoryMiB * mib)} much more slowly.`}
                                </>
                            )}
                        </LimitField>
                    </ConfigSection>
                </ConfigPanel>
            </form>
        </div>
    );
}

// LimitField is a limit's input between minus and plus buttons, which step
// through the usual sizes, with what the app uses now under it.
function LimitField({
    id,
    label,
    unit,
    value,
    onChange,
    onStep,
    atMin,
    atMax,
    disabled,
    error,
    usage,
    children,
    ...input
}: {
    id: string;
    label: string;
    unit: string;
    inputMode: 'decimal' | 'numeric';
    min: number;
    max: number;
    step: number;
    placeholder: string;
    value: string;
    onChange: (value: string) => void;
    onStep: (by: 1 | -1) => void;
    atMin: boolean;
    atMax: boolean;
    disabled?: boolean;
    error?: string;
    usage?: ReactNode;
    children: ReactNode;
}) {
    return (
        <Field data-invalid={!!error || undefined}>
            <FieldLabel htmlFor={id}>{label}</FieldLabel>
            {/* A button at its end is disabled alone; the group mustn't dim with it. */}
            <InputGroup className={cn('max-w-56', !disabled && 'has-disabled:bg-transparent has-disabled:opacity-100')}>
                <InputGroupAddon>
                    <InputGroupButton
                        size="icon-xs"
                        aria-label="Less"
                        disabled={disabled || atMin}
                        onClick={() => onStep(-1)}
                    >
                        <MinusIcon />
                    </InputGroupButton>
                </InputGroupAddon>
                <InputGroupInput
                    id={id}
                    type="number"
                    className={cn('text-center tabular-nums', noSpinner)}
                    value={value}
                    onChange={(e) => onChange(e.target.value)}
                    aria-invalid={!!error || undefined}
                    disabled={disabled}
                    {...input}
                />
                <InputGroupAddon align="inline-end">
                    {unit}
                    <InputGroupButton
                        size="icon-xs"
                        aria-label="More"
                        disabled={disabled || atMax}
                        onClick={() => onStep(1)}
                    >
                        <PlusIcon />
                    </InputGroupButton>
                </InputGroupAddon>
            </InputGroup>
            {error && <FieldError>{error}</FieldError>}
            {usage}
            <FieldDescription>{children}</FieldDescription>
        </Field>
    );
}

// Usage is what the app uses now against the limit in the field, so a
// limit can be checked before it's saved.
function Usage({
    running,
    used,
    limit,
    label,
}: {
    running: boolean;
    used: number | null;
    limit: number;
    label: (used: number) => string;
}) {
    if (!running || used == null) {
        return (
            <p className="text-xs text-muted-foreground">
                {running ? 'Measuring its use.' : "Not running, so there's no use to show."}
            </p>
        );
    }
    const percent = limit > 0 ? (used / limit) * 100 : null;

    return (
        <div className="max-w-56 space-y-1.5">
            <p className="flex justify-between gap-3 text-xs tabular-nums">
                <span>Using {label(used)}</span>
                {percent != null && <span className="text-muted-foreground">{formatPercent(percent)}</span>}
            </p>
            {percent != null && <MeterBar percent={percent} />}
        </div>
    );
}

// OOMAlert says a task was killed for going over its memory limit, while
// Swarm still keeps that task.
export function OOMAlert({ status }: { status: LiveStatus }) {
    if (!status.oom_killed_at) {
        return null;
    }

    return (
        <Alert variant="destructive">
            <TriangleAlertIcon />
            <AlertTitle>
                Out of memory <span title={formatDateTime(status.oom_killed_at)}>{timeAgo(status.oom_killed_at)}</span>
            </AlertTitle>
            <AlertDescription>
                A task used all {status.limits.memory_mib} MiB it may
                {status.limits.swap_mib > 0 && ` and its ${status.limits.swap_mib} MiB of swap`}, so the kernel killed
                it and Swarm started another. Raise the memory limit, or find what&apos;s using it in Metrics.
            </AlertDescription>
        </Alert>
    );
}
