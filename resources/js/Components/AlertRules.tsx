import { Link, router, useForm } from '@inertiajs/react';
import { BellIcon, ExternalLinkIcon, PencilIcon, PlusIcon, Trash2Icon } from 'lucide-react';
import { type FormEvent, type ReactElement, type ReactNode, useState } from 'react';

import { ConfirmDelete } from '@/Components/ConfirmDelete';
import { Panel } from '@/Components/Panel';
import { StatusPill } from '@/Components/StatusBadge';
import { Button, buttonVariants } from '@/Components/ui/button';
import { Checkbox } from '@/Components/ui/checkbox';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
    DialogTrigger,
} from '@/Components/ui/dialog';
import { Field, FieldDescription, FieldError, FieldLabel } from '@/Components/ui/field';
import { InputGroup, InputGroupAddon, InputGroupInput } from '@/Components/ui/input-group';
import {
    Select,
    SelectContent,
    SelectGroup,
    SelectItem,
    SelectLabel,
    SelectTrigger,
    SelectValue,
} from '@/Components/ui/select';
import { Switch } from '@/Components/ui/switch';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/Components/ui/table';
import { notificationKinds } from '@/lib/notifications';
import { formatDateTime, timeAgo } from '@/lib/time';
import { cn } from '@/lib/utils';
import type {
    AlertRule,
    AlertSeverity,
    AlertState,
    AlertTrigger,
    AlertUnit,
    FiringAlert,
    ProviderRef,
    SettingsAlerts,
} from '@/types/models';

const severities: { value: AlertSeverity; label: string; tone: 'neutral' | 'warning' | 'danger' }[] = [
    { value: 'info', label: 'Info', tone: 'neutral' },
    { value: 'warning', label: 'Warning', tone: 'warning' },
    { value: 'critical', label: 'Critical', tone: 'danger' },
];

export function SeverityPill({ severity }: { severity: AlertSeverity }) {
    const s = severities.find((x) => x.value === severity) ?? severities[0];

    return (
        <StatusPill tone={s.tone} plain>
            {s.label}
        </StatusPill>
    );
}

const states: Record<AlertState, { label: string; tone: 'success' | 'warning' | 'danger' | 'neutral' }> = {
    ok: { label: 'OK', tone: 'success' },
    pending: { label: 'Pending', tone: 'warning' },
    firing: { label: 'Firing', tone: 'danger' },
    off: { label: 'Off', tone: 'neutral' },
};

// A condition's state. Nothing for an event, or for an alert that's off,
// which its switch already says.
function StatePill({ state }: { state: AlertRule['state'] }) {
    if (state === '' || state === 'off') {
        return null;
    }
    const s = states[state];

    return (
        <StatusPill tone={s.tone} pulse={state === 'firing'}>
            {s.label}
        </StatusPill>
    );
}

// A condition's threshold unit, as its field shows it.
const unitSuffix: Record<AlertUnit, string> = {
    '': '',
    percent: '%',
    gib: 'GiB',
    minutes: 'minutes',
    count: '',
    per_second: 'per second',
};

// minutesText mirrors alerts.Minutes: "5 minutes", "1 hour 30 minutes".
function minutesText(n: number) {
    const plural = (k: number, unit: string) => (k === 1 ? `1 ${unit}` : `${k} ${unit}s`);
    if (n < 60) {
        return plural(n, 'minute');
    }
    return n % 60 === 0 ? plural(n / 60, 'hour') : `${plural(Math.floor(n / 60), 'hour')} ${plural(n % 60, 'minute')}`;
}

// describe mirrors alerts.Trigger.Describe, for the form's sentence as you
// type: "Memory use above 90% of its limit for 5 minutes".
function describe(t: AlertTrigger, threshold: string, forMinutes: string) {
    const words = [t.label];
    if (t.unit !== '') {
        const n = Number(threshold);
        let value = '…';
        if (threshold.trim() !== '' && Number.isFinite(n)) {
            const text = Number.isInteger(n) ? n.toLocaleString('en-US') : String(n);
            value = t.unit === 'percent' ? `${text}%` : [text, unitSuffix[t.unit]].filter(Boolean).join(' ');
            if (t.unit === 'minutes' && n === 1) {
                value = '1 minute';
            }
        }
        words.push(t.below ? 'below' : 'above', value);
        if (t.of) {
            words.push(t.of);
        }
    }
    const m = Math.floor(Number(forMinutes));
    if (m > 0) {
        words.push('for', minutesText(m));
    }

    return words.join(' ');
}

// What an alert on Settings covers.
function covers(rule: AlertRule, triggers: AlertTrigger[]) {
    if (rule.scope === 'server') {
        return 'The server';
    }
    return triggers.find((t) => t.id === rule.trigger)?.only || 'Every app';
}

function ProviderNames({ providers }: { providers: ProviderRef[] }) {
    if (providers.length === 0) {
        return <span className="text-warning">Nowhere: add a provider</span>;
    }
    return (
        <>
            {providers.map((p, i) => (
                <span key={p.id} className={cn(!p.enabled && 'text-muted-foreground')}>
                    {i > 0 && ', '}
                    {p.name}
                    {!p.enabled && ' (off)'}
                </span>
            ))}
        </>
    );
}

interface ListProps {
    rules: AlertRule[];
    triggers: AlertTrigger[];
    providers: ProviderRef[];
    // Where the rules are edited: /apps/{id}/alerts or /settings/alerts.
    // Without it the list is read-only, with a link to Settings.
    base?: string;
    // Show what each covers, on Settings.
    showCovers?: boolean;
}

// AlertRuleTable lists alerts with their severity, providers and switch.
export function AlertRuleTable({ rules, triggers, providers, base, showCovers }: ListProps) {
    return (
        <Table>
            <TableHeader>
                <TableRow>
                    <TableHead>Alert</TableHead>
                    {showCovers && <TableHead className="hidden lg:table-cell">Covers</TableHead>}
                    <TableHead className="hidden sm:table-cell">State</TableHead>
                    <TableHead className="hidden sm:table-cell">Severity</TableHead>
                    <TableHead className="hidden md:table-cell">Sends to</TableHead>
                    <TableHead>On</TableHead>
                    <TableHead className="text-right">{base ? 'Actions' : ''}</TableHead>
                </TableRow>
            </TableHeader>
            <TableBody>
                {rules.map((rule) => (
                    <TableRow key={rule.id}>
                        {/* Takes the spare width, so the switch stays on a phone's screen. */}
                        <TableCell className="w-full max-w-0">
                            <div className="font-medium break-words">{rule.description}</div>
                            {!rule.known && (
                                <div className="text-xs text-muted-foreground">
                                    From a newer version of Echowa: sends nothing on this one.
                                </div>
                            )}
                            {showCovers && (
                                <div className="truncate text-xs text-muted-foreground lg:hidden">
                                    {covers(rule, triggers)}
                                </div>
                            )}
                            <div className="truncate text-xs text-muted-foreground md:hidden">
                                <span className="sm:hidden">
                                    {rule.state && rule.state !== 'off' && `${states[rule.state].label} · `}
                                    {severities.find((s) => s.value === rule.severity)?.label} ·{' '}
                                </span>
                                <ProviderNames providers={rule.providers} />
                            </div>
                        </TableCell>
                        {showCovers && (
                            <TableCell className="hidden whitespace-nowrap lg:table-cell">
                                {covers(rule, triggers)}
                            </TableCell>
                        )}
                        <TableCell className="hidden sm:table-cell">
                            <StatePill state={rule.state} />
                        </TableCell>
                        <TableCell className="hidden sm:table-cell">
                            <SeverityPill severity={rule.severity} />
                        </TableCell>
                        <TableCell className="hidden max-w-64 truncate md:table-cell">
                            <ProviderNames providers={rule.providers} />
                        </TableCell>
                        <TableCell>
                            {base ? (
                                <Switch
                                    checked={rule.enabled}
                                    aria-label={rule.enabled ? `Turn off ${rule.label}` : `Turn on ${rule.label}`}
                                    title={rule.enabled ? 'Sends' : 'Off: sends nothing'}
                                    onCheckedChange={(enabled) =>
                                        router.put(`${base}/${rule.id}/enabled`, { enabled }, { preserveScroll: true })
                                    }
                                />
                            ) : (
                                <StatusPill tone={rule.enabled ? 'success' : 'neutral'}>
                                    {rule.enabled ? 'On' : 'Off'}
                                </StatusPill>
                            )}
                        </TableCell>
                        <TableCell>
                            <div className="flex justify-end gap-1">
                                {base ? (
                                    <>
                                        <AlertRuleDialog
                                            rule={rule}
                                            base={base}
                                            triggers={triggers}
                                            providers={providers}
                                            trigger={
                                                <Button
                                                    variant="ghost"
                                                    size="icon-sm"
                                                    aria-label={`Edit ${rule.label}`}
                                                    title="Edit"
                                                >
                                                    <PencilIcon />
                                                </Button>
                                            }
                                        />
                                        <ConfirmDelete
                                            title={`Delete ${rule.label}?`}
                                            trigger={
                                                <Button
                                                    variant="ghost"
                                                    size="icon-sm"
                                                    className="text-destructive"
                                                    aria-label={`Delete ${rule.label}`}
                                                    title="Delete"
                                                >
                                                    <Trash2Icon />
                                                </Button>
                                            }
                                            onConfirm={() =>
                                                router.delete(`${base}/${rule.id}`, { preserveScroll: true })
                                            }
                                        >
                                            Nothing more is sent for it. The notifications it sent stay listed.
                                        </ConfirmDelete>
                                    </>
                                ) : (
                                    <Link
                                        href="/settings#alerts"
                                        className={buttonVariants({ variant: 'ghost', size: 'icon-sm' })}
                                        aria-label={`Edit ${rule.label} on Settings`}
                                        title="Edit on Settings"
                                    >
                                        <ExternalLinkIcon />
                                    </Link>
                                )}
                            </div>
                        </TableCell>
                    </TableRow>
                ))}
            </TableBody>
        </Table>
    );
}

// ProviderChoice picks where alerts go.
function ProviderChoice({
    providers,
    value,
    onChange,
}: {
    providers: ProviderRef[];
    value: number[];
    onChange: (value: number[]) => void;
}) {
    return (
        <div className="grid gap-2 sm:grid-cols-2">
            {providers.map((p) => {
                const Icon = notificationKinds[p.kind].icon;
                const checked = value.includes(p.id);
                return (
                    <label
                        key={p.id}
                        className={cn(
                            'flex min-w-0 cursor-pointer items-center gap-2.5 rounded-lg border px-3 py-2 text-sm transition-colors hover:bg-muted/50',
                            checked && 'border-primary bg-primary/5',
                        )}
                    >
                        <Checkbox
                            checked={checked}
                            onCheckedChange={(on) =>
                                onChange(on ? [...value, p.id] : value.filter((id) => id !== p.id))
                            }
                        />
                        <Icon className="size-4 shrink-0" />
                        <span className="truncate">{p.name}</span>
                        {!p.enabled && <span className="ml-auto shrink-0 text-xs text-muted-foreground">Off</span>}
                    </label>
                );
            })}
        </div>
    );
}

// The providers a new form starts with: the only one there is.
function defaultProviders(providers: ProviderRef[]) {
    const on = providers.filter((p) => p.enabled);
    return on.length === 1 ? [on[0].id] : [];
}

interface RuleForm {
    trigger: string;
    severity: AlertSeverity;
    providers: number[];
    // A condition's, as typed.
    threshold: string;
    for_minutes: string;
    repeat_minutes: string;
    notify_resolved: boolean;
}

// AlertRuleDialog adds an alert, or edits one's severity and providers. A
// saved alert's trigger never changes.
export function AlertRuleDialog({
    rule,
    base,
    triggers,
    providers,
    trigger: button,
    settings,
}: {
    rule?: AlertRule;
    base: string;
    triggers: AlertTrigger[];
    providers: ProviderRef[];
    trigger: ReactElement;
    // On Settings, where the trigger decides the server or every app.
    settings?: boolean;
}) {
    const [open, setOpen] = useState(false);
    const initial = (): RuleForm =>
        rule
            ? {
                  trigger: rule.trigger,
                  severity: rule.severity,
                  providers: rule.providers.map((p) => p.id),
                  threshold: rule.threshold === null ? '' : String(rule.threshold),
                  for_minutes: String(rule.for_minutes),
                  repeat_minutes: String(rule.repeat_minutes),
                  notify_resolved: rule.notify_resolved,
              }
            : {
                  trigger: '',
                  severity: 'warning',
                  providers: defaultProviders(providers),
                  threshold: '',
                  for_minutes: '0',
                  repeat_minutes: '0',
                  notify_resolved: true,
              };
    const form = useForm<RuleForm>(initial());
    const chosen = triggers.find((t) => t.id === form.data.trigger);
    const condition = chosen?.kind === 'condition' ? chosen : undefined;
    const groups = [...new Set(triggers.map((t) => t.group))];

    const number = (value: string) => (value.trim() === '' ? null : Number(value));
    form.transform((data) => ({
        ...data,
        threshold: number(data.threshold),
        for_minutes: number(data.for_minutes),
        repeat_minutes: number(data.repeat_minutes),
    }));

    const submit = (e: FormEvent) => {
        e.preventDefault();
        const options = { preserveScroll: true, onSuccess: () => setOpen(false) };
        if (rule) {
            form.put(`${base}/${rule.id}`, options);
        } else {
            form.post(base, options);
        }
    };

    return (
        <Dialog
            open={open}
            onOpenChange={(next) => {
                if (next) {
                    form.setDefaults(initial());
                    form.reset();
                    form.clearErrors();
                }
                setOpen(next);
            }}
        >
            <DialogTrigger render={button} />
            <DialogContent className="max-h-[calc(100dvh-2rem)] grid-rows-[minmax(0,1fr)] sm:max-w-lg">
                <form onSubmit={submit} className="grid min-h-0 grid-rows-[auto_minmax(0,1fr)_auto] gap-4">
                    <DialogHeader>
                        <DialogTitle>{rule ? `Edit ${rule.label}` : 'Add an alert'}</DialogTitle>
                        <DialogDescription>
                            {condition
                                ? 'Echowa checks it every minute, and sends when it fires.'
                                : settings
                                  ? 'An alert about the server, or one that covers every app.'
                                  : 'Echowa sends it as soon as it happens.'}
                        </DialogDescription>
                    </DialogHeader>

                    <div className="-mx-4 grid min-h-0 content-start gap-5 overflow-y-auto px-4 py-0.5">
                        <Field>
                            <FieldLabel htmlFor="alert-trigger">Alert when</FieldLabel>
                            {rule ? (
                                <p className="text-sm font-medium">{rule.label}</p>
                            ) : (
                                <Select
                                    items={triggers.map((t) => ({ value: t.id, label: t.label }))}
                                    value={form.data.trigger || null}
                                    onValueChange={(value) => {
                                        const t = triggers.find((x) => x.id === value);
                                        if (t) {
                                            form.setData((data) => ({
                                                ...data,
                                                trigger: t.id,
                                                severity: t.severity,
                                                threshold: t.unit === '' ? '' : String(t.threshold),
                                                for_minutes: String(t.for_minutes),
                                            }));
                                        }
                                    }}
                                >
                                    <SelectTrigger
                                        id="alert-trigger"
                                        className="w-full"
                                        aria-invalid={!!form.errors.trigger || undefined}
                                    >
                                        <SelectValue placeholder="Choose what to alert on" />
                                    </SelectTrigger>
                                    <SelectContent>
                                        {groups.map((group) => (
                                            <SelectGroup key={group}>
                                                <SelectLabel>{group}</SelectLabel>
                                                {triggers
                                                    .filter((t) => t.group === group)
                                                    .map((t) => (
                                                        <SelectItem key={t.id} value={t.id}>
                                                            {t.label}
                                                        </SelectItem>
                                                    ))}
                                            </SelectGroup>
                                        ))}
                                    </SelectContent>
                                </Select>
                            )}
                            {settings && chosen && (
                                <FieldDescription>
                                    {chosen.server ? 'About the server.' : `Covers ${chosen.only || 'every app'}.`}
                                </FieldDescription>
                            )}
                            {form.errors.trigger && <FieldError>{form.errors.trigger}</FieldError>}
                        </Field>

                        {condition && <ConditionFields trigger={condition} form={form} />}

                        <Field>
                            <FieldLabel>Severity</FieldLabel>
                            <fieldset aria-label="Severity" className="grid grid-cols-3 gap-2">
                                {severities.map((s) => (
                                    <button
                                        key={s.value}
                                        type="button"
                                        aria-pressed={form.data.severity === s.value}
                                        onClick={() => form.setData('severity', s.value)}
                                        className={cn(
                                            'rounded-lg border px-3 py-2 text-sm transition-colors outline-none hover:bg-muted/50 focus-visible:ring-3 focus-visible:ring-ring/50',
                                            form.data.severity === s.value &&
                                                'border-primary bg-primary/5 ring-1 ring-primary',
                                        )}
                                    >
                                        {s.label}
                                    </button>
                                ))}
                            </fieldset>
                            <FieldDescription>
                                How loudly it arrives: ntfy’s priority, Discord’s and Teams’ colour, the mark in Slack
                                and email.
                            </FieldDescription>
                            {form.errors.severity && <FieldError>{form.errors.severity}</FieldError>}
                        </Field>

                        <Field>
                            <FieldLabel>Send to</FieldLabel>
                            <ProviderChoice
                                providers={providers}
                                value={form.data.providers}
                                onChange={(value) => form.setData('providers', value)}
                            />
                            {form.errors.providers && <FieldError>{form.errors.providers}</FieldError>}
                        </Field>
                    </div>

                    <DialogFooter>
                        <Button type="button" variant="outline" onClick={() => setOpen(false)}>
                            Cancel
                        </Button>
                        <Button type="submit" disabled={form.processing}>
                            {rule ? 'Save' : 'Add alert'}
                        </Button>
                    </DialogFooter>
                </form>
            </DialogContent>
        </Dialog>
    );
}

type RuleFormHandle = ReturnType<typeof useForm<RuleForm>>;

// ConditionFields are a condition's: its threshold, how long it must hold,
// how often it repeats and whether it says it's resolved, with the alert
// in words as you type.
function ConditionFields({ trigger, form }: { trigger: AlertTrigger; form: RuleFormHandle }) {
    const forMinutes = Math.max(0, Math.floor(Number(form.data.for_minutes)) || 0);
    const suffix = [unitSuffix[trigger.unit], trigger.of].filter(Boolean).join(' ');

    return (
        <>
            {trigger.unit !== '' && (
                <Field data-invalid={!!form.errors.threshold || undefined}>
                    <FieldLabel htmlFor="alert-threshold">{trigger.below ? 'Below' : 'Above'}</FieldLabel>
                    <InputGroup className="max-w-72">
                        <InputGroupInput
                            id="alert-threshold"
                            type="number"
                            inputMode="decimal"
                            step="any"
                            min={0}
                            max={trigger.unit === 'percent' ? 100 : undefined}
                            className="tabular-nums"
                            value={form.data.threshold}
                            onChange={(e) => form.setData('threshold', e.target.value)}
                            aria-invalid={!!form.errors.threshold || undefined}
                        />
                        {suffix && <InputGroupAddon align="inline-end">{suffix}</InputGroupAddon>}
                    </InputGroup>
                    {form.errors.threshold && <FieldError>{form.errors.threshold}</FieldError>}
                </Field>
            )}

            <div className="grid gap-5 sm:grid-cols-2">
                <Field data-invalid={!!form.errors.for_minutes || undefined}>
                    <FieldLabel htmlFor="alert-for">For</FieldLabel>
                    <MinutesInput
                        id="alert-for"
                        value={form.data.for_minutes}
                        onChange={(value) => form.setData('for_minutes', value)}
                        invalid={!!form.errors.for_minutes}
                    />
                    <FieldDescription>How long before it fires. 0 fires at the first check.</FieldDescription>
                    {form.errors.for_minutes && <FieldError>{form.errors.for_minutes}</FieldError>}
                </Field>
                <Field data-invalid={!!form.errors.repeat_minutes || undefined}>
                    <FieldLabel htmlFor="alert-repeat">Repeat every</FieldLabel>
                    <MinutesInput
                        id="alert-repeat"
                        value={form.data.repeat_minutes}
                        onChange={(value) => form.setData('repeat_minutes', value)}
                        invalid={!!form.errors.repeat_minutes}
                    />
                    <FieldDescription>While it’s still firing. 0 never repeats.</FieldDescription>
                    {form.errors.repeat_minutes && <FieldError>{form.errors.repeat_minutes}</FieldError>}
                </Field>
            </div>

            <Field orientation="horizontal">
                <Switch
                    id="alert-resolved"
                    checked={form.data.notify_resolved}
                    onCheckedChange={(on) => form.setData('notify_resolved', on)}
                />
                <div className="grid gap-1">
                    <FieldLabel htmlFor="alert-resolved">Send when it’s resolved</FieldLabel>
                    <FieldDescription>
                        {forMinutes === 0
                            ? 'At the first check that finds it clear.'
                            : `Once it’s been clear for ${minutesText(Math.min(forMinutes, 5))}.`}
                    </FieldDescription>
                </div>
            </Field>

            <p className="rounded-lg border bg-muted/40 px-3 py-2 text-sm">
                <span className="text-muted-foreground">Fires when </span>
                <span className="font-medium">{describe(trigger, form.data.threshold, form.data.for_minutes)}</span>
            </p>
        </>
    );
}

function MinutesInput({
    id,
    value,
    onChange,
    invalid,
}: {
    id: string;
    value: string;
    onChange: (value: string) => void;
    invalid: boolean;
}) {
    return (
        <InputGroup className="max-w-40">
            <InputGroupInput
                id={id}
                type="number"
                inputMode="numeric"
                min={0}
                step={1}
                className="tabular-nums"
                value={value}
                onChange={(e) => onChange(e.target.value)}
                aria-invalid={invalid || undefined}
            />
            <InputGroupAddon align="inline-end">minutes</InputGroupAddon>
        </InputGroup>
    );
}

// FiringAlerts lists the conditions firing now, the most severe first.
// subjects names what each is about, on Settings.
export function FiringAlerts({ firing, subjects }: { firing: FiringAlert[]; subjects?: boolean }) {
    return (
        <Panel
            flush
            title="Firing"
            description="Past their threshold now. Each sends again only if its alert repeats, and once more when it’s resolved."
        >
            <ul className="divide-y">
                {firing.map((f) => (
                    <li key={`${f.rule_id} ${f.subject}`} className="flex items-start gap-3 px-4 py-3 sm:px-5">
                        <span className="mt-1.5 size-2 shrink-0 animate-pulse rounded-full bg-destructive" />
                        <div className="min-w-0 flex-1 text-sm">
                            <Link href={f.path} className="font-medium hover:underline">
                                {subjects && `${f.subject}: `}
                                {f.description}
                            </Link>
                            <div className="text-xs text-muted-foreground">
                                {f.value && `Now ${f.value}. `}
                                Fired <span title={formatDateTime(f.since)}>{timeAgo(f.since)}</span>.
                            </div>
                        </div>
                        <SeverityPill severity={f.severity} />
                    </li>
                ))}
            </ul>
        </Panel>
    );
}

// SuggestedAlerts offers the alerts that usually matter, to add in one go.
// Give it a key that changes with the suggestions, so its checklist starts
// over after an add.
export function SuggestedAlerts({
    suggestions,
    providers,
    base,
    description,
    settings,
}: {
    suggestions: AlertTrigger[];
    providers: ProviderRef[];
    base: string;
    description: ReactNode;
    settings?: boolean;
}) {
    const form = useForm({ triggers: suggestions.map((s) => s.id), providers: defaultProviders(providers) });
    const count = form.data.triggers.length;

    const submit = (e: FormEvent) => {
        e.preventDefault();
        form.post(`${base}/suggested`, { preserveScroll: true });
    };

    return (
        <Panel title="Suggested alerts" description={description}>
            <form onSubmit={submit} className="space-y-5">
                <ul className="divide-y rounded-lg border">
                    {suggestions.map((s) => {
                        const checked = form.data.triggers.includes(s.id);
                        return (
                            <li key={s.id}>
                                <label className="flex cursor-pointer items-center gap-3 px-3 py-2.5 text-sm">
                                    <Checkbox
                                        checked={checked}
                                        onCheckedChange={(on) =>
                                            form.setData(
                                                'triggers',
                                                on
                                                    ? [...form.data.triggers, s.id]
                                                    : form.data.triggers.filter((id) => id !== s.id),
                                            )
                                        }
                                    />
                                    <span className="min-w-0 flex-1">
                                        <span className="block font-medium">{s.description}</span>
                                        {settings && (
                                            <span className="block truncate text-xs text-muted-foreground">
                                                {s.server ? 'The server' : s.only || 'Every app'}
                                            </span>
                                        )}
                                    </span>
                                    <SeverityPill severity={s.severity} />
                                </label>
                            </li>
                        );
                    })}
                </ul>
                <Field>
                    <FieldLabel>Send them to</FieldLabel>
                    <ProviderChoice
                        providers={providers}
                        value={form.data.providers}
                        onChange={(value) => form.setData('providers', value)}
                    />
                    {form.errors.providers && <FieldError>{form.errors.providers}</FieldError>}
                    {form.errors.triggers && <FieldError>{form.errors.triggers}</FieldError>}
                </Field>
                <Button type="submit" disabled={count === 0 || form.data.providers.length === 0 || form.processing}>
                    <PlusIcon />
                    {count === 1 ? 'Add 1 alert' : `Add ${count} alerts`}
                </Button>
            </form>
        </Panel>
    );
}

export function AddAlertButton(props: Omit<Parameters<typeof AlertRuleDialog>[0], 'trigger' | 'rule'>) {
    return (
        <AlertRuleDialog
            {...props}
            trigger={
                <Button variant="outline">
                    <PlusIcon />
                    Add alert
                </Button>
            }
        />
    );
}

export function NoAlerts({ children, action }: { children: ReactNode; action?: ReactNode }) {
    return (
        <div className="flex flex-col items-center rounded-xl border border-dashed px-6 py-10 text-center">
            <span className="mb-4 flex size-11 items-center justify-center rounded-xl border bg-card">
                <BellIcon className="size-5 text-muted-foreground" />
            </span>
            <p className="max-w-md text-sm text-muted-foreground">{children}</p>
            {action && <div className="mt-5">{action}</div>}
        </div>
    );
}

// SettingsAlertRules is Settings' alerts: the server's and every app's.
export function SettingsAlertRules({ alerts, providers }: { alerts: SettingsAlerts; providers: ProviderRef[] }) {
    const base = '/settings/alerts';
    const ready = providers.length > 0;

    return (
        <div id="alerts" className="space-y-6">
            {alerts.firing.length > 0 && <FiringAlerts firing={alerts.firing} subjects />}
            {ready && alerts.suggestions.length > 0 && (
                <SuggestedAlerts
                    key={alerts.suggestions.map((s) => s.id).join()}
                    suggestions={alerts.suggestions}
                    providers={providers}
                    base={base}
                    settings
                    description="With these on, every app is covered, including ones you add later. An app can still have alerts of its own."
                />
            )}
            <Panel
                flush={alerts.rules.length > 0}
                title="Alerts"
                description="Alerts about the server, and alerts that cover every app. An app’s own alerts are in its Alerts section."
                actions={
                    ready &&
                    alerts.rules.length > 0 && (
                        <AddAlertButton base={base} triggers={alerts.triggers} providers={providers} settings />
                    )
                }
            >
                {alerts.rules.length > 0 ? (
                    <AlertRuleTable
                        rules={alerts.rules}
                        triggers={alerts.triggers}
                        providers={providers}
                        base={base}
                        showCovers
                    />
                ) : (
                    <NoAlerts
                        action={
                            ready && (
                                <AddAlertButton base={base} triggers={alerts.triggers} providers={providers} settings />
                            )
                        }
                    >
                        {ready
                            ? 'No server or every-app alerts yet.'
                            : 'Add a notification provider above first: an alert needs somewhere to go.'}
                    </NoAlerts>
                )}
            </Panel>
        </div>
    );
}
