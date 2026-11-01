import { Head, Link } from '@inertiajs/react';
import { ArrowLeftIcon, BoxesIcon } from 'lucide-react';
import { useEffect, useState } from 'react';

import { Logo } from '@/Components/Logo';
import { Button, buttonVariants } from '@/Components/ui/button';

// NotFound is every 404 (inertia.NotFound): an unknown URL, or a record that's
// gone, such as a deleted app. It has no layout, and needs no shared props,
// since the router's fallback renders it without a session.
export default function NotFound({ path }: { path: string }) {
    // Go back only when there's somewhere in this tab to go back to.
    const [canGoBack, setCanGoBack] = useState(false);
    useEffect(() => setCanGoBack(window.history.length > 1), []);

    return (
        <>
            <Head title="Page not found" />
            <div className="relative isolate flex min-h-dvh flex-col overflow-hidden bg-sidebar text-foreground">
                {/* A dot grid that fades out from the middle, and the accent's glow behind the 404. */}
                <div
                    aria-hidden="true"
                    className="absolute inset-0 -z-10 bg-[radial-gradient(var(--color-border)_1px,transparent_1px)] [background-size:24px_24px] [mask-image:radial-gradient(ellipse_at_center,black_20%,transparent_70%)]"
                />
                <div
                    aria-hidden="true"
                    className="absolute top-1/2 left-1/2 -z-10 size-[28rem] -translate-x-1/2 -translate-y-2/3 rounded-full bg-primary/10 blur-3xl"
                />

                <header className="flex h-16 items-center px-5 sm:px-8">
                    <Link href="/" className="rounded-md outline-offset-4" aria-label="Echowa home">
                        <Logo className="h-6" />
                    </Link>
                </header>

                <main className="flex flex-1 flex-col items-center justify-center px-4 pb-16 text-center">
                    <p className="font-display text-[7rem] leading-none font-semibold tracking-tighter text-primary select-none sm:text-[10rem]">
                        404
                    </p>
                    <h1 className="mt-4 font-display text-2xl tracking-tight sm:text-3xl">Page not found</h1>
                    <p className="mt-3 max-w-md text-sm text-muted-foreground sm:text-base">
                        There’s nothing at{' '}
                        <code className="rounded-md border bg-card px-1.5 py-0.5 font-mono text-[0.85em] break-all text-foreground">
                            {path}
                        </code>
                        . It may have been deleted, or the link may be wrong.
                    </p>
                    <div className="mt-8 flex flex-wrap justify-center gap-2">
                        <Link href="/dashboard" className={buttonVariants({ size: 'lg' })}>
                            <BoxesIcon />
                            Back to apps
                        </Link>
                        {canGoBack && (
                            <Button variant="outline" size="lg" onClick={() => window.history.back()}>
                                <ArrowLeftIcon />
                                Go back
                            </Button>
                        )}
                    </div>
                </main>
            </div>
        </>
    );
}

NotFound.layout = [] as never[];
