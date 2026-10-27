import { router } from '@inertiajs/react';

import { toast } from '@/Components/ui/toast';
import type { FlashData } from '@/types';

const types = ['success', 'error', 'info', 'warning'] as const;

// Shows flash data from the server (inertia.Flash in Go) as toasts. Inertia
// fires "flash" on the first page load and after any visit that carries flash.
export function showFlashAsToasts() {
    router.on('flash', (event) => {
        const flash = event.detail.flash as FlashData;
        for (const type of types) {
            const message = flash[type];
            if (message) {
                toast.add({ type, title: message });
            }
        }
    });
}
