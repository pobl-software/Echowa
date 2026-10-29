import { Link, router, useForm } from '@inertiajs/react';
import { CalendarClockIcon, PencilIcon, PlayIcon, PlusIcon, Trash2Icon, TriangleAlertIcon, XIcon } from 'lucide-react';
import { type FormEvent, type ReactNode, useEffect, useState } from 'react';

import { ConfirmDelete } from '@/Components/ConfirmDelete';
import { Panel } from '@/Components/Panel';
import { browserZone, type Preset, presetLabel, ScheduleFields, ScheduleText } from '@/Components/ScheduleFields';
import { RunStatusBadge } from '@/Components/StatusBadge';
import { TextField } from '@/Components/TextField';
import { Alert, AlertAction, AlertDescription, AlertTitle } from '@/Components/ui/alert';
import { Button } from '@/Components/ui/button';
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
import { InputGroup, InputGroupAddon, InputGroupInput, InputGroupText } from '@/Components/ui/input-group';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/Components/ui/sheet';
import { Switch } from '@/Components/ui/switch';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/Components/ui/table';
import { Textarea } from '@/Components/ui/textarea';
import { duration, formatDateTime, formatInZone, timeAgo } from '@/lib/time';
import { cn } from '@/lib/utils';
import type { App, ScheduledTask, TaskFailure, TaskRun, TaskRunDetail } from '@/types/models';

// The schedules the form offers by name.
const presets: Preset[] = [
    { value: '* * * * *', label: 'Every minute' },
    { value: '0 * * * *', label: 'Every hour' },
    { value: '0 0 * * *', label: 'Every day at midnight' },
    { value: '0 0 * * 0', label: 'Every Sunday at midnight' },
    { value: '0 0 1 * *', label: 'The 1st of every month' },
];

const noSpinner =
    '[appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none';

// ScheduledTasks is a custom app's Scheduled tasks section (ADR 0032): the
// commands Echowa runs in its container on a schedule. A row opens its runs.
export function ScheduledTasks({ app, tasks }: { app: App; tasks: ScheduledTask[] }) {
    // ?task= and ?run= open a task's runs, as the Overview's alert links.
    const [linked] = useState(() => new URLSearchParams(window.location.search));
    const [open, setOpen] = useState<number | null>(() => Number(linked.get('task')) || null);
    const opened = tasks.find((t) => t.id === open) ?? null;
    const close = () => {
        setOpen(null);
        const url = new URL(window.location.href);
        url.searchParams.delete('task');
        url.searchParams.delete('run');
        window.history.replaceState(window.history.state, '', url);
    };

    // Runs start on the minute, so look again every 30s, and every 3s while
    // one is going. Overview's failures come along, so they're current when
    // you switch to it.
    const running = tasks.some((t) => t.last_run?.status === 'running');
    useEffect(() => {
        const id = window.setInterval(
            () => router.reload({ only: ['scheduledTasks', 'taskFailures'] }),
            running ? 3000 : 30_000,
        );
        return () => window.clearInterval(id);
    }, [running]);

    if (tasks.length === 0) {
        return (
            <div className="flex flex-col items-center rounded-xl border border-dashed px-6 py-14 text-center">
                <span className="mb-4 flex size-11 items-center justify-center rounded-xl border bg-card">
                    <CalendarClockIcon className="size-5 text-muted-foreground" />
                </span>
                <h2 className="font-medium">No scheduled tasks</h2>
                <p className="mt-1 max-w-md text-sm text-muted-foreground">
                    Run a command in {app.name}&apos;s container on a schedule, such as{' '}
                    <code className="font-mono text-xs">php artisan schedule:run</code> every minute for Laravel&apos;s
                    scheduler.
                </p>
                <div className="mt-5">
                    <TaskDialog app={app} trigger={<NewTaskButton />} />
                </div>
            </div>
        );
    }

    return (
        <>
            <Panel
                title="Scheduled tasks"
                description={`Commands run in ${app.name}'s newest running container, with its env. Open one to see its runs.`}
                actions={<TaskDialog app={app} trigger={<NewTaskButton />} />}
                flush
            >
                <Table>
                    <TableHeader>
                        <TableRow>
                            <TableHead>Task</TableHead>
                            <TableHead className="hidden md:table-cell">Schedule</TableHead>
                            <TableHead className="hidden xl:table-cell">Next run</TableHead>
                            <TableHead className="hidden sm:table-cell">Last run</TableHead>
                            <TableHead>
                                <span className="sr-only">Enabled</span>
                            </TableHead>
                            <TableHead>
                                <span className="sr-only">Actions</span>
                            </TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {tasks.map((task) => (
                            <TaskRow key={task.id} app={app} task={task} onOpen={() => setOpen(task.id)} />
                        ))}
                    </TableBody>
                </Table>
            </Panel>
            {opened && (
                <TaskRunsSheet
                    app={app}
                    task={opened}
                    open={open !== null}
                    onOpenChange={close}
                    initialRun={opened.id === Number(linked.get('task')) ? Number(linked.get('run')) || null : null}
                />
            )}
        </>
    );
}

// ScheduledTaskAlert is the Overview's alert for a task whose newest failure
// hasn't been dismissed. Dismissing names the run, so a failure that came
// after the page loaded still shows.
export function ScheduledTaskAlert({ app, failure }: { app: App; failure: TaskFailure }) {
    const { run } = failure;

    return (
        <Alert variant="destructive">
            <TriangleAlertIcon />
            <AlertTitle>
                {failure.task_name} {run.status === 'timed_out' ? 'timed out' : 'failed'}{' '}
                <span title={formatDateTime(run.started_at)}>{timeAgo(run.started_at)}</span>
            </AlertTitle>
            <AlertDescription className="flex flex-col items-start gap-2 [&_p:not(:last-child)]:mb-0">
                <p>
                    {run.message ?? `It exited with code ${run.exit_code}.`}
                    {failure.succeeded_at && ` It has succeeded since, ${timeAgo(failure.succeeded_at)}.`}
                </p>
                {failure.tail && (
                    <pre className="w-full rounded-md border bg-muted/40 px-2.5 py-1.5 font-mono text-xs whitespace-pre-wrap break-all text-foreground">
                        {failure.tail}
                    </pre>
                )}
                <Link href={`/apps/${app.id}?tab=tasks&task=${failure.task_id}&run=${run.id}`}>See its runs</Link>
            </AlertDescription>
            <AlertAction>
                <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={`Dismiss ${failure.task_name}'s failure`}
                    title="Dismiss"
                    onClick={() =>
                        router.post(
                            `/apps/${app.id}/tasks/${failure.task_id}/dismiss`,
                            { run: run.id },
                            { preserveScroll: true, preserveState: true },
                        )
                    }
                >
                    <XIcon />
                </Button>
            </AlertAction>
        </Alert>
    );
}

function NewTaskButton(props: React.ComponentProps<typeof Button>) {
    return (
        <Button {...props}>
            <PlusIcon />
            Add task
        </Button>
    );
}

function TaskRow({ app, task, onOpen }: { app: App; task: ScheduledTask; onOpen: () => void }) {
    const last = task.last_run;
    const base = `/apps/${app.id}/tasks/${task.id}`;

    return (
        <TableRow className="cursor-pointer" onClick={onOpen}>
            {/* The name gives way on narrow screens, so the actions stay in view. */}
            <TableCell className="w-full max-w-0">
                <div className="truncate font-medium">{task.name}</div>
                <div className="truncate font-mono text-xs text-muted-foreground">{task.command}</div>
                {/* Phones have no Schedule column, and the smallest no Last run either. */}
                <div className="truncate text-xs text-muted-foreground md:hidden">
                    <ScheduleText presets={presets} schedule={task.schedule} />
                </div>
                {last && (
                    <div className="mt-1 sm:hidden">
                        <RunStatusBadge status={last.status} />
                    </div>
                )}
            </TableCell>
            <TableCell className="hidden md:table-cell">
                <div className="text-sm">
                    <ScheduleText presets={presets} schedule={task.schedule} />
                </div>
                <div className="text-xs text-muted-foreground">{task.timezone}</div>
            </TableCell>
            <TableCell className="hidden text-sm xl:table-cell">
                {task.next_run_at ? (
                    <span title={formatInZone(task.next_run_at, task.timezone)}>{timeAgo(task.next_run_at)}</span>
                ) : (
                    <span className="text-muted-foreground">Off</span>
                )}
            </TableCell>
            <TableCell className="hidden sm:table-cell">
                {last ? (
                    <div className="flex flex-col items-start gap-0.5">
                        <RunStatusBadge status={last.status} />
                        <span
                            className="hidden text-xs text-muted-foreground sm:block"
                            title={formatDateTime(last.started_at)}
                        >
                            {timeAgo(last.started_at)}
                        </span>
                    </div>
                ) : (
                    <span className="text-sm text-muted-foreground">Never</span>
                )}
            </TableCell>
            {/* Clicks in dialogs bubble here through their portals; keep them from opening the row. */}
            <TableCell onClick={(e) => e.stopPropagation()}>
                <Switch
                    checked={task.enabled}
                    aria-label={task.enabled ? `Turn off ${task.name}'s schedule` : `Turn on ${task.name}'s schedule`}
                    title={task.enabled ? 'On schedule' : 'Off: runs only by hand'}
                    onCheckedChange={(enabled) => router.put(`${base}/enabled`, { enabled }, { preserveScroll: true })}
                />
            </TableCell>
            <TableCell className="py-1 text-right" onClick={(e) => e.stopPropagation()}>
                <div className="flex justify-end gap-1">
                    <Button
                        variant="ghost"
                        size="sm"
                        aria-label={`Run ${task.name} now`}
                        title="Run now"
                        disabled={last?.status === 'running'}
                        onClick={() => router.post(`${base}/run`, {}, { preserveScroll: true })}
                    >
                        <PlayIcon />
                        <span className="hidden lg:inline">Run now</span>
                    </Button>
                    <TaskDialog
                        app={app}
                        task={task}
                        trigger={
                            <Button variant="ghost" size="icon-sm" aria-label={`Edit ${task.name}`} title="Edit">
                                <PencilIcon />
                            </Button>
                        }
                    />
                    <ConfirmDelete
                        title={`Delete ${task.name}?`}
                        trigger={
                            <Button variant="ghost" size="icon-sm" aria-label={`Delete ${task.name}`} title="Delete">
                                <Trash2Icon />
                            </Button>
                        }
                        onConfirm={() => router.delete(base, { preserveScroll: true })}
                    >
                        The task and its runs go. A run that&apos;s going now carries on in the container until it ends.
                    </ConfirmDelete>
                </div>
            </TableCell>
        </TableRow>
    );
}

type TaskForm = {
    name: string;
    schedule: string;
    timezone: string;
    command: string;
    timeout_minutes: number | string;
    enabled: boolean;
};

// TaskDialog adds a task, or edits one when given it.
function TaskDialog({ app, task, trigger }: { app: App; task?: ScheduledTask; trigger: ReactNode }) {
    const [open, setOpen] = useState(false);
    const defaults: TaskForm = task
        ? {
              name: task.name,
              schedule: task.schedule,
              timezone: task.timezone,
              command: task.command,
              timeout_minutes: task.timeout_minutes,
              enabled: task.enabled,
          }
        : {
              name: '',
              schedule: presets[0].value,
              timezone: browserZone,
              command: '',
              timeout_minutes: 15,
              enabled: true,
          };
    const form = useForm<TaskForm>(defaults);
    const [custom, setCustom] = useState(() => !presetLabel(presets, defaults.schedule));
    const timeout = Number(form.data.timeout_minutes) || 0;

    const reset = () => {
        form.setDefaults(defaults);
        form.reset();
        form.clearErrors();
        setCustom(!presetLabel(presets, defaults.schedule));
    };
    const submit = (e: FormEvent) => {
        e.preventDefault();
        const options = { preserveScroll: true, onSuccess: () => setOpen(false) };
        if (task) {
            form.put(`/apps/${app.id}/tasks/${task.id}`, options);
        } else {
            form.post(`/apps/${app.id}/tasks`, options);
        }
    };

    return (
        <Dialog
            open={open}
            onOpenChange={(next) => {
                if (next) {
                    reset();
                }
                setOpen(next);
            }}
        >
            <DialogTrigger render={trigger as React.ReactElement} />
            <DialogContent className="max-h-[calc(100dvh-2rem)] grid-rows-[minmax(0,1fr)] sm:max-w-lg">
                <form onSubmit={submit} className="grid min-h-0 grid-rows-[auto_minmax(0,1fr)_auto] gap-4">
                    <DialogHeader>
                        <DialogTitle>{task ? `Edit ${task.name}` : `Add a scheduled task to ${app.name}`}</DialogTitle>
                        <DialogDescription>
                            Echowa runs the command in {app.name}&apos;s newest running container when it&apos;s due.
                        </DialogDescription>
                    </DialogHeader>

                    <div className="-mx-4 grid min-h-0 content-start gap-5 overflow-y-auto px-4 py-0.5">
                        <TextField
                            id="name"
                            label="Name"
                            placeholder="Laravel scheduler"
                            value={form.data.name}
                            onChange={(e) => form.setData('name', e.target.value)}
                            error={form.errors.name}
                        />

                        <ScheduleFields
                            presets={presets}
                            schedule={form.data.schedule}
                            timezone={form.data.timezone}
                            onScheduleChange={(schedule) => form.setData('schedule', schedule)}
                            onTimezoneChange={(timezone) => form.setData('timezone', timezone)}
                            custom={custom}
                            onCustomChange={setCustom}
                            errors={form.errors}
                            active={open}
                        />

                        <Field data-invalid={!!form.errors.command || undefined}>
                            <FieldLabel htmlFor="command">Command</FieldLabel>
                            <Textarea
                                id="command"
                                rows={2}
                                className="font-mono"
                                placeholder="php artisan schedule:run"
                                value={form.data.command}
                                onChange={(e) => form.setData('command', e.target.value)}
                                aria-invalid={!!form.errors.command || undefined}
                            />
                            {form.errors.command && <FieldError>{form.errors.command}</FieldError>}
                            <FieldDescription>
                                Run with <code>sh -c</code>, as the app&apos;s user, in its working directory, with its
                                env. It shares the app&apos;s CPU and memory limits.
                            </FieldDescription>
                        </Field>

                        <Field data-invalid={!!form.errors.timeout_minutes || undefined}>
                            <FieldLabel htmlFor="timeout_minutes">Timeout</FieldLabel>
                            <InputGroup className="max-w-36">
                                <InputGroupInput
                                    id="timeout_minutes"
                                    type="number"
                                    min={1}
                                    max={1440}
                                    className={cn('tabular-nums', noSpinner)}
                                    value={form.data.timeout_minutes}
                                    onChange={(e) => form.setData('timeout_minutes', e.target.value)}
                                    aria-invalid={!!form.errors.timeout_minutes || undefined}
                                />
                                <InputGroupAddon align="inline-end">
                                    <InputGroupText>min</InputGroupText>
                                </InputGroupAddon>
                            </InputGroup>
                            {form.errors.timeout_minutes && <FieldError>{form.errors.timeout_minutes}</FieldError>}
                            <FieldDescription>
                                Echowa stops waiting after this, but can&apos;t stop the command itself. Until then the
                                task doesn&apos;t run again
                                {form.data.schedule === '* * * * *' && timeout > 1
                                    ? `, so a stuck run can cost up to ${timeout - 1} of its every-minute runs.`
                                    : '.'}
                            </FieldDescription>
                        </Field>

                        <Field orientation="horizontal">
                            <Switch
                                id="enabled"
                                checked={form.data.enabled}
                                onCheckedChange={(enabled) => form.setData('enabled', enabled)}
                            />
                            <FieldLabel htmlFor="enabled" className="font-normal">
                                Run on its schedule. Off, it runs only when you start it.
                            </FieldLabel>
                        </Field>
                    </div>

                    <DialogFooter>
                        <Button type="submit" disabled={form.processing}>
                            {task ? 'Save' : 'Add task'}
                        </Button>
                    </DialogFooter>
                </form>
            </DialogContent>
        </Dialog>
    );
}

// TaskRunsSheet lists a task's newest runs, with the chosen one's output.
function TaskRunsSheet({
    app,
    task,
    open,
    onOpenChange,
    initialRun,
}: {
    app: App;
    task: ScheduledTask;
    open: boolean;
    onOpenChange: (open: boolean) => void;
    initialRun: number | null;
}) {
    const base = `/apps/${app.id}/tasks/${task.id}`;
    const [runs, setRuns] = useState<TaskRun[] | null>(null);
    const [selected, setSelected] = useState<number | null>(initialRun);
    const [detail, setDetail] = useState<TaskRunDetail | null>(null);
    const running = runs?.some((r) => r.status === 'running') ?? false;
    // A new run shows up as the last run on the list behind; fetch again then.
    const lastRunId = task.last_run?.id;

    // biome-ignore lint/correctness/useExhaustiveDependencies: lastRunId changes when a run starts, which is when the list needs fetching
    useEffect(() => {
        if (!open) {
            return;
        }
        let cancelled = false;
        const load = async () => {
            const response = await fetch(`${base}/runs`, { headers: { Accept: 'application/json' } });
            if (response.ok && !cancelled) {
                const body: { runs: TaskRun[] } = await response.json();
                setRuns(body.runs);
                setSelected((current) => current ?? body.runs[0]?.id ?? null);
            }
        };
        load();
        const id = running ? window.setInterval(load, 3000) : undefined;
        return () => {
            cancelled = true;
            window.clearInterval(id);
        };
    }, [base, open, running, lastRunId]);

    const selectedRun = runs?.find((r) => r.id === selected);
    const selectedStatus = selectedRun?.status;
    // biome-ignore lint/correctness/useExhaustiveDependencies: selectedStatus changes when the run ends, which is when its output is complete
    useEffect(() => {
        if (selected === null) {
            setDetail(null);
            return;
        }
        let cancelled = false;
        fetch(`${base}/runs/${selected}`, { headers: { Accept: 'application/json' } })
            .then((response) => (response.ok ? response.json() : null))
            .then((body: TaskRunDetail | null) => {
                if (!cancelled) {
                    setDetail(body);
                }
            });
        return () => {
            cancelled = true;
        };
    }, [base, selected, selectedStatus]);

    return (
        <Sheet open={open} onOpenChange={onOpenChange}>
            <SheetContent className="w-full gap-0 data-[side=right]:sm:max-w-2xl">
                <SheetHeader className="border-b">
                    <SheetTitle>{task.name}</SheetTitle>
                    <SheetDescription>
                        <ScheduleText presets={presets} schedule={task.schedule} /> in {task.timezone}
                        {!task.enabled && ', off'}
                    </SheetDescription>
                    <div className="mt-2 flex items-start gap-2">
                        <code className="min-w-0 flex-1 rounded-md border bg-muted/40 px-2.5 py-1.5 font-mono text-xs break-all">
                            {task.command}
                        </code>
                        <Button
                            size="sm"
                            variant="outline"
                            disabled={running}
                            onClick={() => {
                                // Show the new run once the list has it.
                                setSelected(null);
                                router.post(`${base}/run`, {}, { preserveScroll: true, preserveState: true });
                            }}
                        >
                            <PlayIcon />
                            Run now
                        </Button>
                    </div>
                </SheetHeader>

                <div className="grid min-h-0 flex-1 grid-rows-[minmax(0,2fr)_minmax(0,3fr)]">
                    <div className="overflow-y-auto border-b">
                        {runs === null ? (
                            <p className="p-4 text-sm text-muted-foreground">Loading runs…</p>
                        ) : runs.length === 0 ? (
                            <p className="p-4 text-sm text-muted-foreground">No runs yet.</p>
                        ) : (
                            <ul>
                                {runs.map((run) => (
                                    <li key={run.id}>
                                        <button
                                            type="button"
                                            onClick={() => setSelected(run.id)}
                                            className={cn(
                                                'flex w-full items-center gap-3 border-b px-4 py-2 text-left text-sm hover:bg-muted/50',
                                                run.id === selected && 'bg-muted',
                                            )}
                                        >
                                            <RunStatusBadge status={run.status} />
                                            <span
                                                className="min-w-0 flex-1 truncate"
                                                title={formatDateTime(run.started_at)}
                                            >
                                                {timeAgo(run.started_at)}
                                                <span className="text-muted-foreground">
                                                    {run.trigger === 'manual' ? ', by hand' : ''}
                                                </span>
                                            </span>
                                            <span className="text-xs text-muted-foreground tabular-nums">
                                                {run.status === 'running'
                                                    ? duration(run.started_at)
                                                    : duration(run.started_at, run.finished_at)}
                                            </span>
                                            <span className="w-14 text-right font-mono text-xs text-muted-foreground">
                                                {run.exit_code !== null ? `exit ${run.exit_code}` : ''}
                                            </span>
                                        </button>
                                    </li>
                                ))}
                            </ul>
                        )}
                    </div>
                    <div className="flex min-h-0 flex-col gap-2 p-4">
                        {detail?.message && <p className="text-sm text-muted-foreground">{detail.message}</p>}
                        {detail && (
                            <pre className="min-h-0 flex-1 overflow-auto rounded-lg border bg-muted/40 p-3 font-mono text-xs whitespace-pre-wrap break-all">
                                {detail.output || (detail.status === 'running' ? 'Running…' : 'No output.')}
                            </pre>
                        )}
                    </div>
                </div>
            </SheetContent>
        </Sheet>
    );
}
