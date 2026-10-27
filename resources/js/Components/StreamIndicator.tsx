import type { StreamEnd, StreamState } from '@/lib/eventStream';
import { cn } from '@/lib/utils';

const labels: Record<StreamState, string> = {
    connecting: 'Connecting…',
    live: 'Live',
    reconnecting: 'Reconnecting…',
    ended: 'Stopped',
    failed: 'Disconnected',
};

// StreamIndicator shows whether a live stream is flowing.
export function StreamIndicator({ state, end }: { state: StreamState; end?: StreamEnd | null }) {
    return (
        <span className="inline-flex items-center gap-1.5 text-sm" title={end?.message}>
            <span className="relative flex size-2">
                {state === 'live' && (
                    <span className="absolute inline-flex size-full animate-ping rounded-full bg-emerald-500 opacity-60" />
                )}
                <span
                    className={cn(
                        'relative inline-flex size-2 rounded-full',
                        state === 'live' && 'bg-emerald-500',
                        (state === 'connecting' || state === 'reconnecting') && 'bg-amber-500',
                        state === 'ended' && 'bg-muted-foreground',
                        state === 'failed' && 'bg-destructive',
                    )}
                />
            </span>
            {labels[state]}
        </span>
    );
}
