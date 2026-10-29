// RequestError is a failed postJson. errors holds field messages when the
// endpoint validated the body, in the shape useForm's errors have.
export class RequestError extends Error {
    constructor(
        message: string,
        readonly errors: Record<string, string> = {},
    ) {
        super(message);
    }
}

// POSTs JSON outside Inertia (for endpoints that answer with plain JSON),
// sending the CSRF token the way Inertia's own client does (ADR 0008).
export async function postJson<T>(url: string, body: unknown): Promise<T> {
    const response = await fetch(url, {
        method: 'POST',
        credentials: 'same-origin',
        headers: {
            Accept: 'application/json',
            'Content-Type': 'application/json',
            'X-XSRF-TOKEN': xsrfToken(),
        },
        body: JSON.stringify(body),
    });
    const data = await response.json().catch(() => ({}));
    if (!response.ok) {
        throw new RequestError(data.message ?? `Request failed (${response.status})`, data.errors);
    }

    return data as T;
}

// The CSRF token, for a request postJson doesn't make.
export function xsrfToken(): string {
    const match = document.cookie.match(/(?:^|;\s*)XSRF-TOKEN=([^;]*)/);

    return match ? decodeURIComponent(match[1]) : '';
}
