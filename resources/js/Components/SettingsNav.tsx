import { Head, Link } from '@inertiajs/react';
import {
    BellIcon,
    DatabaseBackupIcon,
    DownloadIcon,
    KeyRoundIcon,
    LayoutGridIcon,
    type LucideIcon,
    SendIcon,
    SettingsIcon,
    Trash2Icon,
} from 'lucide-react';
import { type ReactNode, useEffect, useRef } from 'react';

import { Page } from '@/Components/Page';
import { cn } from '@/lib/utils';

// Settings' sections, in the order the nav and the overview list them.
export const settingsSections = [
    { group: null, id: 'overview', label: 'Overview', href: '/settings', icon: LayoutGridIcon, description: '' },
    // The overview leads with the version instead of a card for it.
    {
        group: null,
        id: 'updates',
        label: 'Updates',
        href: '/settings/updates',
        icon: DownloadIcon,
        description: 'New releases of Echowa, and installing them.',
    },
    {
        group: 'Apps',
        id: 'registries',
        label: 'Registries',
        href: '/settings/registries',
        icon: KeyRoundIcon,
        description: 'Logins for pulling private images. Public images don’t need one.',
    },
    {
        group: 'Apps',
        id: 'storage',
        label: 'S3 storage',
        href: '/settings/storage',
        icon: DatabaseBackupIcon,
        description: 'Buckets that database backups go to.',
    },
    {
        group: 'Apps',
        id: 'pruning',
        label: 'Image pruning',
        href: '/settings/pruning',
        icon: Trash2Icon,
        description: 'Old images Echowa clears out every night, keeping enough to roll back.',
    },
    {
        group: 'Monitoring',
        id: 'notifications',
        label: 'Notifications',
        href: '/settings/notifications',
        icon: SendIcon,
        description: 'Where alerts go: Slack, Discord, Teams, ntfy or email.',
    },
    {
        group: 'Monitoring',
        id: 'alerts',
        label: 'Alerts',
        href: '/settings/alerts',
        icon: BellIcon,
        description: 'Alerts about the server, and ones that cover every app.',
    },
] as const satisfies readonly {
    group: string | null;
    id: string;
    label: string;
    href: string;
    icon: LucideIcon;
    description: string;
}[];
export type SettingsSection = (typeof settingsSections)[number]['id'];

export const settingsGroups = ['Apps', 'Monitoring'] as const;

export function settingsSection(id: SettingsSection) {
    return settingsSections.find((s) => s.id === id) ?? settingsSections[0];
}

// SettingsPage is a Settings page: its title, and the sections as a nav
// beside it on wide screens and as tabs on phones. crumbs follow Settings,
// for a page inside a section (a registry's form).
export function SettingsPage({
    section,
    crumbs,
    actions,
    description,
    children,
}: {
    section: SettingsSection;
    crumbs?: { label: string; href?: string }[];
    actions?: ReactNode;
    description?: ReactNode;
    children: ReactNode;
}) {
    const current = settingsSection(section);
    const trail =
        section === 'overview'
            ? [{ label: 'Settings' }]
            : [
                  { label: 'Settings', href: '/settings' },
                  ...(crumbs ? [{ label: current.label, href: current.href }, ...crumbs] : [{ label: current.label }]),
              ];
    const title = trail[trail.length - 1].label;

    return (
        <>
            <Head title={section === 'overview' ? 'Settings' : title} />
            <Page
                crumbs={trail}
                actions={actions}
                description={description}
                aside={<SettingsNavAside current={section} />}
                tabs={<SettingsNavTabs current={section} />}
            >
                {children}
            </Page>
        </>
    );
}

function SettingsNavAside({ current }: { current: SettingsSection }) {
    return (
        <>
            <div className="flex h-14 shrink-0 items-center gap-2.5 border-b px-4">
                <span className="flex size-7 shrink-0 items-center justify-center rounded-md border bg-muted/50">
                    <SettingsIcon className="size-3.5 text-muted-foreground" />
                </span>
                <p className="truncate text-sm font-medium">Settings</p>
            </div>
            <nav aria-label="Settings sections" className="flex-1 space-y-5 overflow-y-auto p-3">
                {[null, ...settingsGroups].map((group) => (
                    <div key={group ?? 'top'}>
                        {group && <p className="px-2.5 pb-1.5 text-xs font-medium text-muted-foreground">{group}</p>}
                        <div className="space-y-0.5">
                            {settingsSections
                                .filter((s) => s.group === group)
                                .map((s) => {
                                    const active = s.id === current;
                                    return (
                                        <Link
                                            key={s.id}
                                            href={s.href}
                                            aria-current={active ? 'page' : undefined}
                                            className={cn(
                                                'flex h-8 items-center gap-2.5 rounded-lg px-2.5 text-sm transition-colors',
                                                active
                                                    ? 'bg-accent font-medium text-foreground'
                                                    : 'text-muted-foreground hover:bg-accent/60 hover:text-foreground',
                                            )}
                                        >
                                            <s.icon className={cn('size-4', active && 'text-primary')} />
                                            {s.label}
                                        </Link>
                                    );
                                })}
                        </div>
                    </div>
                ))}
            </nav>
        </>
    );
}

function SettingsNavTabs({ current }: { current: SettingsSection }) {
    const nav = useRef<HTMLElement>(null);

    // Keep the current tab in view; the row scrolls sideways.
    // biome-ignore lint/correctness/useExhaustiveDependencies: current is what moves the active tab; the effect reads it from the DOM
    useEffect(() => {
        const active = nav.current?.querySelector<HTMLElement>('[aria-current="page"]');
        if (nav.current && active) {
            nav.current.scrollLeft = active.offsetLeft - (nav.current.clientWidth - active.offsetWidth) / 2;
        }
    }, [current]);

    return (
        <nav
            ref={nav}
            aria-label="Settings sections"
            className="flex overflow-x-auto px-2 [scrollbar-width:none] sm:px-3 [&::-webkit-scrollbar]:hidden"
        >
            {settingsSections.map((s) => {
                const active = s.id === current;
                return (
                    <Link
                        key={s.id}
                        href={s.href}
                        aria-current={active ? 'page' : undefined}
                        className={cn(
                            '-mb-px flex h-11 shrink-0 items-center gap-2 border-b-2 px-3 text-sm whitespace-nowrap transition-colors',
                            active
                                ? 'border-primary font-medium text-foreground'
                                : 'border-transparent text-muted-foreground hover:text-foreground',
                        )}
                    >
                        <s.icon className="size-4" />
                        {s.label}
                    </Link>
                );
            })}
        </nav>
    );
}
