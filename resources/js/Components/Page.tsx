import { Link, usePage } from '@inertiajs/react';
import { ChevronRightIcon, MenuIcon, TriangleAlertIcon } from 'lucide-react';
import { Fragment, type ReactNode } from 'react';

import { Alert, AlertDescription, AlertTitle } from '@/Components/ui/alert';
import { Button } from '@/Components/ui/button';
import { useShell } from '@/lib/shell';
import { cn } from '@/lib/utils';

export interface Crumb {
    label: string;
    href?: string;
}

// Page is every signed-in page inside the layout's frame: a top bar with
// breadcrumbs (the last is the page's title), status and actions; an
// optional sub nav (aside on wide screens, tabs on phones); then the
// content, which scrolls on its own; and an optional sidebar, a column on
// the right from lg and under the content below it. Phones show only the
// title, or phoneTitle when the last crumb alone says too little.
export function Page({
    crumbs,
    phoneTitle,
    status,
    actions,
    aside,
    tabs,
    description,
    sidebar,
    className,
    children,
}: {
    crumbs: Crumb[];
    phoneTitle?: string;
    status?: ReactNode;
    actions?: ReactNode;
    aside?: ReactNode;
    tabs?: ReactNode;
    description?: ReactNode;
    sidebar?: ReactNode;
    className?: string;
    children: ReactNode;
}) {
    const { dockerProblem } = usePage().props;
    const { openMenu } = useShell();
    const title = crumbs[crumbs.length - 1];

    return (
        <div className="flex min-w-0 flex-1">
            {aside && <aside className="hidden w-60 shrink-0 flex-col border-r lg:flex">{aside}</aside>}
            <div className="flex min-w-0 flex-1 flex-col">
                <header className="flex h-14 shrink-0 items-center gap-3 border-b px-3 sm:px-5">
                    <Button variant="ghost" size="icon" className="lg:hidden" aria-label="Open menu" onClick={openMenu}>
                        <MenuIcon />
                    </Button>
                    <nav aria-label="Breadcrumb" className="flex min-w-0 items-center gap-1.5 text-sm">
                        {crumbs.slice(0, -1).map((crumb) => (
                            <Fragment key={crumb.label}>
                                {crumb.href ? (
                                    <Link
                                        href={crumb.href}
                                        className="hidden shrink-0 text-muted-foreground hover:text-foreground sm:inline"
                                    >
                                        {crumb.label}
                                    </Link>
                                ) : (
                                    <span className="hidden shrink-0 text-muted-foreground sm:inline">
                                        {crumb.label}
                                    </span>
                                )}
                                <ChevronRightIcon className="hidden size-3.5 shrink-0 text-muted-foreground/60 sm:block" />
                            </Fragment>
                        ))}
                        {/* A top-level page's one crumb is its title; nested pages keep the breadcrumb's size. */}
                        <h1
                            className={cn(
                                'truncate font-medium',
                                crumbs.length === 1 && 'font-display text-lg tracking-tight',
                            )}
                        >
                            {phoneTitle ? (
                                <>
                                    <span className="sm:hidden">{phoneTitle}</span>
                                    <span className="hidden sm:inline">{title.label}</span>
                                </>
                            ) : (
                                title.label
                            )}
                        </h1>
                    </nav>
                    {status && <div className="hidden shrink-0 items-center gap-2 sm:flex">{status}</div>}
                    {actions && <div className="ml-auto flex shrink-0 items-center gap-2">{actions}</div>}
                </header>
                {tabs && <div className="shrink-0 border-b lg:hidden">{tabs}</div>}
                {/* With a sidebar, the content and the sidebar scroll apart from lg; below it they stack and scroll together. */}
                <div className={cn('min-h-0 flex-1 overflow-y-auto', sidebar && 'lg:flex lg:overflow-hidden')}>
                    <div className={cn(sidebar && 'lg:min-w-0 lg:flex-1 lg:overflow-y-auto')}>
                        <div className={cn('mx-auto w-full max-w-6xl px-4 py-6 sm:px-6 lg:px-8', className)}>
                            {status && <div className="mb-4 flex flex-wrap items-center gap-2 sm:hidden">{status}</div>}
                            {dockerProblem && (
                                <Alert variant="destructive" className="mb-6">
                                    <TriangleAlertIcon />
                                    <AlertTitle>Docker needs attention</AlertTitle>
                                    <AlertDescription>{dockerProblem}</AlertDescription>
                                </Alert>
                            )}
                            {description && <p className="mb-6 text-sm text-muted-foreground">{description}</p>}
                            {children}
                        </div>
                    </div>
                    {sidebar && (
                        <aside className="border-t lg:w-72 lg:shrink-0 xl:w-80 lg:overflow-y-auto lg:border-t-0 lg:border-l">
                            {sidebar}
                        </aside>
                    )}
                </div>
            </div>
        </div>
    );
}
