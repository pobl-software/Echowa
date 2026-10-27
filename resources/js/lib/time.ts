const units: [Intl.RelativeTimeFormatUnit, number][] = [
    ['year', 365 * 24 * 3600],
    ['month', 30 * 24 * 3600],
    ['day', 24 * 3600],
    ['hour', 3600],
    ['minute', 60],
    ['second', 1],
];

const relative = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });

// Goravel serialises model times as "2026-10-23 22:17:34", in the app
// timezone (UTC, config/app.go) but without saying so. Date would read that
// as local time, so mark it as UTC.
export function parseServerTime(value: string): Date {
    const hasZone = /(Z|[+-]\d\d:?\d\d)$/.test(value);

    return new Date(hasZone ? value : `${value.replace(' ', 'T')}Z`);
}

// "3 minutes ago".
export function timeAgo(value: string | null | undefined): string {
    if (!value) {
        return '';
    }
    const seconds = (parseServerTime(value).getTime() - Date.now()) / 1000;
    for (const [unit, size] of units) {
        if (Math.abs(seconds) >= size || unit === 'second') {
            return relative.format(Math.round(seconds / size), unit);
        }
    }

    return '';
}

// Full local date and time, for title tooltips.
export function formatDateTime(value: string | null | undefined): string {
    return value ? parseServerTime(value).toLocaleString() : '';
}

// "25 Jan 2027", in the browser's locale.
export function formatDate(value: string | null | undefined): string {
    return value
        ? parseServerTime(value).toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' })
        : '';
}

// "1m 12s", between two server timestamps (or until now).
export function duration(from: string | null | undefined, to?: string | null): string {
    if (!from) {
        return '';
    }
    const end = to ? parseServerTime(to) : new Date();
    const seconds = Math.max(0, Math.round((end.getTime() - parseServerTime(from).getTime()) / 1000));
    const minutes = Math.floor(seconds / 60);

    return minutes > 0 ? `${minutes}m ${seconds % 60}s` : `${seconds}s`;
}

// "Mon 2 Nov, 09:00", in the given IANA zone rather than the browser's.
export function formatInZone(value: string, zone: string): string {
    return parseServerTime(value).toLocaleString(undefined, {
        timeZone: zone,
        weekday: 'short',
        day: 'numeric',
        month: 'short',
        hour: '2-digit',
        minute: '2-digit',
    });
}
