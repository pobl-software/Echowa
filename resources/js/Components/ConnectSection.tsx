import { CheckIcon, CopyIcon, EyeIcon, EyeOffIcon, PlugIcon } from 'lucide-react';
import { type ReactNode, useState } from 'react';

import { ConfigSection } from '@/Components/ConfigPanel';
import { Button } from '@/Components/ui/button';
import { toast } from '@/Components/ui/toast';
import { postJson } from '@/lib/http';
import type { ConnectInfo } from '@/types/models';

// What POST /apps/{id}/connect/reveal answers with.
type Secrets = { url: string; password: string };

const masked = '••••••••••••';

// ConnectSection shows how other apps reach a template app (ADR 0025), in
// its Connection section. The page only has the URL's shape, with env keys
// where the values go. Reveal, and copying the password or URL, fetch the
// real ones from the server.
export function ConnectSection({ appId, connect }: { appId: number; connect: ConnectInfo }) {
    // Fetched once, then kept; shown says whether the page shows them.
    const [secrets, setSecrets] = useState<Secrets | null>(null);
    const [shown, setShown] = useState(false);
    const [busy, setBusy] = useState(false);
    const [copied, setCopied] = useState<string | null>(null);

    const fetchSecrets = async () => {
        if (secrets) {
            return secrets;
        }
        setBusy(true);
        try {
            const fetched = await postJson<Secrets>(`/apps/${appId}/connect/reveal`, {});
            setSecrets(fetched);
            return fetched;
        } catch (error) {
            toast.add({ type: 'error', title: 'Couldn’t get the password', description: String(error) });
            return null;
        } finally {
            setBusy(false);
        }
    };

    const toggle = async () => {
        setShown(!shown && (await fetchSecrets()) != null);
    };

    const copy = async (label: string, value: string | null | undefined) => {
        if (value == null) {
            return;
        }
        // Browsers only allow the clipboard over HTTPS or on localhost, and
        // a server is often reached over plain HTTP on port 8192. They can
        // also refuse it outright, so either way the value is shown instead.
        try {
            await navigator.clipboard.writeText(value);
            setCopied(label);
            setTimeout(() => setCopied((current) => (current === label ? null : current)), 2000);
        } catch {
            setShown(true);
            toast.add({
                type: 'info',
                title: 'Copy it from the page',
                description: 'The browser didn’t allow copying, so it’s shown.',
            });
        }
    };
    const revealed = shown ? secrets : null;

    return (
        <ConfigSection icon={PlugIcon} title="Connect" description="How other apps on this server reach it.">
            <dl className="grid grid-cols-[8rem_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 rounded-lg border bg-muted/40 py-2 pr-2 pl-3 text-sm">
                <Row label="Internal Hostname" copied={copied} onCopy={() => copy('Internal Hostname', connect.host)}>
                    {connect.host}
                </Row>
                <Row label="Port">{connect.port}</Row>
                <Row label="Username" copied={copied} onCopy={() => copy('Username', connect.user)}>
                    {connect.user}
                </Row>
                <Row
                    label="Password"
                    copied={copied}
                    busy={busy}
                    onCopy={async () => copy('Password', (await fetchSecrets())?.password)}
                >
                    {revealed ? revealed.password : <span className="text-muted-foreground">{masked}</span>}
                </Row>
                <Row
                    label="URL"
                    copied={copied}
                    busy={busy}
                    onCopy={async () => copy('URL', (await fetchSecrets())?.url)}
                >
                    {revealed?.url ?? connect.preview}
                </Row>
            </dl>
            <div className="flex">
                <Button
                    variant="outline"
                    size="sm"
                    onClick={toggle}
                    disabled={busy}
                    aria-label={shown ? 'Hide password' : 'Reveal password'}
                >
                    {shown ? <EyeOffIcon /> : <EyeIcon />}
                    {shown ? 'Hide' : 'Reveal'}
                </Button>
            </div>
        </ConfigSection>
    );
}

// Row is a label, a value and, for one worth copying, a copy button.
function Row({
    label,
    copied,
    busy,
    onCopy,
    children,
}: {
    label: string;
    copied?: string | null;
    busy?: boolean;
    onCopy?: () => void;
    children: ReactNode;
}) {
    return (
        <>
            <dt className="text-muted-foreground">{label}</dt>
            <dd className="font-mono break-all select-all">{children}</dd>
            <div className="size-6">
                {onCopy && (
                    <Button
                        variant="ghost"
                        size="icon-xs"
                        onClick={onCopy}
                        disabled={busy}
                        aria-label={`Copy ${label.toLowerCase()}`}
                        title={`Copy ${label.toLowerCase()}`}
                    >
                        {copied === label ? <CheckIcon className="text-success" /> : <CopyIcon />}
                    </Button>
                )}
            </div>
        </>
    );
}
