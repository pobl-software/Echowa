import type { ComponentType, ReactNode } from 'react';

import { Panel } from '@/Components/Panel';
import { Button } from '@/Components/ui/button';

// ConfigPanel is the layout for a page of settings (docs/design.md): one
// panel of ConfigSections, and a footer, usually a SaveBar. Put it inside
// the <form>, so the SaveBar's Save submits it.
export function ConfigPanel({ footer, children }: { footer?: ReactNode; children: ReactNode }) {
    return (
        // overflow-clip, not hidden, so the save bar can stick to the bottom of the page.
        <Panel flush className="overflow-clip">
            <div className="divide-y">{children}</div>
            {footer}
        </Panel>
    );
}

// ConfigSection is a row of a ConfigPanel: what it's for on the left, its
// fields on the right. They stack on phones.
export function ConfigSection({
    icon: Icon,
    title,
    description,
    children,
}: {
    icon: ComponentType<{ className?: string }>;
    title: string;
    description: string;
    children: ReactNode;
}) {
    return (
        <section className="grid md:grid-cols-[15.5rem_minmax(0,1fr)]">
            {/* Padding is on the columns, not the row, so the left one's border runs the panel's full height. */}
            <div className="flex gap-3 px-4 pt-5 pb-4 sm:px-5 md:block md:border-r md:pb-5">
                <span className="flex size-8 shrink-0 items-center justify-center rounded-md border bg-muted/50 md:mb-3">
                    <Icon className="size-4 text-muted-foreground" />
                </span>
                {/* The description is faded from md, and comes back on hover. Phones have no hover, so it stays. */}
                <div className="group/title cursor-default">
                    <h3 className="text-sm font-medium">{title}</h3>
                    <p className="mt-0.5 text-sm text-muted-foreground transition-opacity duration-200 md:opacity-50 md:group-hover/title:opacity-100">
                        {description}
                    </p>
                </div>
            </div>
            <div className="min-w-0 px-4 pb-5 sm:px-5 md:px-8 md:pt-5">
                <div className="flex max-w-xl flex-col gap-4">{children}</div>
            </div>
        </section>
    );
}

// SaveBar is a ConfigPanel's footer while its form has unsaved changes,
// stuck to the bottom of the page. note says when they take effect; it's
// left out on phones.
export function SaveBar({
    dirty,
    processing,
    onDiscard,
    note,
}: {
    dirty: boolean;
    processing: boolean;
    onDiscard: () => void;
    note?: string;
}) {
    if (!dirty) {
        return null;
    }

    return (
        <div className="sticky bottom-0 flex items-center gap-2 border-t bg-card px-4 py-3 sm:px-5">
            <p className="flex min-w-0 items-center gap-2 text-sm">
                <span className="size-1.5 shrink-0 rounded-full bg-warning" />
                <span>
                    Unsaved changes
                    {note && <span className="hidden text-muted-foreground sm:inline">. {note}</span>}
                </span>
            </p>
            <div className="ml-auto flex gap-2">
                <Button type="button" variant="ghost" onClick={onDiscard} disabled={processing}>
                    Discard
                </Button>
                <Button type="submit" disabled={processing}>
                    Save
                </Button>
            </div>
        </div>
    );
}
