import { cn } from '@/lib/utils';

// The mark: a signal and its fading echoes, on the accent colour.
export function LogoMark({ className }: { className?: string }) {
    return (
        <svg viewBox="0 0 32 32" aria-hidden="true" className={cn('size-7 shrink-0', className)}>
            <rect width="32" height="32" rx="8" className="fill-primary" />
            <rect x="8" y="9" width="3.5" height="14" rx="1.75" fill="white" />
            <rect x="14.25" y="11.5" width="3.5" height="9" rx="1.75" fill="white" opacity="0.75" />
            <rect x="20.5" y="13.5" width="3.5" height="5" rx="1.75" fill="white" opacity="0.5" />
        </svg>
    );
}

export function Logo({ name, className }: { name: string; className?: string }) {
    return (
        <span className={cn('flex items-center gap-2.5', className)}>
            <LogoMark />
            <span className="text-[15px] font-semibold tracking-tight text-foreground">{name}</span>
        </span>
    );
}
