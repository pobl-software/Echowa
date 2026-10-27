import { useEffect, useState } from 'react';

export type Theme = 'system' | 'light' | 'dark';

// Kept in sync with the inline script in resources/views/app.html, which
// applies the theme before first paint so the page doesn't flash.
const key = 'echowa-theme';
const darkQuery = '(prefers-color-scheme: dark)';

function stored(): Theme {
    try {
        const value = localStorage.getItem(key);
        return value === 'light' || value === 'dark' ? value : 'system';
    } catch {
        return 'system';
    }
}

function apply(theme: Theme) {
    const dark = theme === 'dark' || (theme === 'system' && window.matchMedia(darkQuery).matches);
    document.documentElement.classList.toggle('dark', dark);
}

// useTheme is the viewer's light/dark choice. "system" follows the OS.
export function useTheme() {
    const [theme, setTheme] = useState<Theme>(stored);

    useEffect(() => {
        apply(theme);
        if (theme !== 'system') {
            return;
        }
        const query = window.matchMedia(darkQuery);
        const onChange = () => apply('system');
        query.addEventListener('change', onChange);
        return () => query.removeEventListener('change', onChange);
    }, [theme]);

    const choose = (next: Theme) => {
        try {
            if (next === 'system') {
                localStorage.removeItem(key);
            } else {
                localStorage.setItem(key, next);
            }
        } catch {
            // Private windows can refuse storage; the choice still applies now.
        }
        setTheme(next);
    };

    return [theme, choose] as const;
}
