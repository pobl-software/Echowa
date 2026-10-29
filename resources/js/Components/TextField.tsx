import type { ComponentProps, ReactNode } from 'react';

import { Field, FieldDescription, FieldError, FieldLabel } from '@/Components/ui/field';
import { Input } from '@/Components/ui/input';

interface TextFieldProps extends ComponentProps<typeof Input> {
    id: string;
    label: string;
    // Validation message, e.g. form.errors.email from useForm.
    error?: string;
    // Shown at the right of the label, e.g. a "Forgot password?" link.
    labelAction?: ReactNode;
    // Shown under the input: what the field does or what follows from it.
    description?: ReactNode;
}

// A labelled input with its validation error: the building block for forms.
export function TextField({ id, label, error, labelAction, description, ...props }: TextFieldProps) {
    const describedBy = [error && `${id}-error`, description && `${id}-description`].filter(Boolean).join(' ');

    return (
        <Field data-invalid={!!error || undefined}>
            <div className="flex items-center justify-between gap-2">
                <FieldLabel htmlFor={id}>{label}</FieldLabel>
                {labelAction}
            </div>
            <Input
                id={id}
                name={id}
                aria-invalid={!!error || undefined}
                aria-describedby={describedBy || undefined}
                {...props}
            />
            {error && <FieldError id={`${id}-error`}>{error}</FieldError>}
            {description && <FieldDescription id={`${id}-description`}>{description}</FieldDescription>}
        </Field>
    );
}
