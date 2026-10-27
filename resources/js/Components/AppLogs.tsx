import { router } from '@inertiajs/react';
import { EraserIcon, PauseIcon, PlayIcon, RotateCwIcon } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';

import { type LogEntry, LogView } from '@/Components/LogView';
import { NativeSelect } from '@/Components/NativeSelect';
import { StreamIndicator } from '@/Components/StreamIndicator';
import { Button } from '@/Components/ui/button';
import { useEventStream } from '@/lib/eventStream';
import { timeAgo } from '@/lib/time';
import type { LogDeploy } from '@/types/models';

// services.LogLineEvent
interface LogLineEvent {
    time: string;
    stream: 'stdout' | 'stderr';
    text: string;
}

const tailSizes = [100, 200, 500, 1000, 5000];
// The viewer keeps at most this many lines, dropping the oldest.
const maxLines = 10000;

let nextKey = 0;

function keepLast(lines: LogEntry[]): LogEntry[] {
    return lines.length > maxLines ? lines.slice(lines.length - maxLines) : lines;
}

// "Current · #14 v1.2.0 · 2 hours ago". A rollback's ref is a digest.
function deployLabel(deploy: LogDeploy): string {
    const ref = deploy.deployment?.ref.replace(/^(sha256:.{12}).*$/, '$1');
    return [
        deploy.current && 'Current',
        deploy.deployment ? `#${deploy.deployment.id} ${ref}` : !deploy.current && 'Earlier deploy',
        timeAgo(deploy.deployment?.started_at ?? deploy.started_at),
    ]
        .filter(Boolean)
        .join(' · ');
}

// AppLogs follows the app's logs, stdout and stderr, for the Logs tab: the
// current deploy's by default, or a past one's from deploys.
export function AppLogs({ appId, deploys }: { appId: number; deploys?: LogDeploy[] }) {
    // A past deploy's generation, or null for the current one, which stays
    // live across a new deploy.
    const [deploy, setDeploy] = useState<number | null>(null);
    const [tail, setTail] = useState(200);
    const [lines, setLines] = useState<LogEntry[]>([]);
    const [paused, setPaused] = useState(false);
    const [held, setHeld] = useState(0);

    // Lines arrive in bursts. Collect them and render once per frame.
    const incoming = useRef<LogEntry[]>([]);
    const frame = useRef(0);
    const pausedRef = useRef(paused);
    pausedRef.current = paused;

    const flush = () => {
        frame.current = 0;
        if (pausedRef.current) {
            setHeld(incoming.current.length);
            return;
        }
        const batch = incoming.current;
        incoming.current = [];
        setHeld(0);
        if (batch.length > 0) {
            // Docker sends each task's lines in order but not interleaved by
            // time, so the first tail of a service with old tasks comes out
            // shuffled. ISO times sort as strings.
            batch.sort((a, b) => (a.time ?? '').localeCompare(b.time ?? ''));
            setLines((current) => keepLast(current.concat(batch)));
        }
    };
    const schedule = () => {
        if (!frame.current) {
            frame.current = requestAnimationFrame(flush);
        }
    };
    useEffect(() => () => cancelAnimationFrame(frame.current), []);

    const loadDeploys = () => router.reload({ only: ['logDeploys'] });
    // biome-ignore lint/correctness/useExhaustiveDependencies: once, on arrival; loadDeploys is rebuilt every render
    useEffect(() => {
        loadDeploys();
    }, []);

    const query = deploy === null ? `tail=${tail}` : `tail=${tail}&deploy=${deploy}`;
    const stream = useEventStream(`/apps/${appId}/logs/stream?${query}`, {
        line: (data) => {
            const line = data as LogLineEvent;
            incoming.current.push({ key: nextKey++, text: line.text, stream: line.stream, time: line.time });
            if (incoming.current.length > maxLines) {
                incoming.current.splice(0, incoming.current.length - maxLines);
            }
            schedule();
        },
    });

    // A new stream sends its tail again, so start from empty.
    const startOver = () => {
        incoming.current = [];
        setLines([]);
        setHeld(0);
    };
    const changeTail = (value: number) => {
        startOver();
        setTail(value);
    };
    const changeDeploy = (value: string) => {
        startOver();
        setDeploy(value === 'current' ? null : Number(value));
    };
    const reconnect = () => {
        startOver();
        loadDeploys();
        stream.restart();
    };

    const togglePause = () => {
        setPaused(!paused);
        pausedRef.current = !paused;
        schedule();
    };

    return (
        <div className="space-y-3">
            <div className="flex flex-wrap items-center gap-2">
                {deploys && deploys.length > 0 && (
                    <NativeSelect
                        aria-label="Deploy"
                        className="w-auto max-w-full"
                        value={deploy ?? 'current'}
                        onChange={(event) => changeDeploy(event.target.value)}
                    >
                        {deploys.map((d) => (
                            <option key={d.generation} value={d.current ? 'current' : d.generation}>
                                {deployLabel(d)}
                            </option>
                        ))}
                    </NativeSelect>
                )}
                <StreamIndicator state={stream.state} end={stream.end} />
                <span className="text-xs text-muted-foreground tabular-nums">
                    {lines.length.toLocaleString()} {lines.length === 1 ? 'line' : 'lines'}
                    {lines.length >= maxLines && ` (the last ${maxLines.toLocaleString()})`}
                    {paused && held > 0 && ` · ${held.toLocaleString()} new while paused`}
                </span>
                <div className="ml-auto flex flex-wrap items-center gap-2">
                    {/* Docker's tail counts per task, and a redeploy leaves the old task's logs too. */}
                    <label className="flex items-center gap-2 text-sm text-muted-foreground">
                        <span className="hidden sm:inline">Earlier lines per task</span>
                        <span className="sm:hidden">Per task</span>
                        <NativeSelect
                            className="w-auto"
                            value={tail}
                            onChange={(event) => changeTail(Number(event.target.value))}
                        >
                            {tailSizes.map((size) => (
                                <option key={size} value={size}>
                                    {size.toLocaleString()}
                                </option>
                            ))}
                        </NativeSelect>
                    </label>
                    <Button
                        variant="outline"
                        size="sm"
                        onClick={togglePause}
                        aria-pressed={paused}
                        aria-label={paused ? 'Resume' : 'Pause'}
                    >
                        {paused ? <PlayIcon /> : <PauseIcon />}
                        <span className="hidden sm:inline">{paused ? 'Resume' : 'Pause'}</span>
                    </Button>
                    <Button
                        variant="outline"
                        size="sm"
                        onClick={() => setLines([])}
                        disabled={lines.length === 0}
                        aria-label="Clear"
                    >
                        <EraserIcon />
                        <span className="hidden sm:inline">Clear</span>
                    </Button>
                    {/* A past deploy's stream ends once it has sent everything. */}
                    {(stream.state === 'ended' || stream.state === 'failed') && stream.end?.reason !== 'stopped' && (
                        <Button variant="outline" size="sm" onClick={reconnect}>
                            <RotateCwIcon />
                            Reconnect
                        </Button>
                    )}
                </div>
            </div>
            <LogView
                lines={lines}
                className="h-[32rem]"
                empty={
                    stream.end?.message ??
                    (stream.state === 'failed'
                        ? "Couldn't open the log stream. Reload the page if you've been signed out."
                        : 'Waiting for log lines…')
                }
            />
        </div>
    );
}
