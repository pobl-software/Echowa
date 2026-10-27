import { useForm } from '@inertiajs/react';
import { RocketIcon } from 'lucide-react';
import { type FormEvent, useState } from 'react';

import { TextField } from '@/Components/TextField';
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
import { FieldDescription } from '@/Components/ui/field';
import type { App } from '@/types/models';

// Deploys the default tag, or a tag or digest typed in.
export function DeployDialog({ app }: { app: App }) {
    const [open, setOpen] = useState(false);
    const form = useForm({ ref: app.default_tag });

    const submit = (e: FormEvent) => {
        e.preventDefault();
        form.post(`/apps/${app.id}/deploy`, { onSuccess: () => setOpen(false) });
    };

    return (
        <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger render={<Button />}>
                <RocketIcon />
                Deploy
            </DialogTrigger>
            <DialogContent>
                <form onSubmit={submit} className="grid gap-4">
                    <DialogHeader>
                        <DialogTitle>Deploy {app.name}</DialogTitle>
                        <DialogDescription>
                            Pulls <span className="font-mono">{app.repository}</span>, pins the digest it resolves to,
                            and rolls it out with no downtime. Swarm rolls back if the new version fails.
                        </DialogDescription>
                    </DialogHeader>
                    <div>
                        <TextField
                            id="ref"
                            label="Tag or digest"
                            className="font-mono"
                            autoFocus
                            value={form.data.ref}
                            onChange={(e) => form.setData('ref', e.target.value)}
                            error={form.errors.ref}
                        />
                        <FieldDescription className="mt-2">
                            Defaults to <span className="font-mono">{app.default_tag}</span>. A digest looks like{' '}
                            <span className="font-mono">sha256:…</span>
                        </FieldDescription>
                    </div>
                    <DialogFooter>
                        <Button type="submit" disabled={form.processing}>
                            Deploy {form.data.ref || app.default_tag}
                        </Button>
                    </DialogFooter>
                </form>
            </DialogContent>
        </Dialog>
    );
}
