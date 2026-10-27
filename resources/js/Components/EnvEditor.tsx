import { useForm } from '@inertiajs/react';
import { EyeIcon, PlusIcon, Trash2Icon } from 'lucide-react';
import { type FormEvent, useState } from 'react';

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
import { FieldError } from '@/Components/ui/field';
import { Input } from '@/Components/ui/input';
import { Textarea } from '@/Components/ui/textarea';
import { toast } from '@/Components/ui/toast';
import { postJson } from '@/lib/http';

// One editor row, sent as services.EnvEdit. value null means "unchanged":
// the browser never has stored values unless one is revealed.
interface Row {
    key: string;
    original_key: string | null;
    value: string | null;
}

const rowsFrom = (keys: string[]): Row[] => keys.map((key) => ({ key, original_key: key, value: null }));

export function EnvEditor({ appId, envKeys }: { appId: number; envKeys: string[] }) {
    const form = useForm({ vars: rowsFrom(envKeys) });
    const [revealing, setRevealing] = useState<number | null>(null);
    const errors = form.errors as Record<string, string | undefined>;

    const update = (index: number, patch: Partial<Row>) =>
        form.setData(
            'vars',
            form.data.vars.map((row, i) => (i === index ? { ...row, ...patch } : row)),
        );

    const reveal = async (index: number) => {
        const row = form.data.vars[index];
        if (!row.original_key) {
            return;
        }
        setRevealing(index);
        try {
            const { value } = await postJson<{ value: string }>(`/apps/${appId}/env/reveal`, { key: row.original_key });
            update(index, { value });
        } catch (error) {
            toast.add({ type: 'error', title: 'Couldn’t reveal the value', description: String(error) });
        } finally {
            setRevealing(null);
        }
    };

    const save = (e: FormEvent) => {
        e.preventDefault();
        form.put(`/apps/${appId}/env`, {
            preserveScroll: true,
            onSuccess: (page) => {
                const vars = rowsFrom((page.props as { envKeys?: string[] }).envKeys ?? []);
                form.setDefaults({ vars });
                form.setData('vars', vars);
            },
        });
    };

    return (
        <form onSubmit={save} className="space-y-4">
            {form.data.vars.length === 0 ? (
                <p className="text-sm text-muted-foreground">No variables yet. Add one, or paste a .env file.</p>
            ) : (
                <div className="space-y-2">
                    <div className="grid grid-cols-[minmax(0,2fr)_minmax(0,3fr)_auto] gap-2 text-xs font-medium text-muted-foreground">
                        <span>Key</span>
                        <span>Value</span>
                        <span className="w-16" />
                    </div>
                    {form.data.vars.map((row, index) => (
                        <div
                            // biome-ignore lint/suspicious/noArrayIndexKey: rows are fully controlled and the server's errors name them by index (vars.2.key)
                            key={index}
                            className="grid grid-cols-[minmax(0,2fr)_minmax(0,3fr)_auto] items-start gap-2"
                        >
                            <div>
                                <Input
                                    aria-label="Key"
                                    className="font-mono"
                                    value={row.key}
                                    onChange={(e) => update(index, { key: e.target.value })}
                                    aria-invalid={!!errors[`vars.${index}.key`] || undefined}
                                />
                                {errors[`vars.${index}.key`] && <FieldError>{errors[`vars.${index}.key`]}</FieldError>}
                            </div>
                            <div>
                                {row.value === null ? (
                                    <Input
                                        aria-label="Value (hidden)"
                                        className="font-mono"
                                        value="••••••••"
                                        disabled
                                        readOnly
                                    />
                                ) : (
                                    <Input
                                        aria-label="Value"
                                        className="font-mono"
                                        value={row.value}
                                        autoComplete="off"
                                        onChange={(e) => update(index, { value: e.target.value })}
                                        aria-invalid={!!errors[`vars.${index}.value`] || undefined}
                                    />
                                )}
                                {errors[`vars.${index}.value`] && (
                                    <FieldError>{errors[`vars.${index}.value`]}</FieldError>
                                )}
                            </div>
                            <div className="flex w-16 justify-end gap-1">
                                {row.value === null && (
                                    <Button
                                        type="button"
                                        variant="ghost"
                                        size="icon"
                                        title="Reveal and edit"
                                        aria-label={`Reveal ${row.key}`}
                                        disabled={revealing !== null}
                                        onClick={() => reveal(index)}
                                    >
                                        <EyeIcon />
                                    </Button>
                                )}
                                <Button
                                    type="button"
                                    variant="ghost"
                                    size="icon"
                                    title="Remove"
                                    aria-label={`Remove ${row.key || 'row'}`}
                                    onClick={() =>
                                        form.setData(
                                            'vars',
                                            form.data.vars.filter((_, i) => i !== index),
                                        )
                                    }
                                >
                                    <Trash2Icon />
                                </Button>
                            </div>
                        </div>
                    ))}
                </div>
            )}
            {errors.vars && <FieldError>{errors.vars}</FieldError>}

            <div className="flex flex-wrap gap-2">
                <Button
                    type="button"
                    variant="outline"
                    onClick={() =>
                        form.setData('vars', [...form.data.vars, { key: '', original_key: null, value: '' }])
                    }
                >
                    <PlusIcon />
                    Add variable
                </Button>
                <PasteDotEnv appId={appId} />
                <Button type="submit" disabled={form.processing || !form.isDirty} className="ml-auto">
                    Save environment
                </Button>
            </div>
            <p className="text-xs text-muted-foreground">
                Values are stored encrypted. Changes apply on the next deploy.
            </p>
        </form>
    );
}

// Bulk paste: merges KEY=value lines into the saved environment straight
// away, overwriting keys that already exist.
function PasteDotEnv({ appId }: { appId: number }) {
    const [open, setOpen] = useState(false);
    const form = useForm({ text: '' });

    const submit = () =>
        form.post(`/apps/${appId}/env/import`, {
            preserveScroll: true,
            onSuccess: () => {
                form.reset();
                setOpen(false);
            },
        });

    return (
        <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger render={<Button type="button" variant="outline" />}>Paste .env</DialogTrigger>
            <DialogContent className="sm:max-w-lg">
                <DialogHeader>
                    <DialogTitle>Paste a .env file</DialogTitle>
                    <DialogDescription>
                        Each KEY=value line is saved now. Keys that already exist are overwritten; others are kept.
                        Comments, <code>export</code> and quoted values are understood.
                    </DialogDescription>
                </DialogHeader>
                <Textarea
                    aria-label=".env contents"
                    className="h-48 font-mono text-xs"
                    placeholder={'DATABASE_URL=postgres://...\nSECRET_KEY="..."'}
                    value={form.data.text}
                    onChange={(e) => form.setData('text', e.target.value)}
                    aria-invalid={!!form.errors.text || undefined}
                />
                {form.errors.text && <FieldError>{form.errors.text}</FieldError>}
                <DialogFooter>
                    <Button type="button" onClick={submit} disabled={form.processing || form.data.text.trim() === ''}>
                        Import
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
