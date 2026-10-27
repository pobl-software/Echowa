import { Link, router, usePage } from '@inertiajs/react';
import {
    BoxesIcon,
    GaugeIcon,
    KeyRoundIcon,
    LogOutIcon,
    type LucideIcon,
    MonitorIcon,
    MoonIcon,
    SettingsIcon,
    SunIcon,
} from 'lucide-react';
import { type PropsWithChildren, useEffect, useState } from 'react';

import { Logo, LogoMark } from '@/Components/Logo';
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuGroup,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuRadioGroup,
    DropdownMenuRadioItem,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
} from '@/Components/ui/dropdown-menu';
import { Sheet, SheetContent, SheetTitle } from '@/Components/ui/sheet';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/Components/ui/tooltip';
import { ShellContext } from '@/lib/shell';
import { type Theme, useTheme } from '@/lib/theme';
import { cn } from '@/lib/utils';

interface NavItem {
    label: string;
    href: string;
    icon: LucideIcon;
    // URL prefixes that count as this section.
    match: string[];
}

const nav: NavItem[] = [
    { label: 'Apps', href: '/dashboard', icon: BoxesIcon, match: ['/dashboard', '/apps'] },
    { label: 'Server', href: '/server', icon: GaugeIcon, match: ['/server'] },
    { label: 'Registries', href: '/registries', icon: KeyRoundIcon, match: ['/registries'] },
    { label: 'Settings', href: '/settings', icon: SettingsIcon, match: ['/settings'] },
];

const themes: { value: Theme; label: string; icon: LucideIcon }[] = [
    { value: 'system', label: 'System', icon: MonitorIcon },
    { value: 'light', label: 'Light', icon: SunIcon },
    { value: 'dark', label: 'Dark', icon: MoonIcon },
];

// The signed-in shell. On wide screens: a thin icon rail, then the page in a
// bordered frame (Page draws its top bar and sub nav inside it). On phones:
// the page fills the screen, and its top bar's menu button opens the
// navigation as a drawer.
export default function AppLayout({ children }: PropsWithChildren) {
    const [open, setOpen] = useState(false);

    // The drawer closes once a link in it has navigated.
    useEffect(() => router.on('navigate', () => setOpen(false)), []);

    return (
        <ShellContext.Provider value={{ openMenu: () => setOpen(true) }}>
            <div className="flex h-dvh bg-sidebar text-foreground">
                <Rail />
                <Sheet open={open} onOpenChange={setOpen}>
                    <SheetContent
                        side="left"
                        className="w-72 max-w-[85vw] gap-0 border-sidebar-border bg-sidebar p-0"
                        showCloseButton={false}
                    >
                        <SheetTitle className="sr-only">Menu</SheetTitle>
                        <Drawer />
                    </SheetContent>
                </Sheet>
                <div className="flex min-w-0 flex-1 lg:py-2 lg:pr-2">
                    <div className="flex min-w-0 flex-1 overflow-hidden bg-background lg:rounded-xl lg:border">
                        {children}
                    </div>
                </div>
            </div>
        </ShellContext.Provider>
    );
}

function useActive() {
    const { url } = usePage();
    return (item: NavItem) => item.match.some((prefix) => url.startsWith(prefix));
}

function Rail() {
    const isActive = useActive();

    return (
        <aside className="hidden w-14 shrink-0 flex-col items-center gap-1 py-3 lg:flex">
            <Link href="/dashboard" className="mb-3 rounded-lg outline-offset-2" aria-label="Echowa home">
                <LogoMark className="size-8" />
            </Link>
            <nav aria-label="Main" className="flex flex-col items-center gap-1">
                {nav.map((item) => {
                    const active = isActive(item);
                    return (
                        <Tooltip key={item.href}>
                            <TooltipTrigger
                                render={
                                    <Link
                                        href={item.href}
                                        aria-label={item.label}
                                        aria-current={active ? 'page' : undefined}
                                        className={cn(
                                            'flex size-10 items-center justify-center rounded-lg transition-colors',
                                            active
                                                ? 'bg-sidebar-accent text-primary'
                                                : 'text-sidebar-foreground/70 hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground',
                                        )}
                                    />
                                }
                            >
                                <item.icon className="size-[18px]" />
                            </TooltipTrigger>
                            <TooltipContent side="right">{item.label}</TooltipContent>
                        </Tooltip>
                    );
                })}
            </nav>
            <div className="mt-auto">
                <AccountMenu />
            </div>
        </aside>
    );
}

function Avatar({ className }: { className?: string }) {
    const { auth } = usePage().props;
    const letter = (auth.user?.name || auth.user?.email || '?').slice(0, 1);

    return (
        <span
            className={cn(
                'flex size-8 shrink-0 items-center justify-center rounded-full bg-sidebar-accent text-xs font-semibold uppercase',
                className,
            )}
        >
            {letter}
        </span>
    );
}

function AccountMenu() {
    const { auth } = usePage().props;
    const [theme, setTheme] = useTheme();

    return (
        <DropdownMenu>
            <Tooltip>
                <TooltipTrigger
                    render={
                        <DropdownMenuTrigger
                            aria-label="Account and theme"
                            className="flex size-10 items-center justify-center rounded-lg hover:bg-sidebar-accent/60"
                        />
                    }
                >
                    <Avatar />
                </TooltipTrigger>
                <TooltipContent side="right">Account and theme</TooltipContent>
            </Tooltip>
            <DropdownMenuContent side="right" align="end" sideOffset={8} className="w-56">
                {auth.user && (
                    <DropdownMenuGroup>
                        <DropdownMenuLabel className="font-normal">
                            <p className="truncate text-sm font-medium text-foreground">{auth.user.name}</p>
                            <p className="truncate text-xs text-muted-foreground">{auth.user.email}</p>
                        </DropdownMenuLabel>
                    </DropdownMenuGroup>
                )}
                <DropdownMenuSeparator />
                <DropdownMenuGroup>
                    <DropdownMenuLabel>Theme</DropdownMenuLabel>
                    <DropdownMenuRadioGroup value={theme} onValueChange={(value) => setTheme(value as Theme)}>
                        {themes.map(({ value, label, icon: Icon }) => (
                            <DropdownMenuRadioItem key={value} value={value}>
                                <Icon />
                                {label}
                            </DropdownMenuRadioItem>
                        ))}
                    </DropdownMenuRadioGroup>
                </DropdownMenuGroup>
                <DropdownMenuSeparator />
                <DropdownMenuItem onClick={() => router.post('/logout')}>
                    <LogOutIcon />
                    Log out
                </DropdownMenuItem>
            </DropdownMenuContent>
        </DropdownMenu>
    );
}

// Drawer is the navigation on phones and tablets, with labels.
function Drawer() {
    const { appName, auth } = usePage().props;
    const isActive = useActive();
    const [theme, setTheme] = useTheme();

    return (
        <div className="flex h-full flex-col">
            <div className="flex h-14 items-center border-b border-sidebar-border px-4">
                <Logo name={appName} />
            </div>
            <nav aria-label="Main" className="flex-1 space-y-0.5 overflow-y-auto p-3">
                {nav.map((item) => {
                    const active = isActive(item);
                    return (
                        <Link
                            key={item.href}
                            href={item.href}
                            aria-current={active ? 'page' : undefined}
                            className={cn(
                                'flex h-10 items-center gap-3 rounded-lg px-3 text-sm transition-colors',
                                active
                                    ? 'bg-sidebar-accent font-medium text-sidebar-accent-foreground'
                                    : 'text-sidebar-foreground hover:bg-sidebar-accent/60',
                            )}
                        >
                            <item.icon className={cn('size-4', active ? 'text-primary' : 'opacity-70')} />
                            {item.label}
                        </Link>
                    );
                })}
            </nav>
            <div className="space-y-3 border-t border-sidebar-border p-3">
                <div
                    role="radiogroup"
                    aria-label="Theme"
                    className="grid grid-cols-3 gap-0.5 rounded-lg bg-sidebar-accent/70 p-0.5"
                >
                    {themes.map(({ value, label, icon: Icon }) => (
                        <label
                            key={value}
                            className={cn(
                                'flex h-8 cursor-pointer items-center justify-center gap-1.5 rounded-md text-xs transition-colors has-focus-visible:ring-3 has-focus-visible:ring-ring/50',
                                theme === value ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground',
                            )}
                        >
                            <input
                                type="radio"
                                name="theme"
                                value={value}
                                checked={theme === value}
                                onChange={() => setTheme(value)}
                                className="sr-only"
                            />
                            <Icon className="size-3.5" />
                            {label}
                        </label>
                    ))}
                </div>
                {auth.user && (
                    <div className="flex items-center gap-2.5 px-1">
                        <Avatar />
                        <div className="min-w-0 flex-1 leading-tight">
                            <p className="truncate text-sm font-medium">{auth.user.name}</p>
                            <p className="truncate text-xs text-muted-foreground">{auth.user.email}</p>
                        </div>
                        <Link
                            href="/logout"
                            method="post"
                            as="button"
                            aria-label="Log out"
                            className="rounded-md p-2 text-muted-foreground hover:bg-sidebar-accent hover:text-foreground"
                        >
                            <LogOutIcon className="size-4" />
                        </Link>
                    </div>
                )}
            </div>
        </div>
    );
}
