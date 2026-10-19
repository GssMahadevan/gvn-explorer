// Thin fetch layer over the JSON API — the same contract the curl
// scripts and Go tests use. JSON-only bodies; multipart upload is the
// single exception (the browser sets its own boundary content-type).

export interface ListState {
  limit: number;
  page: number; // 1-based; converted to offset= for the wire
  q: string;
  col: string;
  sort: string;
  dir: 'asc' | 'desc';
}

export function qs(s: ListState): string {
  const p = new URLSearchParams();
  p.set('limit', String(s.limit));
  p.set('offset', String((s.page - 1) * s.limit));
  p.set('sort', s.sort);
  p.set('dir', s.dir);
  if (s.q) {
    p.set('q', s.q);
    if (s.col) p.set('col', s.col);
  }
  return p.toString();
}

export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
  ) {
    super(message);
  }
}

async function toError(r: Response): Promise<ApiError> {
  const body = (await r.json().catch(() => ({}))) as { error?: { message?: string } };
  return new ApiError(body?.error?.message || `${r.status} ${r.statusText}`, r.status);
}

export async function api<T>(path: string, opts: RequestInit = {}): Promise<T> {
  const r = await fetch(path, {
    headers: { 'Content-Type': 'application/json' },
    ...opts,
  });
  if (r.status === 204) return undefined as T;
  if (!r.ok) throw await toError(r);
  return (await r.json()) as T;
}

// List fetch: X-Total-Count carries the filtered row total, so the
// pager knows the last page even though one page of rows is sent.
export async function apiList<T>(
  path: string,
): Promise<{ data: T; total: number }> {
  const r = await fetch(path, { headers: { 'Content-Type': 'application/json' } });
  if (!r.ok) throw await toError(r);
  const total = parseInt(r.headers.get('X-Total-Count') ?? '', 10);
  return { data: (await r.json()) as T, total: Number.isFinite(total) ? total : 0 };
}

export const fmtSize = (n: number): string =>
  n >= 1e6 ? (n / 1e6).toFixed(1) + ' MB' : n >= 1e3 ? (n / 1e3).toFixed(1) + ' kB' : `${n} B`;

export const bits = (p: { read: boolean; write: boolean; delete: boolean }): string =>
  (p.read ? 'r' : '-') + (p.write ? 'w' : '-') + (p.delete ? 'd' : '-');
