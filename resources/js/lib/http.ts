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
        throw new Error(data.message ?? `Request failed (${response.status})`);
    }

    return data as T;
}

function xsrfToken(): string {
    const match = document.cookie.match(/(?:^|;\s*)XSRF-TOKEN=([^;]*)/);

    return match ? decodeURIComponent(match[1]) : '';
}
