import { Tooltip, TooltipContent, TooltipTrigger } from '@/Components/ui/tooltip';
import { formatDateTime } from '@/lib/time';
import { cn } from '@/lib/utils';
import type { DeploymentStatus, LiveStatus, RunStatus } from '@/types/models';

type Tone = 'success' | 'warning' | 'danger' | 'neutral' | 'primary';

const tones: Record<Tone, { pill: string; dot: string; halo: string }> = {
    success: { pill: 'bg-success/10 text-success', dot: 'bg-success', halo: 'ring-success/15' },
    warning: { pill: 'bg-warning/12 text-warning', dot: 'bg-warning', halo: 'ring-warning/15' },
    danger: { pill: 'bg-destructive/10 text-destructive', dot: 'bg-destructive', halo: 'ring-destructive/15' },
    neutral: {
        pill: 'bg-muted text-muted-foreground',
        dot: 'bg-muted-foreground/60',
        halo: 'ring-muted-foreground/15',
    },
    primary: { pill: 'bg-primary/10 text-primary', dot: 'bg-primary', halo: 'ring-primary/15' },
};

// StatusPill is a state at a glance: a coloured dot and a word. pulse marks
// something in progress. plain drops the dot, for a label that isn't a state.
export function StatusPill({
    tone,
    pulse,
    plain,
    title,
    children,
}: {
    tone: Tone;
    pulse?: boolean;
    plain?: boolean;
    title?: string;
    children: React.ReactNode;
}) {
    const { pill, dot } = tones[tone];

    return (
        <span
            title={title}
            className={cn(
                'inline-flex h-6 shrink-0 items-center gap-1.5 rounded-full px-2.5 text-xs font-medium whitespace-nowrap',
                pill,
            )}
        >
            {!plain && (
                <span className="relative flex size-1.5">
                    {pulse && (
                        <span
                            className={cn('absolute inline-flex size-full animate-ping rounded-full opacity-70', dot)}
                        />
                    )}
                    <span className={cn('relative inline-flex size-1.5 rounded-full', dot)} />
                </span>
            )}
            {children}
        </span>
    );
}

// StatusDot is a StatusPill without its word, for a state that's fine and
// needs no reading. Hovering it shows the word.
export function StatusDot({ tone, label }: { tone: Tone; label: React.ReactNode }) {
    const { dot, halo } = tones[tone];

    return (
        <Tooltip>
            <TooltipTrigger render={<span className="inline-flex h-6 w-4 shrink-0 items-center justify-center" />}>
                <span className={cn('size-2 rounded-full ring-4', dot, halo)} />
                <span className="sr-only">{label}</span>
            </TooltipTrigger>
            <TooltipContent>{label}</TooltipContent>
        </Tooltip>
    );
}

const live: Record<LiveStatus['state'], { label: string; tone: Tone }> = {
    running: { label: 'Running', tone: 'success' },
    degraded: { label: 'Degraded', tone: 'warning' },
    down: { label: 'Down', tone: 'danger' },
    not_deployed: { label: 'Not deployed', tone: 'neutral' },
    unknown: { label: 'Unknown', tone: 'neutral' },
};

// The app's Swarm service: "Running 2/2", "Degraded 1/2", "Not deployed"...
// Short of replicas for over 2 minutes, it's "Unhealthy". quiet shows a
// healthy, settled service as a dot alone.
export function LiveStatusBadge({ status, quiet }: { status: LiveStatus; quiet?: boolean }) {
    const { label, tone } = status.unhealthy_since
        ? { label: 'Unhealthy', tone: 'danger' as const }
        : live[status.state];
    const counts = status.state === 'running' || status.state === 'degraded' || status.state === 'down';
    const updating = status.update === 'updating' || status.update === 'rollback_started';
    const title = [
        status.unhealthy_since && `Fewer replicas running than desired since ${formatDateTime(status.unhealthy_since)}`,
        status.update && `Last update: ${status.update.replace('_', ' ')}`,
    ]
        .filter(Boolean)
        .join('. ');

    if (quiet && status.state === 'running' && !status.unhealthy_since && !updating) {
        return (
            <StatusDot
                tone="success"
                label={
                    <>
                        {label}{' '}
                        <span className="tabular-nums">
                            {status.running}/{status.desired}
                        </span>
                    </>
                }
            />
        );
    }

    return (
        <StatusPill tone={tone} pulse={updating} title={title || undefined}>
            {label}
            {counts && (
                <span className="tabular-nums opacity-80">
                    {status.running}/{status.desired}
                </span>
            )}
        </StatusPill>
    );
}

const deployment: Record<DeploymentStatus, { label: string; tone: Tone; pulse?: boolean }> = {
    queued: { label: 'Queued', tone: 'neutral', pulse: true },
    pulling: { label: 'Pulling', tone: 'warning', pulse: true },
    deploying: { label: 'Deploying', tone: 'warning', pulse: true },
    succeeded: { label: 'Succeeded', tone: 'success' },
    failed: { label: 'Failed', tone: 'danger' },
    rolled_back: { label: 'Rolled back', tone: 'danger' },
};

// quiet shows a deployment that succeeded as a dot alone.
export function DeploymentStatusBadge({ status, quiet }: { status: DeploymentStatus; quiet?: boolean }) {
    const { label, tone, pulse } = deployment[status];

    if (quiet && status === 'succeeded') {
        return <StatusDot tone={tone} label={label} />;
    }

    return (
        <StatusPill tone={tone} pulse={pulse}>
            {label}
        </StatusPill>
    );
}

// Marks the deployment the app is running now. It sits next to the
// deployment's status, so it has no dot of its own.
export function CurrentBadge() {
    return (
        <StatusPill tone="primary" plain title="The version running now">
            Current
        </StatusPill>
    );
}

const run: Record<RunStatus, { label: string; tone: Tone; pulse?: boolean }> = {
    running: { label: 'Running', tone: 'warning', pulse: true },
    succeeded: { label: 'Succeeded', tone: 'success' },
    failed: { label: 'Failed', tone: 'danger' },
    timed_out: { label: 'Timed out', tone: 'danger' },
    skipped: { label: 'Skipped', tone: 'neutral' },
    interrupted: { label: 'Interrupted', tone: 'neutral' },
};

// How a scheduled task's run, a backup or a restore went (ADRs 0032, 0034).
// quiet shows one that succeeded as a dot alone.
export function RunStatusBadge({ status, quiet }: { status: RunStatus; quiet?: boolean }) {
    const { label, tone, pulse } = run[status];

    if (quiet && status === 'succeeded') {
        return <StatusDot tone={tone} label={label} />;
    }

    return (
        <StatusPill tone={tone} pulse={pulse}>
            {label}
        </StatusPill>
    );
}
