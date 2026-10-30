import { Link } from '@inertiajs/react';
import {
    ArchiveIcon,
    BellIcon,
    BoxesIcon,
    BracesIcon,
    CalendarClockIcon,
    ChartLineIcon,
    ContainerIcon,
    DatabaseIcon,
    GaugeIcon,
    GlobeIcon,
    HardDriveIcon,
    HistoryIcon,
    LayoutDashboardIcon,
    type LucideIcon,
    PlugIcon,
    ScrollTextIcon,
    SquareTerminalIcon,
    TriangleAlertIcon,
} from 'lucide-react';
import { type MouseEvent, type ReactNode, useEffect, useRef } from 'react';

import { templateIcon } from '@/lib/templates';
import { cn } from '@/lib/utils';

export const appSections = [
    { group: 'Configuration', id: 'overview', label: 'Overview', icon: LayoutDashboardIcon },
    { group: 'Configuration', id: 'environment', label: 'Environment', icon: BracesIcon },
    // The databases a custom app is linked to (ADR 0028).
    { group: 'Configuration', id: 'databases', label: 'Databases', icon: DatabaseIcon, kind: 'custom' },
    // The custom apps that support it, such as a queue worker (ADR 0037).
    { group: 'Configuration', id: 'services', label: 'Services', icon: BoxesIcon, kind: 'custom' },
    // How other apps, and clients outside the server, reach a template database.
    { group: 'Configuration', id: 'connection', label: 'Connection', icon: PlugIcon, kind: 'template' },
    // Dumps of a template database to S3 storage (ADR 0034). Not Redis.
    { group: 'Configuration', id: 'backups', label: 'Backups', icon: ArchiveIcon, kind: 'backups' },
    { group: 'Configuration', id: 'docker', label: 'Docker config', icon: ContainerIcon },
    // The paths whose files survive a deploy (ADR 0039). A template's are
    // fixed, and listed in its Docker config.
    { group: 'Configuration', id: 'volumes', label: 'Volumes', icon: HardDriveIcon, kind: 'custom' },
    { group: 'Configuration', id: 'resources', label: 'Resource limits', icon: GaugeIcon },
    // Template apps don't speak HTTP, so they have no domain.
    { group: 'Configuration', id: 'domains', label: 'Domains', icon: GlobeIcon, kind: 'custom' },
    // Commands run in a custom app's container on a schedule (ADR 0032).
    { group: 'Configuration', id: 'tasks', label: 'Scheduled tasks', icon: CalendarClockIcon, kind: 'custom' },
    { group: 'Observe', id: 'deployments', label: 'Deployments', icon: HistoryIcon },
    { group: 'Observe', id: 'logs', label: 'Logs', icon: ScrollTextIcon },
    // A template app's includes what the database reports (ADR 0027).
    { group: 'Observe', id: 'metrics', label: 'Metrics', icon: ChartLineIcon },
    // Where its notifications go, and what was sent (ADR 0040).
    { group: 'Observe', id: 'alerts', label: 'Alerts', icon: BellIcon },
    // A shell in a custom app's container (ADR 0038).
    { group: 'Manage', id: 'terminal', label: 'Terminal', icon: SquareTerminalIcon, kind: 'custom' },
    { group: 'Manage', id: 'danger', label: 'Danger zone', icon: TriangleAlertIcon },
] as const satisfies readonly {
    group: string;
    id: string;
    label: string;
    icon: LucideIcon;
    kind?: 'template' | 'custom' | 'backups';
}[];
export type AppSection = (typeof appSections)[number]['id'];

// backups says the app's template is one Echowa backs up.
type SectionApp = { template?: string | null; backups?: boolean };

// The sections an app has.
export function sectionsFor(app: SectionApp) {
    const kind = app.template != null ? 'template' : 'custom';
    return appSections.filter(
        (s) => !('kind' in s) || s.kind === kind || (s.kind === 'backups' && app.backups === true),
    );
}

const groups = [...new Set(appSections.map((s) => s.group))];

interface Props {
    app: { id: number; name: string } & SectionApp;
    current: AppSection;
    // Switch sections in place instead of visiting the app page.
    onSelect?: (section: AppSection) => void;
}

function useLink({ app, onSelect }: Props) {
    return (id: AppSection) => ({
        href: `/apps/${app.id}?tab=${id}`,
        onClick: onSelect
            ? (event: MouseEvent) => {
                  if (!event.metaKey && !event.ctrlKey && !event.shiftKey) {
                      event.preventDefault();
                      onSelect(id);
                  }
              }
            : undefined,
    });
}

// AppNavAside is an app's section list beside the page on wide screens.
export function AppNavAside(props: Props & { subtitle?: ReactNode }) {
    const link = useLink(props);
    const Icon = templateIcon(props.app.template);

    return (
        <>
            <div className="flex h-14 shrink-0 items-center gap-2.5 border-b px-4">
                <span className="flex size-7 shrink-0 items-center justify-center rounded-md border bg-muted/50">
                    <Icon className="size-3.5 text-muted-foreground" />
                </span>
                <div className="min-w-0 leading-tight">
                    <p className="truncate text-sm font-medium">{props.app.name}</p>
                    {props.subtitle && (
                        <p className="truncate font-mono text-[11px] text-muted-foreground">{props.subtitle}</p>
                    )}
                </div>
            </div>
            <nav aria-label="App sections" className="flex-1 space-y-5 overflow-y-auto p-3">
                {groups.map((group) => (
                    <div key={group}>
                        <p className="px-2.5 pb-1.5 text-xs font-medium text-muted-foreground">{group}</p>
                        <div className="space-y-0.5">
                            {sectionsFor(props.app)
                                .filter((s) => s.group === group)
                                .map((s) => {
                                    const active = s.id === props.current;
                                    return (
                                        <Link
                                            key={s.id}
                                            {...link(s.id)}
                                            aria-current={active ? 'page' : undefined}
                                            className={cn(
                                                'flex h-8 items-center gap-2.5 rounded-lg px-2.5 text-sm transition-colors',
                                                active
                                                    ? 'bg-accent font-medium text-foreground'
                                                    : 'text-muted-foreground hover:bg-accent/60 hover:text-foreground',
                                                active && s.id === 'danger' && 'text-destructive',
                                            )}
                                        >
                                            <s.icon
                                                className={cn('size-4', active && s.id !== 'danger' && 'text-primary')}
                                            />
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

// AppNavTabs is the same list as a row of tabs on phones and tablets.
export function AppNavTabs(props: Props) {
    const link = useLink(props);
    const nav = useRef<HTMLElement>(null);

    // Keep the current tab in view; the row scrolls sideways.
    // biome-ignore lint/correctness/useExhaustiveDependencies: props.current is what moves the active tab; the effect reads it from the DOM
    useEffect(() => {
        const active = nav.current?.querySelector<HTMLElement>('[aria-current="page"]');
        if (nav.current && active) {
            nav.current.scrollLeft = active.offsetLeft - (nav.current.clientWidth - active.offsetWidth) / 2;
        }
    }, [props.current]);

    return (
        <nav
            ref={nav}
            aria-label="App sections"
            className="flex overflow-x-auto px-2 [scrollbar-width:none] sm:px-3 [&::-webkit-scrollbar]:hidden"
        >
            {sectionsFor(props.app).map((s) => {
                const active = s.id === props.current;
                return (
                    <Link
                        key={s.id}
                        {...link(s.id)}
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
