// The rail's dot on Alarms says this while some are unread.
export function unreadNote(count: number): string | null {
    if (count <= 0) {
        return null;
    }

    return count === 1 ? '1 unread alarm' : `${count} unread alarms`;
}
