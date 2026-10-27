import { router } from '@inertiajs/react';
import { RotateCcwIcon } from 'lucide-react';
import { useState } from 'react';

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
import { Button } from '@/Components/ui/button';
import type { DeploymentSummary } from '@/types/models';

const shortDigest = (digest: string) => digest.replace(/^.*sha256:/, '').slice(0, 12);

// RedeployButton queues a deploy of the exact image a succeeded deployment
// ran (a rollback). compact is for table rows: smaller, and icon only until
// the screen is wide enough for every column.
export function RedeployButton({
    appId,
    deployment,
    compact,
}: {
    appId: number;
    deployment: DeploymentSummary;
    compact?: boolean;
}) {
    const [sending, setSending] = useState(false);
    if (deployment.status !== 'succeeded' || !deployment.digest) {
        return null;
    }

    const redeploy = () =>
        router.post(
            `/apps/${appId}/deployments/${deployment.id}/redeploy`,
            {},
            { onStart: () => setSending(true), onFinish: () => setSending(false) },
        );

    return (
        <AlertDialog>
            <AlertDialogTrigger
                render={
                    <Button
                        variant={compact ? 'ghost' : 'outline'}
                        size={compact ? 'sm' : 'default'}
                        disabled={sending}
                        aria-label={`Redeploy #${deployment.id}`}
                        title="Redeploy this version"
                    >
                        <RotateCcwIcon />
                        <span className={compact ? 'hidden xl:inline' : undefined}>Redeploy</span>
                    </Button>
                }
            />
            <AlertDialogContent>
                <AlertDialogHeader>
                    <AlertDialogTitle>Redeploy #{deployment.id}?</AlertDialogTitle>
                    <AlertDialogDescription>
                        Rolls the app to the exact image #{deployment.id} ran,{' '}
                        <code className="font-mono text-foreground">{shortDigest(deployment.digest)}</code>, with its
                        current settings and environment. It's a normal deploy: Swarm starts the old version next to the
                        running one, and keeps the running one if it fails.
                    </AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                    <AlertDialogCancel>Cancel</AlertDialogCancel>
                    <AlertDialogAction onClick={redeploy}>Redeploy this version</AlertDialogAction>
                </AlertDialogFooter>
            </AlertDialogContent>
        </AlertDialog>
    );
}
