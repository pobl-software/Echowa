// Number formats for metrics.

// Binary units, as `free -h` shows them: "7.8 GiB".
export function formatBytes(bytes: number): string {
    const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
    let value = bytes;
    let unit = 0;
    while (value >= 1024 && unit < units.length - 1) {
        value /= 1024;
        unit++;
    }
    return `${value.toFixed(value >= 10 || unit === 0 ? 0 : 1)} ${units[unit]}`;
}

// "12 MiB/s".
export function formatRate(bytesPerSecond: number): string {
    return `${formatBytes(bytesPerSecond)}/s`;
}

// "42%", or "4.2%" below 10.
export function formatPercent(percent: number): string {
    return `${percent.toFixed(percent < 10 && percent !== 0 ? 1 : 0)}%`;
}

// "1.2/s", or "0.05/s" for rare events.
export function formatPerSecond(perSecond: number): string {
    return `${perSecond >= 10 || perSecond === 0 ? perSecond.toFixed(0) : perSecond >= 0.1 ? perSecond.toFixed(1) : perSecond.toFixed(2)}/s`;
}

// "45 s", "12 min", "3.5 h".
export function formatSeconds(seconds: number): string {
    if (seconds < 60) {
        return `${seconds.toFixed(0)} s`;
    }
    if (seconds < 3600) {
        return `${(seconds / 60).toFixed(0)} min`;
    }
    return `${(seconds / 3600).toFixed(1)} h`;
}

// "<0.01 ms", "0.42 ms", "12 ms", "1.2 s", then as formatSeconds.
export function formatMilliseconds(ms: number): string {
    if (ms > 0 && ms < 0.01) {
        return '<0.01 ms';
    }
    if (ms < 10) {
        return `${ms.toFixed(2)} ms`;
    }
    if (ms < 1000) {
        return `${ms.toFixed(0)} ms`;
    }
    if (ms < 60_000) {
        return `${(ms / 1000).toFixed(1)} s`;
    }
    return formatSeconds(ms / 1000);
}

// "1,234,567": whole numbers with separators.
export function formatCount(count: number): string {
    return Math.round(count).toLocaleString('en');
}

// What a chart's y axis counts, for its ticks.
export type AxisUnit = 'bytes' | 'rate' | 'percent' | 'count' | 'number';

const byteUnits = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];

// The y axis from 0 to max in about four round steps (1, 2, 2.5 or 5
// times a power of ten), each labelled exactly. Bytes step in round
// amounts of one unit, and every label uses the top tick's unit
// ("0.25 MiB", not "256 KiB" beside "1.0 MiB"). Counts, and bytes
// below a KiB, step in whole numbers.
export function axisTicks(max: number, unit: AxisUnit): { values: number[]; label: (value: number) => string } {
    const binary = unit === 'bytes' || unit === 'rate';
    const top = max > 0 ? max : binary ? 1024 : 1;
    const power = binary ? Math.min(Math.max(Math.floor(Math.log(top) / Math.log(1024)), 0), byteUnits.length - 1) : 0;
    const scale = 1024 ** power;

    let step = niceStep(top / scale / 4);
    if (unit === 'count' || (binary && scale === 1)) {
        step = Math.max(step, 1);
    }
    const steps = Math.ceil(top / scale / step - 1e-9);
    const values = Array.from({ length: steps + 1 }, (_, i) => i * step * scale);

    // As many decimals as the step needs, less trailing zeros: 2.5, 5, 7.5.
    const decimals = (String(step).split('.')[1] ?? '').length;
    const number = (value: number) => String(Number((value / scale).toFixed(decimals)));
    const label =
        unit === 'bytes'
            ? (value: number) => `${number(value)} ${byteUnits[power]}`
            : unit === 'rate'
              ? (value: number) => `${number(value)} ${byteUnits[power]}/s`
              : unit === 'percent'
                ? (value: number) => `${number(value)}%`
                : number;

    return { values, label };
}

function niceStep(raw: number): number {
    const power = 10 ** Math.floor(Math.log10(raw));
    const fraction = raw / power;
    const nice = fraction <= 1 ? 1 : fraction <= 2 ? 2 : fraction <= 2.5 ? 2.5 : fraction <= 5 ? 5 : 10;
    // Rounded so 0.1 * 3 doesn't print as 0.30000000000000004.
    return Number((nice * power).toPrecision(2));
}

// "5 min", "2 h", "1 day", for a bucket size in seconds.
export function formatStep(seconds: number): string {
    if (seconds >= 86400) {
        const days = seconds / 86400;
        return `${days} ${days === 1 ? 'day' : 'days'}`;
    }
    return seconds >= 3600 ? `${seconds / 3600} h` : `${seconds / 60} min`;
}
