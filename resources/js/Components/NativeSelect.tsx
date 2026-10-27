import type { ComponentProps } from 'react';

import { cn } from '@/lib/utils';

// A native <select> styled like Input, for short option lists in forms.
export function NativeSelect({ className, ...props }: ComponentProps<'select'>) {
    return (
        <select
            className={cn(
                'h-8 w-full min-w-0 rounded-lg border border-input bg-transparent px-2 py-1 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 md:text-sm dark:bg-input/30',
                className,
            )}
            {...props}
        />
    );
}
