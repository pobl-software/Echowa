import { type ReactElement, type ReactNode, useState } from 'react';

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

interface ConfirmDeleteProps {
    // The button that opens the dialog.
    trigger: ReactElement;
    title: string;
    children: ReactNode;
    confirmLabel?: string;
    // Default for an action that only interrupts, such as a restart.
    variant?: 'destructive' | 'default';
    onConfirm: () => void;
}

// A destructive action behind an "are you sure?" dialog.
export function ConfirmDelete({
    trigger,
    title,
    children,
    confirmLabel = 'Delete',
    variant = 'destructive',
    onConfirm,
}: ConfirmDeleteProps) {
    const [open, setOpen] = useState(false);

    return (
        <AlertDialog open={open} onOpenChange={setOpen}>
            <AlertDialogTrigger render={trigger} />
            <AlertDialogContent>
                <AlertDialogHeader>
                    <AlertDialogTitle>{title}</AlertDialogTitle>
                    <AlertDialogDescription>{children}</AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                    <AlertDialogCancel>Cancel</AlertDialogCancel>
                    <AlertDialogAction
                        variant={variant}
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
