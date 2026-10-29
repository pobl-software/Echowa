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
    ...controlled
}: ConfirmDeleteProps) {
    const [own, setOwn] = useState(false);
    const open = controlled.open ?? own;
    const setOpen = controlled.onOpenChange ?? setOwn;

    return (
        <AlertDialog open={open} onOpenChange={setOpen}>
            {trigger && <AlertDialogTrigger render={trigger} />}
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
