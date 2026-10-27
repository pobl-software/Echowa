import { Link, usePage } from '@inertiajs/react';
import type { PropsWithChildren } from 'react';

import { Logo } from '@/Components/Logo';

// Centred single-column layout for signed-out pages (login).
// Pages opt in with `Page.layout = GuestLayout`.
export default function GuestLayout({ children }: PropsWithChildren) {
    const { appName } = usePage().props;

    return (
        <div className="flex min-h-dvh flex-col items-center justify-center gap-8 bg-sidebar p-4">
            <Link href="/" className="rounded-md outline-offset-4">
                <Logo name={appName} className="[&_svg]:size-9 [&>span]:text-lg" />
            </Link>
            <div className="w-full max-w-sm">{children}</div>
        </div>
    );
}
