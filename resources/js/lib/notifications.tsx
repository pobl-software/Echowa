import { HashIcon, MailIcon, UsersIcon } from 'lucide-react';
import { siDiscord, siNtfy, siResend } from 'simple-icons';

import type { AppIcon } from '@/lib/templates';
import { cn } from '@/lib/utils';
import type { NotificationKind } from '@/types/models';

// A brand's own mark from Simple Icons, in its colour (docs/design.md,
// the one place raw colours are allowed). Slack and Microsoft aren't in
// Simple Icons, so they get plain icons for now.
function brand(path: string, colour: string): AppIcon {
    return function BrandIcon({ className }: { className?: string }) {
        return (
            <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" className={cn('size-4', className, colour)}>
                <path d={path} />
            </svg>
        );
    };
}

function plain(Icon: AppIcon): AppIcon {
    return function PlainIcon({ className }: { className?: string }) {
        return <Icon className={cn('size-4', className, 'text-muted-foreground')} />;
    };
}

export const notificationKinds: Record<NotificationKind, { label: string; icon: AppIcon }> = {
    slack: { label: 'Slack', icon: plain(HashIcon) },
    discord: { label: 'Discord', icon: brand(siDiscord.path, 'text-[#5865F2]') },
    teams: { label: 'Microsoft Teams', icon: plain(UsersIcon) },
    ntfy: { label: 'ntfy', icon: brand(siNtfy.path, 'text-[#317F6F] dark:text-[#4FB39C]') },
    smtp: { label: 'Email (SMTP)', icon: plain(MailIcon) },
    resend: { label: 'Resend', icon: brand(siResend.path, 'text-foreground') },
};

export const kindOrder: NotificationKind[] = ['slack', 'discord', 'teams', 'ntfy', 'smtp', 'resend'];
