import { useSyncExternalStore } from 'react';

// useMediaQuery is whether query matches now, updated as it changes. It's
// read synchronously, so the first render already has the right answer.
export function useMediaQuery(query: string): boolean {
    return useSyncExternalStore(
        (onChange) => {
            const list = window.matchMedia(query);
            list.addEventListener('change', onChange);
            return () => list.removeEventListener('change', onChange);
        },
        () => window.matchMedia(query).matches,
    );
}
