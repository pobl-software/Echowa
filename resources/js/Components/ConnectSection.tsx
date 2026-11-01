import { EyeIcon, EyeOffIcon, PlugIcon } from 'lucide-react';
import { type ReactNode, useState } from 'react';

import { ConfigSection } from '@/Components/ConfigPanel';
import { CopyButton } from '@/Components/CopyButton';
import { Button } from '@/Components/ui/button';
import { toast } from '@/Components/ui/toast';
import { postJson } from '@/lib/http';
import type { ConnectInfo } from '@/types/models';

// What POST /apps/{id}/connect/reveal answers with. pooled_url is the URL
// through the connection pooler, while pooling is on (ADR 0035).
export type Secrets = { url: string; password: string; pooled_url: string | null };

export interface ConnectSecrets {
    // Fetches the secrets the first time, then returns the same ones.
    get: () => Promise<Secrets | null>;
    busy: boolean;
}

// useConnectSecrets fetches the app's secrets once, for every section of
// the Connection tab. Turning pooling on or off fetches them again, since
// pooled_url changes.
export function useConnectSecrets(appId: number, pooling: boolean): ConnectSecrets {
    const [cached, setCached] = useState<{ pooling: boolean; secrets: Secrets } | null>(null);
    const [busy, setBusy] = useState(false);

    const get = async () => {
        if (cached?.pooling === pooling) {
            return cached.secrets;
        }
        setBusy(true);
        try {
            const fetched = await postJson<Secrets>(`/apps/${appId}/connect/reveal`, {});
            setCached({ pooling, secrets: fetched });
            return fetched;
        } catch (error) {
            toast.add({ type: 'error', title: 'Couldn’t get the password', description: String(error) });
            return null;
        } finally {
            setBusy(false);
        }
    };

    return { get, busy };
}

const masked = '••••••••••••';

// ConnectSection shows how other apps reach a template app (ADR 0025), in
// its Connection section.
export function ConnectSection({ connect, secrets }: { connect: ConnectInfo; secrets: ConnectSecrets }) {
    return (
        <ConfigSection icon={PlugIcon} title="Connect" description="How other apps on this server reach it.">
            <ConnectDetails connect={connect} secrets={secrets} url={(s) => s.url} />
        </ConfigSection>
    );
}

// ConnectDetails lists a host, port, user, password and URL. The page only
// has the URL's shape, with env keys where the values go. Reveal, and
// copying the password or URL, fetch the real ones from the server.
export function ConnectDetails({
    connect,
    secrets,
    url,
}: {
    connect: ConnectInfo;
    secrets: ConnectSecrets;
    url: (secrets: Secrets) => string | null;
}) {
    const [shown, setShown] = useState<Secrets | null>(null);

    const toggle = async () => {
        setShown(shown ? null : await secrets.get());
    };

    const reveal = async () => {
        setShown(await secrets.get());
    };
    const fetched = async () => {
        const got = await secrets.get();
        return got && { password: got.password, url: url(got) };
    };

    return (
        <>
            <dl className="grid grid-cols-[8rem_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 rounded-lg border bg-muted/40 py-2 pr-2 pl-3 text-sm">
                <Row label="Internal Hostname" copy={connect.host}>
                    {connect.host}
                </Row>
                <Row label="Port">{connect.port}</Row>
                <Row label="Username" copy={connect.user}>
                    {connect.user}
                </Row>
                <Row
                    label="Password"
                    busy={secrets.busy}
                    copy={async () => (await fetched())?.password}
                    onRefused={reveal}
                >
                    {shown ? shown.password : <span className="text-muted-foreground">{masked}</span>}
                </Row>
                <Row label="URL" busy={secrets.busy} copy={async () => (await fetched())?.url} onRefused={reveal}>
                    {(shown && url(shown)) ?? connect.preview}
                </Row>
            </dl>
            <div className="flex">
                <Button
                    variant="outline"
                    size="sm"
                    onClick={toggle}
                    disabled={secrets.busy}
                    aria-label={shown ? 'Hide password' : 'Reveal password'}
                >
                    {shown ? <EyeOffIcon /> : <EyeIcon />}
                    {shown ? 'Hide' : 'Reveal'}
                </Button>
            </div>
        </>
    );
}

// Row is a label, a value and, for one worth copying, a copy button.
function Row({
    label,
    busy,
    copy,
    onRefused,
    children,
}: {
    label: string;
    busy?: boolean;
    copy?: string | (() => Promise<string | null | undefined>);
    onRefused?: () => Promise<void>;
    children: ReactNode;
}) {
    return (
        <>
            <dt className="text-muted-foreground">{label}</dt>
            <dd className="font-mono break-all select-all">{children}</dd>
            <div className="size-6">
                {copy != null && (
                    <CopyButton label={label.toLowerCase()} value={copy} disabled={busy} onRefused={onRefused} />
                )}
            </div>
        </>
    );
}
