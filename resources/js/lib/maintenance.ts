import { router } from '@inertiajs/react';
import {
    ClockIcon,
    ContainerIcon,
    type LucideIcon,
    PackageIcon,
    PackageXIcon,
    RefreshCwIcon,
    RotateCwIcon,
    ShieldCheckIcon,
    ShieldIcon,
    SquareActivityIcon,
    TimerIcon,
} from 'lucide-react';
import { useEffect } from 'react';

import { parseServerTime, timeAgo, uptime } from '@/lib/time';
import type { Maintenance, ServerFacts } from '@/types/models';

// How a check stands. danger and warning need doing; update is a newer
// Echowa; unknown wasn't measured.
export type CheckTone = 'danger' | 'warning' | 'update' | 'unknown' | 'ok';

// A check on the server (ADR 0041). The Server page's Maintenance panel
// shows the ones that need doing, in full; the dashboard's sidebar lists
// the summary ones always, and the rest when they need doing.
export interface Check {
    key: string;
    icon: LucideIcon;
    // A short name, for the sidebar, and the panel's fuller one.
    label: string;
    title: string;
    tone: CheckTone;
    // A few words, for the sidebar: "4 pending", "Not needed".
    value: string;
    summary: boolean;
    // For the panel: why it's due, then what doing it means.
    why?: string;
    note?: string;
    // The command to run over SSH, or the page in Echowa that does it.
    command?: string;
    href?: string;
    // What an update would install, security fixes first.
    packages?: { name: string; security: boolean }[];
}

type State = Pick<Check, 'tone' | 'value' | 'why' | 'note' | 'command' | 'href' | 'packages'> &
    Partial<Pick<Check, 'label' | 'title'>>;

interface Definition {
    key: string;
    icon: LucideIcon;
    label: string;
    summary: boolean;
    // null when it wasn't measured.
    read: (m: Maintenance, facts: ServerFacts, now: number) => State | null;
}

const DAY = 86_400_000;
const upgrade = 'sudo apt update && sudo apt upgrade';
const support = 'Upgrade to a newer release, or move to a new server.';

// In the order rows that tie are shown.
const definitions: Definition[] = [
    {
        key: 'interrupted',
        icon: PackageXIcon,
        label: 'Package installs',
        summary: false,
        read: (m) =>
            m.interrupted == null
                ? null
                : m.interrupted
                  ? {
                        title: 'Install interrupted',
                        tone: 'danger',
                        value: 'Interrupted',
                        why: 'A package install stopped part way, and apt won’t install anything else until it’s finished.',
                        command: 'sudo dpkg --configure -a',
                    }
                  : { tone: 'ok', value: 'Finished' },
    },
    {
        key: 'restart',
        icon: RotateCwIcon,
        label: 'Restart',
        summary: true,
        read: ({ restart }, facts, now) => {
            if (restart == null) {
                return null;
            }
            if (!restart.required) {
                return { tone: 'ok', value: 'Not needed' };
            }
            const since =
                restart.since != null ? Math.floor((now - parseServerTime(restart.since).getTime()) / DAY) : null;
            const why = restart.new_kernel
                ? `Kernel ${restart.new_kernel} installed, ${restart.running_kernel} running`
                : `${restart.packages.length > 0 ? list(restart.packages) : 'A package'} asked for it ${timeAgo(restart.since)}`;

            return {
                label: 'Restart required',
                tone: 'warning',
                value: since == null ? 'New kernel' : since === 0 ? 'Today' : count(since, 'day'),
                why: `${why}${facts.booted_at ? `, up ${uptime(facts.booted_at)}` : ''}.`,
                note: 'Every app, and Echowa, is down until the server is back up.',
                command: 'sudo reboot',
            };
        },
    },
    {
        key: 'updates',
        icon: ShieldIcon,
        label: 'Updates',
        summary: true,
        read: ({ updates }) => {
            if (updates == null) {
                return null;
            }
            const { security, other } = updates;
            const packages = [
                ...security.map((name) => ({ name, security: true })),
                ...other.map((name) => ({ name, security: false })),
            ];
            if (security.length > 0) {
                return {
                    label: 'Security updates',
                    tone: 'danger',
                    value: `${security.length} pending`,
                    why: `${count(security.length, 'security update')}${other.length > 0 ? ` and ${count(other.length, 'other')}` : ''} ready to install.`,
                    command: upgrade,
                    packages,
                };
            }
            if (other.length > 0) {
                return {
                    tone: 'warning',
                    value: `${other.length} pending`,
                    why: `${count(other.length, 'update')} ready to install, none of them security fixes.`,
                    command: upgrade,
                    packages,
                };
            }

            return { tone: 'ok', value: 'None' };
        },
    },
    {
        key: 'docker',
        icon: ContainerIcon,
        label: 'Docker',
        summary: false,
        read: ({ updates }) =>
            updates == null
                ? null
                : updates.docker.length > 0
                  ? {
                        title: 'Docker update',
                        tone: 'warning',
                        value: 'Update pending',
                        why: `${list(updates.docker)} ${updates.docker.length === 1 ? 'is' : 'are'} among the updates.`,
                        note: 'Installing them restarts dockerd, which restarts every app: Swarm has no live restore.',
                    }
                  : { tone: 'ok', value: 'Up to date' },
    },
    {
        key: 'lists',
        icon: RefreshCwIcon,
        label: 'Package lists',
        summary: false,
        read: ({ updates }, _, now) => {
            if (updates?.lists_updated == null) {
                return null;
            }
            if (!updates.lists_stale) {
                return { tone: 'ok', value: `Updated ${timeAgo(updates.lists_updated)}` };
            }
            // Rounded as timeAgo rounds, so the panel and sidebar agree.
            const age = Math.round((now - parseServerTime(updates.lists_updated).getTime()) / DAY);

            return {
                title: 'Package lists out of date',
                tone: 'warning',
                value: `${count(age, 'day')} old`,
                why: `apt last fetched them ${timeAgo(updates.lists_updated)}, so newer updates aren’t counted.`,
                command: 'sudo apt update',
            };
        },
    },
    {
        key: 'auto',
        icon: ShieldCheckIcon,
        label: 'Automatic updates',
        summary: false,
        read: (m) =>
            m.auto_updates == null
                ? null
                : m.auto_updates
                  ? { tone: 'ok', value: 'On' }
                  : {
                        title: 'Automatic updates off',
                        tone: 'warning',
                        value: 'Off',
                        why: 'Security updates wait until someone installs them.',
                        command:
                            'sudo apt install unattended-upgrades && sudo dpkg-reconfigure -plow unattended-upgrades',
                    },
    },
    {
        key: 'support',
        icon: TimerIcon,
        label: 'OS support',
        summary: true,
        read: ({ support: s }, _, now) => {
            if (s == null) {
                return null;
            }
            const ends = parseServerTime(s.ends);
            const date = ends.toLocaleDateString(undefined, {
                day: 'numeric',
                month: 'short',
                year: 'numeric',
                timeZone: 'UTC',
            });
            if (ends.getTime() <= now) {
                return {
                    title: 'Out of support',
                    tone: 'danger',
                    value: 'Ended',
                    why: `Standard security updates for ${s.release} ended on ${date}.`,
                    note: support,
                };
            }
            if (s.ending) {
                return {
                    title: 'Support ending',
                    tone: 'warning',
                    value: `${count(Math.ceil((ends.getTime() - now) / DAY), 'day')} left`,
                    why: `Standard security updates for ${s.release} end on ${date}.`,
                    note: support,
                };
            }

            return {
                tone: 'ok',
                value: `Until ${ends.toLocaleDateString(undefined, { month: 'short', year: 'numeric', timeZone: 'UTC' })}`,
            };
        },
    },
    {
        key: 'services',
        icon: SquareActivityIcon,
        label: 'System services',
        summary: true,
        read: ({ failed_units: units }) =>
            units == null
                ? null
                : units.length > 0
                  ? {
                        title: units.length === 1 ? 'A service failed' : 'Services failed',
                        tone: 'warning',
                        value: `${units.length} failed`,
                        why: `${list(units)} ${units.length === 1 ? 'is' : 'are'} in systemd’s failed state.`,
                        command: `systemctl status ${units.map(shellWord).join(' ')}`,
                    }
                  : { tone: 'ok', value: 'All running' },
    },
    {
        key: 'clock',
        icon: ClockIcon,
        label: 'Clock',
        summary: true,
        read: ({ clock_synced: synced, clock_offset: offset }) => {
            if (synced == null) {
                return null;
            }
            if (synced) {
                // With an offset, nothing syncs the clock but it's right:
                // OrbStack sets its machines' clocks from the Mac.
                return { tone: 'ok', value: offset == null ? 'Synced' : 'On time' };
            }
            if (offset == null) {
                return {
                    title: 'Clock not synced',
                    tone: 'warning',
                    value: 'Not synced',
                    why: 'Certificates, schedules and backups rely on the time.',
                    command: 'timedatectl status',
                };
            }
            const off = `${Math.round(Math.abs(offset))} s ${offset > 0 ? 'fast' : 'slow'}`;

            return {
                title: 'Clock is off',
                tone: 'warning',
                value: off,
                why: `It’s ${off}, and nothing keeps it in sync. Certificates, schedules and backups rely on the time.`,
                command: 'sudo timedatectl set-ntp true',
            };
        },
    },
    {
        key: 'echowa',
        icon: PackageIcon,
        label: 'Echowa',
        summary: true,
        read: (_, facts) =>
            facts.update
                ? {
                      title: 'Echowa update',
                      tone: 'update',
                      value: `${facts.update} available`,
                      why: `${facts.update} is out, and this server runs ${facts.version}.`,
                      href: '/settings/updates',
                  }
                : facts.update_checked
                  ? { tone: 'ok', value: facts.version }
                  : { tone: 'unknown', value: `${facts.version} · not checked` },
    },
];

const rank: Record<CheckTone, number> = { danger: 0, warning: 1, update: 2, unknown: 3, ok: 4 };

// maintenanceChecks is every check, worst first.
export function maintenanceChecks(m: Maintenance, facts: ServerFacts): Check[] {
    const now = Date.now();

    return definitions
        .map(({ read, ...definition }, i) => {
            const state = read(m, facts, now);
            const label = state?.label ?? definition.label;
            const check: Check = {
                ...definition,
                tone: 'unknown',
                value: 'Not checked',
                ...state,
                label,
                title: state?.title ?? label,
            };
            return { i, check };
        })
        .sort((a, b) => rank[a.check.tone] - rank[b.check.tone] || a.i - b.i)
        .map(({ check }) => check);
}

// isDue is whether a check needs something done.
export function isDue(check: Check): boolean {
    return check.tone === 'danger' || check.tone === 'warning' || check.tone === 'update';
}

// measured is whether Echowa could read anything about the server. On
// macOS or in a container it can't, and nothing may be shown as fine.
export function measured(m: Maintenance): boolean {
    return Object.entries(m).some(([key, value]) => key !== 'read_at' && value != null);
}

// The rail's dot says this while serverAttention is true.
export const attentionNote = 'Server needs maintenance';

// useAttentionAfter asks for serverAttention again once maintenance has
// arrived. The dot reads only apt's last run, and reading maintenance may
// have just made the first, so without this the dot could miss the
// security updates the page shows.
export function useAttentionAfter(maintenance: Maintenance | undefined) {
    useEffect(() => {
        if (maintenance) {
            router.reload({ only: ['serverAttention'] });
        }
    }, [maintenance]);
}

// "3 days", "1 security update".
function count(n: number, noun: string): string {
    return `${n} ${noun}${n === 1 ? '' : 's'}`;
}

// "a, b and c".
function list(items: string[]): string {
    return items.length <= 1 ? (items[0] ?? '') : `${items.slice(0, -1).join(', ')} and ${items[items.length - 1]}`;
}

// A unit name as one shell word: an escaped one, such as a mount's
// "dev-disk-by\x2duuid-…", goes in single quotes.
function shellWord(word: string): string {
    return /^[\w@%+=:,./-]+$/.test(word) ? word : `'${word.replaceAll("'", `'\\''`)}'`;
}
