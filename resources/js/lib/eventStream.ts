import { useEffect, useRef, useState } from 'react';

// connecting: opening. live: open. reconnecting: dropped, the browser is
// retrying on its own. ended: the server said it's over, or the page closed
// it. failed: refused (signed out, not found, bad query); no more retries.
export type StreamState = 'connecting' | 'live' | 'reconnecting' | 'ended' | 'failed';

// The "end" event the server sends when a stream is over (services.StreamEndEvent).
export interface StreamEnd {
    reason: 'not_deployed' | 'not_found' | 'ended' | 'stopped' | 'error';
    message: string;
}

type Listeners = Record<string, (data: unknown) => void>;

// useEventStream follows a Server-Sent Events URL while url isn't null.
// Changing url (or calling restart) opens a new stream. Event data is
// parsed as JSON. An "end" event closes the stream for good. Call close
// when the page has what it needs.
export function useEventStream(url: string | null, listeners: Listeners) {
    const [state, setState] = useState<StreamState>('connecting');
    const [end, setEnd] = useState<StreamEnd | null>(null);
    const [attempt, setAttempt] = useState(0);
    const latest = useRef(listeners);
    latest.current = listeners;
    const source = useRef<EventSource | null>(null);

    // biome-ignore lint/correctness/useExhaustiveDependencies: restart bumps attempt to open a new stream
    useEffect(() => {
        if (!url) {
            return;
        }
        setState('connecting');
        setEnd(null);
        const es = new EventSource(url);
        source.current = es;

        es.onopen = () => setState('live');
        es.onerror = () => setState(es.readyState === EventSource.CLOSED ? 'failed' : 'reconnecting');
        const names = new Set([...Object.keys(latest.current), 'end']);
        for (const name of names) {
            es.addEventListener(name, (event) => {
                const data: unknown = JSON.parse((event as MessageEvent<string>).data);
                latest.current[name]?.(data);
                if (name === 'end') {
                    es.close();
                    setEnd(data as StreamEnd);
                    setState('ended');
                }
            });
        }

        return () => {
            es.close();
            source.current = null;
        };
    }, [url, attempt]);

    return {
        state,
        end,
        close: () => {
            source.current?.close();
            setState('ended');
        },
        restart: () => setAttempt((n) => n + 1),
    };
}
