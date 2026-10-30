import type { MouseEvent, ReactNode } from 'react';

import type { AppSection } from '@/Components/AppNav';
import type { App } from '@/types/models';

// SectionLink switches to another section in place, as the section nav does;
// a modified click opens it in a new tab.
export function SectionLink({
    app,
    section,
    onSelect,
    children,
}: {
    app: Pick<App, 'id'>;
    section: AppSection;
    onSelect: (section: AppSection) => void;
    children: ReactNode;
}) {
    const view = (event: MouseEvent) => {
        if (!event.metaKey && !event.ctrlKey && !event.shiftKey) {
            event.preventDefault();
            onSelect(section);
        }
    };

    return (
        <a href={`/apps/${app.id}?tab=${section}`} onClick={view} className="hover:underline">
            {children}
        </a>
    );
}
