import { NotificationProviders } from '@/Components/NotificationProviders';
import { SettingsPage } from '@/Components/SettingsNav';
import type { NotificationProvider } from '@/types/models';

export default function SettingsNotifications({
    notificationProviders,
}: {
    notificationProviders: NotificationProvider[];
}) {
    return (
        <SettingsPage section="notifications">
            <NotificationProviders providers={notificationProviders} />
        </SettingsPage>
    );
}
