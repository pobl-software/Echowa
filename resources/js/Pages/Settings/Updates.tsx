import { SettingsPage } from '@/Components/SettingsNav';
import { UpdatesPanel } from '@/Components/UpdatesPanel';
import type { UpdateSettings } from '@/types/models';

export default function SettingsUpdates({ update }: { update: UpdateSettings }) {
    return (
        <SettingsPage section="updates">
            <UpdatesPanel update={update} />
        </SettingsPage>
    );
}
