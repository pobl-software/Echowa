import { Head, Link, router, usePage } from '@inertiajs/react';
import { BoxIcon, LayoutTemplateIcon } from 'lucide-react';

import { AppForm } from '@/Components/AppForm';
import { FromBackup } from '@/Components/NewAppRestore';
import { Page } from '@/Components/Page';
import { Panel } from '@/Components/Panel';
import { Tabs, TabsList, TabsTrigger } from '@/Components/ui/tabs';
import { type AppIcon, templateIcon } from '@/lib/templates';
import { cn } from '@/lib/utils';
import type { RegistryOption, StorageChoice, TemplateOption } from '@/types/models';

export default function AppsCreate({
    registries,
    templates,
    locations,
}: {
    registries: RegistryOption[];
    templates: TemplateOption[];
    locations: StorageChoice[];
}) {
    const { url, props } = usePage();
    // ?template with no value is the Template choice before one is picked.
    // ?from=backup restores into the template's new app, for templates with
    // backups.
    const params = new URL(url, window.location.origin).searchParams;
    const fromTemplate = params.has('template');
    const template = templates.find((t) => t.slug === params.get('template'));
    const fromBackup = template?.backups === true && params.get('from') === 'backup';
    const templateError = (props.errors as Record<string, string | undefined>).template;

    return (
        <>
            <Head title={template ? `New ${template.name}` : 'New app'} />
            <Page
                crumbs={[{ label: 'Apps', href: '/dashboard' }, { label: 'New app' }]}
                description={
                    fromBackup
                        ? `A ${template.name} app with a backup’s data: Echowa deploys it, then restores the backup into it.`
                        : fromTemplate
                          ? 'Ready to run: Echowa sets the environment and deploys it as soon as it’s created.'
                          : 'One image, run as a Swarm service. Nothing is deployed until you deploy it.'
                }
            >
                <div className="flex max-w-2xl flex-col gap-4">
                    <nav aria-label="Start from" className="grid gap-3 sm:grid-cols-2">
                        <Choice
                            href="/apps/create"
                            icon={BoxIcon}
                            title="Your own image"
                            detail="From any registry"
                            selected={!fromTemplate}
                        />
                        <Choice
                            href={`/apps/create?template=${template?.slug ?? ''}`}
                            icon={LayoutTemplateIcon}
                            title="Template"
                            detail="Databases, set up for you"
                            selected={fromTemplate}
                        />
                    </nav>
                    {fromTemplate && (
                        <section aria-labelledby="templates" className="flex flex-col gap-2.5">
                            <h2 id="templates" className="text-sm font-medium">
                                Choose a template
                            </h2>
                            <nav aria-label="Templates" className="grid gap-3 sm:grid-cols-3">
                                {templates.map((t) => (
                                    <Choice
                                        key={t.slug}
                                        href={`/apps/create?template=${t.slug}`}
                                        icon={templateIcon(t.slug)}
                                        title={t.name}
                                        detail={`${t.repository}:${t.default_tag}`}
                                        mono
                                        selected={t.slug === template?.slug}
                                    />
                                ))}
                            </nav>
                        </section>
                    )}
                    {templateError && <p className="text-sm text-destructive">{templateError}</p>}
                    {template?.backups && (
                        <Tabs
                            value={fromBackup ? 'backup' : 'empty'}
                            onValueChange={(value) =>
                                router.get(
                                    `/apps/create?template=${template.slug}${value === 'backup' ? '&from=backup' : ''}`,
                                    {},
                                    { replace: true, preserveScroll: true },
                                )
                            }
                        >
                            <TabsList aria-label={`Start ${template.name}`}>
                                <TabsTrigger value="empty" className="px-2.5">
                                    Start empty
                                </TabsTrigger>
                                <TabsTrigger value="backup" className="px-2.5">
                                    From a backup
                                </TabsTrigger>
                            </TabsList>
                        </Tabs>
                    )}
                    {fromBackup && <FromBackup locations={locations} template={template} />}
                    {!fromBackup && (!fromTemplate || template) && (
                        <Panel title={template?.name} description={template?.description}>
                            <AppForm key={template?.slug ?? 'custom'} registries={registries} template={template} />
                        </Panel>
                    )}
                </div>
            </Page>
        </>
    );
}

// Choice is one way to start a new app. Picking it reloads the page with
// ?template, so the choice survives a reload and a validation error.
function Choice({
    href,
    icon: Icon,
    title,
    detail,
    mono = false,
    selected,
}: {
    href: string;
    icon: AppIcon;
    title: string;
    detail: string;
    mono?: boolean;
    selected: boolean;
}) {
    return (
        <Link
            href={href}
            replace
            preserveScroll
            aria-current={selected ? 'page' : undefined}
            className={cn(
                'flex items-center gap-3 rounded-xl border bg-card px-3.5 py-3 text-left transition-colors hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none',
                selected && 'border-primary bg-primary/5 ring-1 ring-primary/40 hover:bg-primary/5',
            )}
        >
            <span
                className={cn(
                    'flex size-9 shrink-0 items-center justify-center rounded-lg border bg-background',
                    selected && 'border-primary/40 text-primary',
                )}
            >
                <Icon className="size-4" />
            </span>
            <span className="min-w-0">
                <span className="block text-sm font-medium">{title}</span>
                <span className={cn('block truncate text-xs text-muted-foreground', mono && 'font-mono')}>
                    {detail}
                </span>
            </span>
        </Link>
    );
}
