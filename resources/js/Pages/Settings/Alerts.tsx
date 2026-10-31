import { SettingsAlertRules } from '@/Components/AlertRules';
import { SettingsPage } from '@/Components/SettingsNav';
import type { ProviderRef, SettingsAlerts } from '@/types/models';

export default function SettingsAlertsPage({
    alerts,
    providers,
}: {
    alerts: SettingsAlerts;
    providers: ProviderRef[];
}) {
    return (
        <SettingsPage section="alerts">
            <SettingsAlertRules alerts={alerts} providers={providers} />
        </SettingsPage>
    );
}
