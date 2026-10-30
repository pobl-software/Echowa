import { router } from '@inertiajs/react';
import { PlayIcon, SquareIcon } from 'lucide-react';
import { useState } from 'react';

import { ConfirmDelete } from '@/Components/ConfirmDelete';
import { Button } from '@/Components/ui/button';
import type { App, LiveStatus } from '@/types/models';

// Stop and Start, for the app's header. Stop scales the service to nothing
// until Start deploys the image it ran. Neither shows for an app that isn't
// deployed, and both wait while a deployment runs or something holds the app.
export function StopStartButton({ app, status, disabled }: { app: App; status: LiveStatus; disabled: boolean }) {
    const [sending, setSending] = useState(false);
    const { state } = status;

    if (state === 'running' || state === 'degraded' || state === 'down') {
        return (
            <ConfirmDelete
                title={`Stop ${app.name}?`}
                confirmLabel="Stop"
                variant="default"
                trigger={
                    <Button variant="outline" disabled={disabled || sending} aria-label={`Stop ${app.name}`}>
                        <SquareIcon />
                        <span className="hidden sm:inline">Stop</span>
                    </Button>
                }
                onConfirm={() =>
                    router.post(
                        `/apps/${app.id}/stop`,
                        {},
                        { preserveScroll: true, onStart: () => setSending(true), onFinish: () => setSending(false) },
                    )
                }
            >
                Swarm stops its containers and removes them, with their logs, so read Logs first if you need them.
                {app.domain ? ` ${app.domain} answers 502 until you start it.` : ''} Scheduled tasks and backups are
                skipped meanwhile.
            </ConfirmDelete>
        );
    }

    if (state === 'stopped' || state === 'stopping') {
        return (
            <Button
                variant="outline"
                disabled={disabled || sending || state === 'stopping'}
                aria-label={`Start ${app.name}`}
                title={state === 'stopping' ? `${app.name} is still stopping.` : undefined}
                onClick={() =>
                    router.post(
                        `/apps/${app.id}/start`,
                        {},
                        { preserveScroll: true, onStart: () => setSending(true), onFinish: () => setSending(false) },
                    )
                }
            >
                <PlayIcon />
                <span className="hidden sm:inline">Start</span>
            </Button>
        );
    }

    return null;
}
