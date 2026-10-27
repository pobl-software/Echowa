// Props shared with every page by HandleInertiaRequests.Share().
export interface SharedProps {
    appName: string;
    auth: {
        user: AuthUser | null;
    };
    // Why Docker can't be managed right now, or null. Signed-in pages only.
    dockerProblem: string | null;
    [key: string]: unknown;
}

// middleware.AuthUser in Go.
export interface AuthUser {
    id: number;
    name: string;
    email: string;
}

export type PageProps<T extends Record<string, unknown> = Record<string, unknown>> = T & SharedProps;

// Flash data set with inertia.Flash(ctx, key, value) in Go. These four keys
// are shown as toasts by resources/js/lib/flash.ts.
export interface FlashData {
    success?: string;
    error?: string;
    info?: string;
    warning?: string;
}

declare module '@inertiajs/core' {
    interface InertiaConfig {
        flashDataType: FlashData;
        sharedPageProps: SharedProps;
    }
}
