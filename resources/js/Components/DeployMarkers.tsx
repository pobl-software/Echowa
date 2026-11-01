import { useId, useMemo, useState } from 'react';

import { shortRef } from '@/Components/DeploymentsTable';
import type { ChartMarker } from '@/Components/MetricChart';
import { Label } from '@/Components/ui/label';
import { Switch } from '@/Components/ui/switch';
import type { ChartDeploy } from '@/types/metrics';

const key = 'echowa-chart-deploys';

function stored(): boolean {
    try {
        return localStorage.getItem(key) === 'on';
    } catch {
        return false;
    }
}

// useDeployMarkers is whether this browser shows deploys on an app's
// charts, off until the viewer turns it on, and the markers when it does.
export function useDeployMarkers(appId: number, deploys: ChartDeploy[]) {
    const [on, setOn] = useState(stored);

    const markers = useMemo<ChartMarker[] | undefined>(
        () =>
            on
                ? deploys.map((d) => ({
                      at: d.at,
                      label: `#${d.id} ${shortRef(d.ref)} · ${d.trigger}`,
                      href: `/apps/${appId}/deployments/${d.id}`,
                  }))
                : undefined,
        [on, appId, deploys],
    );

    const toggle = (next: boolean) => {
        try {
            if (next) {
                localStorage.setItem(key, 'on');
            } else {
                localStorage.removeItem(key);
            }
        } catch {
            // Private windows can refuse storage; the choice still applies now.
        }
        setOn(next);
    };

    return { on, toggle, markers };
}

// A control the height of the range tabs, with the markers' own pennant,
// so it reads as part of the chart controls.
export function DeploysSwitch({ on, toggle }: { on: boolean; toggle: (next: boolean) => void }) {
    const id = useId();
    return (
        <div
            className="flex h-8 items-center gap-2.5 rounded-lg border bg-card pr-2.5 transition-colors has-data-checked:border-primary/40 has-data-checked:bg-primary/5"
            title="Mark each deploy on the charts"
        >
            <Label htmlFor={id} className="h-full cursor-pointer gap-2 pl-2.5">
                <svg width="10" height="7" viewBox="0 0 10 7" className="shrink-0" aria-hidden="true">
                    <path d="M1 0.5h8l-4 6z" fill="currentColor" fillOpacity={0.7} />
                </svg>
                Show deploys
            </Label>
            <Switch id={id} checked={on} onCheckedChange={toggle} />
        </div>
    );
}
