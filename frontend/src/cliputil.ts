// Client-side mirror of the server's virtual clipboard tree rules
// (internal/handlers/clip.go): a tenant's feeds live at
// /tenants/{T}/clipboards/{feed}/{id}-{name}, rendered from the DB on
// every listing. No fs write verb can ever touch that subtree, so the
// GUI renders it read-only exactly like the embedded /help tree.
//
// The name and mime rules below mirror the server's (clipNameRe and
// internal/clip.MimeKinds) so the form offers only what the API
// accepts; the server still enforces both.

import { fileKind } from './filekind';

export const isClipsPath = (p: string): boolean =>
  /^\/tenants\/[^/]+\/clipboards(\/|$)/.test(p);

// allowed feed kinds, in the order the server documents them
export const CLIP_MIMES: { value: string; label: string }[] = [
  { value: '*/*', label: 'any type (mixed feed)' },
  { value: 'text/plain', label: 'text files' },
  { value: 'image/*', label: 'images' },
  { value: 'video/*', label: 'videos' },
  { value: 'audio/*', label: 'audio' },
  { value: 'application/pdf', label: 'PDFs' },
];

// suggest a feed kind from the file being shared — a shared pdf opens
// a "text files" feed, so guessing saves one wrong choice
export function guessClipMime(path: string): string {
  switch (fileKind(path)) {
    case 'text':
    case 'yaml':
      return 'text/plain';
    case 'image':
      return 'image/*';
    case 'video':
      return 'video/*';
    case 'audio':
      return 'audio/*';
    case 'pdf':
      return 'application/pdf';
    default:
      return '*/*';
  }
}

// Copy text into the system clipboard with graceful fallback: the async
// Clipboard API only exists in secure contexts (https, or http on
// localhost), while the legacy execCommand path still works for plain
// http on a LAN address. False means: this browser will not copy at all
// — the caller should show the text itself so it can be taken manually.
export async function copyText(s: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(s);
    return true;
  } catch {
    /* fall through to the legacy path */
  }
  try {
    const ta = document.createElement('textarea');
    ta.value = s;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    const done = document.execCommand('copy');
    ta.remove();
    return done;
  } catch {
    return false;
  }
}

// Copy an image's bytes into the system clipboard. Browsers accept only
// a few image types there (png is the one all of them take); anything
// else rejects — the caller then falls back to copying the link.
export async function copyImageFrom(rawHref: string): Promise<boolean> {
  try {
    const r = await fetch(rawHref);
    if (!r.ok) return false;
    const blob = await r.blob();
    await navigator.clipboard.write([new ClipboardItem({ [blob.type || 'image/png']: blob })]);
    return true;
  } catch {
    return false;
  }
}
