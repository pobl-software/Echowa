import { CheckIcon, CopyIcon } from 'lucide-react';
import { useState } from 'react';

import { Button } from '@/Components/ui/button';
import { toast } from '@/Components/ui/toast';

// CopyButton copies a value, then shows a tick for two seconds. Browsers
// only allow the clipboard over HTTPS or on localhost, and a server is
// often reached over plain HTTP on port 8192. They can also refuse it
// outright, so the value must be on the page to copy by hand: onRefused
// shows it, when it isn't already.
export function CopyButton({
    label,
    value,
    disabled,
    onRefused,
}: {
    // What's copied, as in "Copy password".
    label: string;
    // A value fetched when it's copied may be null, and then nothing is.
    value: string | (() => Promise<string | null | undefined>);
    disabled?: boolean;
    onRefused?: () => Promise<void>;
}) {
    const [copied, setCopied] = useState(false);

    const copy = async () => {
        const text = typeof value === 'string' ? value : await value();
        if (text == null) {
            return;
        }
        try {
            await navigator.clipboard.writeText(text);
            setCopied(true);
            setTimeout(() => setCopied(false), 2000);
        } catch {
            await onRefused?.();
            toast.add({
                type: 'info',
                title: 'Copy it from the page',
                description: onRefused
                    ? 'The browser didn’t allow copying, so it’s shown.'
                    : 'The browser didn’t allow copying.',
            });
        }
    };

    return (
        <Button
            variant="ghost"
            size="icon-xs"
            onClick={copy}
            disabled={disabled}
            aria-label={`Copy ${label}`}
            title={`Copy ${label}`}
        >
            {copied ? <CheckIcon className="text-success" /> : <CopyIcon />}
        </Button>
    );
}
