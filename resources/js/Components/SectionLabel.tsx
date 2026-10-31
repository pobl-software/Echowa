import type { ReactNode } from 'react';

import { cn } from '@/lib/utils';

// SectionLabel names a section or a column in small mono capitals, as the
// dashboard's do.
export function SectionLabel({ className, children }: { className?: string; children: ReactNode }) {
    return (
        <span className={cn('font-mono text-[11px] text-muted-foreground uppercase tracking-wider', className)}>
            {children}
        </span>
    );
}
