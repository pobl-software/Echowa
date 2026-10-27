import '../css/app.css';

import { createInertiaApp, type ResolvedComponent } from '@inertiajs/react';

import { Toaster } from '@/Components/ui/toast';
import { TooltipProvider } from '@/Components/ui/tooltip';
import AppLayout from '@/Layouts/AppLayout';
import { showFlashAsToasts } from '@/lib/flash';

showFlashAsToasts();

// No setup() needed: Inertia v3 creates the React root itself.
createInertiaApp({
    title: (title) => (title ? `${title} - Echowa` : 'Echowa'),
    resolve: (name) => {
        const pages = import.meta.glob<{ default: ResolvedComponent }>('./Pages/**/*.tsx', { eager: true });
        return pages[`./Pages/${name}.tsx`];
    },
    // Persistent default layout: it stays mounted across visits, so its state
    // (menus, scroll position, etc.) survives navigation. A page can override it
    // with `Page.layout = OtherLayout`, or opt out with `Page.layout = []`.
    layout: () => AppLayout,
    // App-wide providers. The Toaster sits above every layout, so pages with
    // their own layout (e.g. auth pages) can show toasts too.
    withApp: (app) => (
        <Toaster>
            <TooltipProvider delay={300}>{app}</TooltipProvider>
        </Toaster>
    ),
    progress: {
        color: '#d14400',
    },
});
