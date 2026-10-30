import { type ComponentProps, lazy, Suspense } from 'react';

import type { AppTerminal as Terminal } from '@/Components/AppTerminal';

const Loaded = lazy(() => import('@/Components/AppTerminal').then((module) => ({ default: module.AppTerminal })));

// AppTerminal in its own chunk: xterm.js is most of its weight, and only
// the Terminal section should load it. Import this.
export function AppTerminal(props: ComponentProps<typeof Terminal>) {
    return (
        <Suspense
            fallback={
                <div className="mt-11 rounded-md bg-muted p-2">
                    <div className="h-[max(16rem,calc(100dvh-17.5rem))]" />
                </div>
            }
        >
            <Loaded {...props} />
        </Suspense>
    );
}
