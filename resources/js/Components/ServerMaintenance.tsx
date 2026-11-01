import { Link } from '@inertiajs/react';
import { ArrowUpRightIcon, ChevronRightIcon } from 'lucide-react';
import { useState } from 'react';

import { CopyButton } from '@/Components/CopyButton';
import { Panel } from '@/Components/Panel';
import { StatusPill, type Tone } from '@/Components/StatusBadge';
import { Button, buttonVariants } from '@/Components/ui/button';
import { type Check, type CheckTone, isDue, maintenanceChecks, measured } from '@/lib/maintenance';
import { timeAgo } from '@/lib/time';
import { cn } from '@/lib/utils';
import type { Maintenance, ServerFacts } from '@/types/models';

const pills: Partial<Record<CheckTone, Tone>> = { danger: 'danger', warning: 'warning', update: 'primary' };

// ServerMaintenance is the Server page's Maintenance panel (ADR 0041): what
// the server itself needs doing, worst first, each with the command to run
// over SSH. maintenance arrives after the page. With nothing measured, on
// macOS or in a container, there's no panel.
export function ServerMaintenance({
    maintenance,
    facts,
}: {
    maintenance: Maintenance | undefined;
    facts: ServerFacts;
}) {
    if (!maintenance) {
        return (
            <Panel title="Maintenance">
                <p className="text-sm text-muted-foreground">Checking the server…</p>
            </Panel>
        );
    }
    const due = maintenanceChecks(maintenance, facts).filter(isDue);
    if (due.length === 0 && !measured(maintenance)) {
        return null;
    }
    const listsUpdated = maintenance.updates?.lists_updated;

    if (due.length === 0) {
        return (
            <Panel title="Maintenance">
                <p className="text-sm text-muted-foreground">
                    Nothing to do on the server.{listsUpdated && ` Package lists updated ${timeAgo(listsUpdated)}.`}
                </p>
            </Panel>
        );
    }

    return (
        <Panel title="Maintenance" description="Run these over SSH. Echowa only reads the server." flush>
            <ul className="divide-y">
                {due.map((check) => (
                    <Row key={check.key} check={check} />
                ))}
            </ul>
        </Panel>
    );
}

// Row is a check that needs doing: its pill, then why, what doing it
// means and how. From sm the pills make a column of their own.
function Row({ check }: { check: Check }) {
    return (
        <li className="grid gap-x-4 gap-y-2 px-4 py-4 sm:grid-cols-[12.5rem_minmax(0,1fr)] sm:px-5">
            <div>
                <StatusPill tone={pills[check.tone] ?? 'neutral'}>{check.title}</StatusPill>
            </div>
            <div className="min-w-0 space-y-2 text-sm sm:pt-0.5">
                {check.why && <p>{check.why}</p>}
                {check.note && <p className="text-muted-foreground">{check.note}</p>}
                {check.command && <Command command={check.command} />}
                {check.href && (
                    <Link href={check.href} className={buttonVariants({ variant: 'outline', size: 'sm' })}>
                        Go to Updates
                        <ArrowUpRightIcon />
                    </Link>
                )}
                {check.packages && <Packages packages={check.packages} />}
            </div>
        </li>
    );
}

// Command wraps rather than scrolls, so the copy button stays in view on
// a phone.
function Command({ command }: { command: string }) {
    return (
        <div className="flex items-start gap-2 rounded-md bg-muted py-1 pr-1 pl-3">
            <code className="min-w-0 flex-1 py-0.5 font-mono text-xs leading-5 wrap-anywhere select-all">
                {command}
            </code>
            <CopyButton label="command" value={command} />
        </div>
    );
}

// Packages is what an update would install, behind a toggle, in the plain
// monospace of a log.
function Packages({ packages }: { packages: NonNullable<Check['packages']> }) {
    const [open, setOpen] = useState(false);

    return (
        <div>
            <Button
                variant="ghost"
                size="xs"
                className="-ml-2 text-muted-foreground"
                aria-expanded={open}
                onClick={() => setOpen(!open)}
            >
                <ChevronRightIcon className={cn('transition-transform', open && 'rotate-90')} />
                {open ? 'Hide packages' : 'Show packages'}
            </Button>
            {open && (
                <ul className="mt-1 max-h-64 overflow-auto rounded-md bg-muted px-3 py-2 font-mono text-xs leading-5">
                    {packages.map((pkg) => (
                        <li key={pkg.name} className="flex justify-between gap-3">
                            <span className="min-w-0 truncate">{pkg.name}</span>
                            {pkg.security && <span className="shrink-0 text-destructive">security</span>}
                        </li>
                    ))}
                </ul>
            )}
        </div>
    );
}
