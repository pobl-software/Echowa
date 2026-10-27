import { BoxIcon, DatabaseIcon, type LucideIcon, ZapIcon } from 'lucide-react';

const icons: Record<string, LucideIcon> = { postgres: DatabaseIcon, mysql: DatabaseIcon, redis: ZapIcon };

// templateIcon is the icon for an app template (ADR 0025).
export function templateIcon(slug: string): LucideIcon {
    return icons[slug] ?? BoxIcon;
}
