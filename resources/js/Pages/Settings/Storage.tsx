import { SettingsPage } from '@/Components/SettingsNav';
import { StorageLocations } from '@/Components/StorageLocations';
import type { StorageLocation } from '@/types/models';

export default function SettingsStorage({
    storageLocations,
    storagePrefix,
}: {
    storageLocations: StorageLocation[];
    storagePrefix: string;
}) {
    return (
        <SettingsPage section="storage">
            <StorageLocations locations={storageLocations} prefix={storagePrefix} />
        </SettingsPage>
    );
}
