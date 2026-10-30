import '@xterm/xterm/css/xterm.css';

import { router } from '@inertiajs/react';
import { FitAddon } from '@xterm/addon-fit';
import { type ITheme, Terminal } from '@xterm/xterm';
import { EraserIcon, RotateCwIcon } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';

import { StreamIndicator } from '@/Components/StreamIndicator';
import { Button } from '@/Components/ui/button';
import type { StreamState } from '@/lib/eventStream';

// What Echowa sends as JSON in a text frame (TerminalController). The
// shell's bytes come in binary frames.
type ServerMessage =
    | { type: 'ready'; container: string }
    | { type: 'exit'; code: number }
    | { type: 'closed'; reason: string };

// How the shell ended is in the terminal, as a note.
type Status =
    | { state: 'connecting' }
    | { state: 'connected'; container: string }
    | { state: 'exited' | 'closed' | 'disconnected' };

const indicators = {
    connecting: ['connecting', 'Connecting…'],
    connected: ['live', 'Connected'],
    exited: ['ended', 'Exited'],
    closed: ['ended', 'Closed'],
    disconnected: ['failed', 'Disconnected'],
} as const satisfies Record<Status['state'], readonly [StreamState, string]>;

const fontSize = 13;

// Programs choose ANSI colours by number, and the theme has no tokens for
// them, so each theme has its own sixteen, readable on --muted.
const ansi = {
    light: {
        black: '#24292f',
        red: '#cf222e',
        green: '#116329',
        yellow: '#7d4e00',
        blue: '#0969da',
        magenta: '#8250df',
        cyan: '#1b7c83',
        white: '#6e7781',
        brightBlack: '#57606a',
        brightRed: '#a40e26',
        brightGreen: '#1a7f37',
        brightYellow: '#633c01',
        brightBlue: '#218bff',
        brightMagenta: '#a475f9',
        brightCyan: '#3192aa',
        brightWhite: '#8c959f',
    },
    dark: {
        black: '#484f58',
        red: '#ff7b72',
        green: '#3fb950',
        yellow: '#d29922',
        blue: '#58a6ff',
        magenta: '#bc8cff',
        cyan: '#39c5cf',
        white: '#b1bac4',
        brightBlack: '#6e7681',
        brightRed: '#ffa198',
        brightGreen: '#56d364',
        brightYellow: '#e3b341',
        brightBlue: '#79c0ff',
        brightMagenta: '#d2a8ff',
        brightCyan: '#56d4dd',
        brightWhite: '#f0f6fc',
    },
} satisfies Record<'light' | 'dark', ITheme>;

// hex is a CSS colour as #rrggbb. xterm reads oklch() itself, but only
// opaque, so the selection's alpha goes on the end of this.
function hex(color: string) {
    const context = document.createElement('canvas').getContext('2d');
    if (!context) {
        return '#808080';
    }
    context.fillStyle = color;
    context.fillRect(0, 0, 1, 1);
    const [r, g, b] = context.getImageData(0, 0, 1, 1).data;
    return `#${[r, g, b].map((c) => c.toString(16).padStart(2, '0')).join('')}`;
}

function pageTheme(): ITheme {
    const root = document.documentElement;
    const style = getComputedStyle(root);
    const token = (name: string) => style.getPropertyValue(`--${name}`).trim();
    return {
        background: token('muted'),
        foreground: token('foreground'),
        cursor: token('primary'),
        cursorAccent: token('primary-foreground'),
        selectionBackground: `${hex(token('primary'))}59`,
        ...ansi[root.classList.contains('dark') ? 'dark' : 'light'],
    };
}

// exitNote says how the shell ended. Above 128 is 128 plus the signal that
// killed it, which is what Stop and a deploy's new container do.
function exitNote(code: number) {
    return code > 128
        ? `The shell was killed (exit code ${code}), as it is when the app stops or a deploy replaces its container.`
        : `The shell exited with code ${code}.`;
}

// AppTerminal is a shell in the app's newest running task (ADR 0038),
// opened when it mounts and ended when it unmounts. active says its
// section is showing, so it takes the keyboard.
export function AppTerminal({ appId, active }: { appId: number; active: boolean }) {
    const host = useRef<HTMLDivElement>(null);
    const [term, setTerm] = useState<Terminal | null>(null);
    const [status, setStatus] = useState<Status>({ state: 'connecting' });
    const [attempt, setAttempt] = useState(0);

    // The screen, fitted to its box and following the theme.
    useEffect(() => {
        const box = host.current;
        if (!box) {
            return;
        }
        const fontFamily = getComputedStyle(document.documentElement).getPropertyValue('--font-mono').trim();
        const screen = new Terminal({
            fontFamily: fontFamily || 'monospace',
            fontSize,
            cursorBlink: true,
            scrollback: 5000,
            // A program that assumes a dark background stays readable on a light one.
            minimumContrastRatio: 4.5,
            theme: pageTheme(),
        });
        const fit = new FitAddon();
        screen.loadAddon(fit);
        let frame = 0;
        const resizes = new ResizeObserver(() => {
            cancelAnimationFrame(frame);
            frame = requestAnimationFrame(() => fit.fit());
        });
        const themes = new MutationObserver(() => {
            screen.options.theme = pageTheme();
        });
        let gone = false;
        // Cells measured in a fallback font would be the wrong width.
        document.fonts
            .load(`${fontSize}px ${fontFamily}`)
            .catch(() => undefined)
            .then(() => {
                if (gone) {
                    return;
                }
                screen.open(box);
                fit.fit();
                resizes.observe(box);
                themes.observe(document.documentElement, { attributeFilter: ['class'] });
                setTerm(screen);
            });
        return () => {
            gone = true;
            cancelAnimationFrame(frame);
            resizes.disconnect();
            themes.disconnect();
            screen.dispose();
        };
    }, []);

    // One shell per attempt; Reconnect starts the next.
    // biome-ignore lint/correctness/useExhaustiveDependencies: attempt is what Reconnect changes to open a new shell
    useEffect(() => {
        if (!term) {
            return;
        }
        setStatus({ state: 'connecting' });
        const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws';
        const socket = new WebSocket(
            `${scheme}://${window.location.host}/apps/${appId}/terminal?cols=${term.cols}&rows=${term.rows}`,
        );
        socket.binaryType = 'arraybuffer';

        let ready = false;
        let ended = false;
        const end = (state: 'exited' | 'closed' | 'disconnected', note: string) => {
            if (ended) {
                return;
            }
            ended = true;
            setStatus({ state });
            // Once the output before it is on the screen.
            term.write('', () => {
                // A full-screen program that was running would leave its
                // screen and modes behind. Leaving the alternate screen
                // also puts the cursor back where the program started.
                const buffer = term.buffer.active;
                const leave = buffer.type === 'alternate' ? '\x1b[?1049l' : '';
                const newline = leave || buffer.cursorX > 0 ? '\r\n' : '';
                term.write(`${leave}\x1b[!p${newline}\x1b[2m${note}\x1b[0m\r\n\x1b[?25l`);
            });
        };
        socket.onmessage = ({ data }: MessageEvent<ArrayBuffer | string>) => {
            if (typeof data !== 'string') {
                term.write(new Uint8Array(data));
                return;
            }
            const message = JSON.parse(data) as ServerMessage;
            if (message.type === 'ready') {
                ready = true;
                term.write('\x1b[?25h');
                setStatus({ state: 'connected', container: message.container });
            } else if (message.type === 'exit') {
                end('exited', exitNote(message.code));
            } else {
                end('closed', message.reason);
            }
        };
        socket.onclose = () =>
            end('disconnected', ready ? 'Lost the connection to Echowa.' : "Couldn't connect to Echowa.");

        const send = (data: string | Uint8Array<ArrayBuffer>) => {
            if (socket.readyState === WebSocket.OPEN) {
                socket.send(data);
            }
        };
        const encoder = new TextEncoder();
        const typed = term.onData((data) => send(encoder.encode(data)));
        // Some mouse reports are bytes that aren't UTF-8.
        const reports = term.onBinary((data) => send(Uint8Array.from(data, (c) => c.charCodeAt(0))));
        let resizing = 0;
        const resized = term.onResize(({ cols, rows }) => {
            window.clearTimeout(resizing);
            resizing = window.setTimeout(() => send(JSON.stringify({ type: 'resize', cols, rows })), 100);
        });

        return () => {
            ended = true;
            typed.dispose();
            reports.dispose();
            resized.dispose();
            window.clearTimeout(resizing);
            socket.close();
        };
    }, [term, appId, attempt]);

    useEffect(() => {
        if (active && status.state === 'connected') {
            term?.focus();
        }
    }, [active, status.state, term]);

    // Leaving the app ends the shell, so ask first while it's open.
    const connected = status.state === 'connected';
    useEffect(() => {
        if (!connected) {
            return;
        }
        const leaving = router.on('before', ({ detail: { visit } }) => {
            // Saves, polls and partial reloads keep this page, and the shell.
            if (
                visit.method !== 'get' ||
                visit.prefetch ||
                visit.preserveState === true ||
                visit.only.length > 0 ||
                visit.except.length > 0
            ) {
                return;
            }
            return window.confirm('Leave and close the terminal? Anything still running in it stops.');
        });
        const unloading = (event: BeforeUnloadEvent) => event.preventDefault();
        window.addEventListener('beforeunload', unloading);
        return () => {
            leaving();
            window.removeEventListener('beforeunload', unloading);
        };
    }, [connected]);

    const [indicator, label] = indicators[status.state];
    const over = status.state !== 'connecting' && status.state !== 'connected';

    return (
        <div className="space-y-3">
            <div className="flex flex-wrap items-center gap-2">
                <StreamIndicator state={indicator} label={label} />
                {status.state === 'connected' && (
                    <span className="font-mono text-xs text-muted-foreground" title="The container the shell runs in">
                        {status.container}
                    </span>
                )}
                <div className="ml-auto flex items-center gap-2">
                    <Button
                        variant="outline"
                        size="sm"
                        disabled={!term}
                        aria-label="Clear"
                        onClick={() => {
                            term?.clear();
                            term?.focus();
                        }}
                    >
                        <EraserIcon />
                        <span className="hidden sm:inline">Clear</span>
                    </Button>
                    {over && (
                        <Button variant="outline" size="sm" onClick={() => setAttempt((n) => n + 1)}>
                            <RotateCwIcon />
                            Reconnect
                        </Button>
                    )}
                </div>
            </div>
            {/* Fills the screen below the page's header and the panel's. LazyAppTerminal's placeholder matches. */}
            <div className="rounded-md bg-muted p-2">
                <div ref={host} className="h-[max(16rem,calc(100dvh-17.5rem))]" />
            </div>
        </div>
    );
}
