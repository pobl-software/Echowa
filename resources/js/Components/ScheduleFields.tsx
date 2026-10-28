import { useEffect, useState } from 'react';

import {
    Combobox,
    ComboboxContent,
    ComboboxEmpty,
    ComboboxInput,
    ComboboxItem,
    ComboboxList,
} from '@/Components/ui/combobox';
import { Field, FieldDescription, FieldError, FieldLabel } from '@/Components/ui/field';
import { Input } from '@/Components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/Components/ui/select';
import { formatInZone } from '@/lib/time';

// A schedule a form offers by name. Go only sees the expression.
export type Preset = { value: string; label: string };

export const browserZone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
const zones = (() => {
    const list = Intl.supportedValuesOf('timeZone');
    return list.includes('UTC') ? list : ['UTC', ...list];
})();

export const presetLabel = (presets: Preset[], schedule: string) => presets.find((p) => p.value === schedule)?.label;

// ScheduleText is a schedule's preset name, or its expression.
export function ScheduleText({ presets, schedule }: { presets: Preset[]; schedule: string }) {
    return presetLabel(presets, schedule) ?? <span className="font-mono">{schedule}</span>;
}

// ScheduleFields picks a schedule, from presets or as a cron expression,
// and the zone it's read in, with its next runs as it's typed. Scheduled
// tasks (ADR 0032) and backup schedules (ADR 0034) share it.
export function ScheduleFields({
    presets,
    schedule,
    timezone,
    onScheduleChange,
    onTimezoneChange,
    custom,
    onCustomChange,
    errors,
    active,
}: {
    presets: Preset[];
    schedule: string;
    timezone: string;
    onScheduleChange: (schedule: string) => void;
    onTimezoneChange: (timezone: string) => void;
    custom: boolean;
    onCustomChange: (custom: boolean) => void;
    errors: { schedule?: string; timezone?: string };
    // active is false while the form is hidden, so it asks for no preview.
    active: boolean;
}) {
    const preview = useSchedulePreview(schedule, timezone, active);
    const items = [...presets, { value: 'custom', label: 'Custom cron expression' }];

    return (
        <>
            <Field data-invalid={!!errors.schedule || undefined}>
                <FieldLabel htmlFor="schedule">Schedule</FieldLabel>
                <Select
                    items={items}
                    value={custom ? 'custom' : schedule}
                    onValueChange={(value) => {
                        if (value === 'custom') {
                            onCustomChange(true);
                        } else if (value) {
                            onCustomChange(false);
                            onScheduleChange(value);
                        }
                    }}
                >
                    <SelectTrigger id="schedule" className="w-full">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        {items.map((item) => (
                            <SelectItem key={item.value} value={item.value}>
                                {item.label}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
                {custom && (
                    <Input
                        aria-label="Cron expression"
                        className="font-mono"
                        placeholder="*/5 * * * *"
                        value={schedule}
                        onChange={(e) => onScheduleChange(e.target.value)}
                        aria-invalid={!!errors.schedule || undefined}
                    />
                )}
                {errors.schedule && <FieldError>{errors.schedule}</FieldError>}
                <NextRuns preview={preview} zone={timezone} custom={custom} />
            </Field>

            <Field data-invalid={!!errors.timezone || undefined}>
                <FieldLabel htmlFor="timezone">Time zone</FieldLabel>
                <Combobox items={zones} value={timezone} onValueChange={(value) => value && onTimezoneChange(value)}>
                    <ComboboxInput
                        id="timezone"
                        className="w-full"
                        placeholder="Search time zones"
                        aria-invalid={!!errors.timezone || undefined}
                    />
                    <ComboboxContent>
                        <ComboboxEmpty>No time zone matches.</ComboboxEmpty>
                        <ComboboxList>
                            {(zone: string) => (
                                <ComboboxItem key={zone} value={zone}>
                                    {zone}
                                </ComboboxItem>
                            )}
                        </ComboboxList>
                    </ComboboxContent>
                </Combobox>
                {errors.timezone && <FieldError>{errors.timezone}</FieldError>}
            </Field>
        </>
    );
}

type Preview = { next: string[]; error: string | null };

// useSchedulePreview asks the server when a schedule would next run, as
// it's typed, so a typo shows before it's saved.
function useSchedulePreview(schedule: string, zone: string, active: boolean): Preview | null {
    const [preview, setPreview] = useState<Preview | null>(null);
    useEffect(() => {
        if (!active) {
            return;
        }
        const controller = new AbortController();
        const timer = window.setTimeout(async () => {
            try {
                const query = new URLSearchParams({ schedule, timezone: zone });
                const response = await fetch(`/scheduled-tasks/preview?${query}`, {
                    headers: { Accept: 'application/json' },
                    signal: controller.signal,
                });
                if (response.ok) {
                    setPreview(await response.json());
                }
            } catch {
                // Aborted by the next keystroke, or offline: keep the last one.
            }
        }, 250);
        return () => {
            window.clearTimeout(timer);
            controller.abort();
        };
    }, [schedule, zone, active]);

    return preview;
}

function NextRuns({ preview, zone, custom }: { preview: Preview | null; zone: string; custom: boolean }) {
    if (!preview) {
        return custom ? <FieldDescription>Minute, hour, day of month, month and day of week.</FieldDescription> : null;
    }
    if (preview.error) {
        // Go errors start lower case.
        return <FieldError>{preview.error[0].toUpperCase() + preview.error.slice(1)}.</FieldError>;
    }

    return (
        <div className="rounded-lg border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
            <span className="font-medium text-foreground">Next runs</span> in {zone}:{' '}
            {preview.next.map((t) => formatInZone(t, zone)).join(', ')}
        </div>
    );
}
