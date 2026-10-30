import { type ReactElement, type ReactNode, useEffect, useId, useState } from 'react';

import {
    AlertDialog,
    AlertDialogAction,
    AlertDialogCancel,
    AlertDialogContent,
    AlertDialogDescription,
    AlertDialogFooter,
    AlertDialogHeader,
    AlertDialogTitle,
    AlertDialogTrigger,
} from '@/Components/ui/alert-dialog';
import { Field, FieldLabel } from '@/Components/ui/field';
import { Input } from '@/Components/ui/input';

interface ConfirmDeleteProps {
    // The button that opens the dialog. Without one, open and onOpenChange
    // control it, as from a menu item.
    trigger?: ReactElement;
    open?: boolean;
    onOpenChange?: (open: boolean) => void;
    title: string;
    children: ReactNode;
    confirmLabel?: string;
    // Default for an action that only interrupts, such as a restart.
    variant?: 'destructive' | 'default';
    // Text to type before the action is allowed, for data that can't come
    // back.
    confirmText?: string;
    onConfirm: () => void;
}

// A destructive action behind an "are you sure?" dialog.
export function ConfirmDelete({
    trigger,
    title,
    children,
    confirmLabel = 'Delete',
    variant = 'destructive',
    confirmText,
    onConfirm,
    ...controlled
}: ConfirmDeleteProps) {
    const [own, setOwn] = useState(false);
    const open = controlled.open ?? own;
    const setOpen = controlled.onOpenChange ?? setOwn;
    const [typed, setTyped] = useState('');
    const inputId = useId();

    useEffect(() => {
        if (!open) {
            setTyped('');
        }
    }, [open]);

    return (
        <AlertDialog open={open} onOpenChange={setOpen}>
            {trigger && <AlertDialogTrigger render={trigger} />}
            <AlertDialogContent>
                <AlertDialogHeader>
                    <AlertDialogTitle>{title}</AlertDialogTitle>
                    <AlertDialogDescription>{children}</AlertDialogDescription>
                </AlertDialogHeader>
                {confirmText !== undefined && (
                    <Field>
                        <FieldLabel htmlFor={inputId}>
                            Type <span className="font-mono">{confirmText}</span> to confirm
                        </FieldLabel>
                        <Input
                            id={inputId}
                            className="font-mono"
                            autoComplete="off"
                            value={typed}
                            onChange={(e) => setTyped(e.target.value)}
                        />
                    </Field>
                )}
                <AlertDialogFooter>
                    <AlertDialogCancel>Cancel</AlertDialogCancel>
                    <AlertDialogAction
                        variant={variant}
                        disabled={confirmText !== undefined && typed !== confirmText}
                        onClick={() => {
                            setOpen(false);
                            onConfirm();
                        }}
                    >
                        {confirmLabel}
                    </AlertDialogAction>
                </AlertDialogFooter>
            </AlertDialogContent>
        </AlertDialog>
    );
}
