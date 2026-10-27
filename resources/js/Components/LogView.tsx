import { useVirtualizer } from '@tanstack/react-virtual';
import { ArrowDownIcon } from 'lucide-react';
import { type ReactNode, useLayoutEffect, useRef, useState } from 'react';

import { Button } from '@/Components/ui/button';
import { cn } from '@/lib/utils';

export interface LogEntry {
    key: number;
    text: string;
    stream?: 'stdout' | 'stderr';
    time?: string; // ISO 8601, with a zone
}

// Every row is one unwrapped line, so rows have a fixed height and only the
// visible ones are rendered. Long lines scroll sideways.
const rowHeight = 20;

const timeFormat = new Intl.DateTimeFormat(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    fractionalSecondDigits: 3,
    hour12: false,
});

// LogView shows log lines, following the newest while the reader is at the
// bottom. Scrolling up stops following until they jump back down. With
// fitTo, it grows with its lines up to that many pixels instead of taking
// its height from className.
export function LogView({
    lines,
    empty,
    className,
    fitTo,
}: {
    lines: LogEntry[];
    empty: ReactNode;
    className?: string;
    fitTo?: number;
}) {
    const scroller = useRef<HTMLDivElement>(null);
    const [following, setFollowing] = useState(true);
    const virtualizer = useVirtualizer({
        count: lines.length,
        getScrollElement: () => scroller.current,
        estimateSize: () => rowHeight,
        overscan: 30,
    });

    // biome-ignore lint/correctness/useExhaustiveDependencies: new lines are what scroll a following view to the bottom
    useLayoutEffect(() => {
        const el = scroller.current;
        if (following && el) {
            el.scrollTop = el.scrollHeight;
        }
    }, [lines, following]);

    const onScroll = () => {
        const el = scroller.current;
        if (el) {
            setFollowing(el.scrollHeight - el.scrollTop - el.clientHeight < rowHeight * 2);
        }
    };

    return (
        <div
            className={cn('relative', className)}
            style={fitTo ? { height: Math.min(Math.max(lines.length, 3) * rowHeight + 34, fitTo) } : undefined}
        >
            <div
                ref={scroller}
                onScroll={onScroll}
                className="h-full overflow-auto rounded-md bg-muted py-2 font-mono text-xs"
                role="log"
                aria-live="off"
            >
                {lines.length === 0 ? (
                    <div className="px-3 leading-5 text-muted-foreground">{empty}</div>
                ) : (
                    <div className="relative w-max min-w-full" style={{ height: virtualizer.getTotalSize() }}>
                        {virtualizer.getVirtualItems().map((row) => {
                            const line = lines[row.index];
                            return (
                                <div
                                    key={line.key}
                                    className={cn(
                                        'absolute left-0 flex w-full px-3 leading-5 whitespace-pre hover:bg-foreground/5',
                                        line.stream === 'stderr' && 'text-destructive',
                                    )}
                                    style={{ height: rowHeight, transform: `translateY(${row.start}px)` }}
                                    data-stream={line.stream}
                                >
                                    {line.time && (
                                        <span
                                            className="mr-3 shrink-0 text-muted-foreground select-none"
                                            title={line.time}
                                        >
                                            {timeFormat.format(new Date(line.time))}
                                        </span>
                                    )}
                                    <span>{line.text || ' '}</span>
                                </div>
                            );
                        })}
                    </div>
                )}
            </div>
            {!following && lines.length > 0 && (
                <Button
                    size="sm"
                    variant="secondary"
                    className="absolute right-4 bottom-3 shadow-md"
                    onClick={() => setFollowing(true)}
                >
                    <ArrowDownIcon />
                    Latest
                </Button>
            )}
        </div>
    );
}
