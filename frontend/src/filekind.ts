// Which action a file row should offer, decided from the name alone so the
// table works before any content is fetched (same policy GitHub uses):
//   'yaml'   -> structured YAML view; edit mode runs the draft/publish flow
//   'text'   -> file page: read-only viewer, editing in CodeMirror (FilePane)
//   'image'  -> file page: previewed with a plain <img src=/fs/raw>
//   'video'  -> file page: played with the browser's native <video controls>
//              (the same controls YouTube shows; /fs/raw already answers
//              Range requests, so seeking streams only the needed bytes)
//   'audio'  -> file page: played with the browser's native <audio controls>
//   'pdf'    -> file page: shown in the browser's own pdf viewer via an
//              iframe on /fs/raw?dispo=inline (the server honours that
//              param only for its own explicit allowlist, which mirrors
//              the sets below)
//   'download' -> everything else (binaries, unknown types): stream via /fs/raw
// Only formats browsers actually decode natively count as image/video/
// audio/pdf — mkv/avi/tiff/svg/html/docx/zip stay downloads rather than
// dead players or script-executing surprises. Extension-less
// files (README, Makefile, LICENSE) count as text; if the server still
// sniffs them as binary (NUL bytes), the file page falls back to a
// download prompt — is_binary is the safety net either way.

const TEXT_EXT = new Set([
  'md', 'markdown', 'txt', 'text', 'rtf', 'json', 'csv', 'tsv', 'log',
  'ini', 'conf', 'cfg', 'properties', 'toml', 'env',
  'sh', 'bash', 'zsh', 'py', 'go', 'rs', 'c', 'h', 'js', 'mjs', 'cjs',
  'ts', 'tsx', 'jsx', 'css', 'scss', 'less', 'html', 'htm', 'xml',
  'sql', 'service', 'manifest', 'tpl', 'template',
]);

const IMAGE_EXT = new Set([
  'png', 'jpg', 'jpeg', 'gif', 'webp', 'avif', 'bmp', 'ico', 'svg',
]);

// formats Chrome, Safari and Android all decode natively (h264/aac in the
// mp4/mov containers, vp8/9 in webm, theora in ogv)
const VIDEO_EXT = new Set(['mp4', 'm4v', 'webm', 'mov', 'ogv']);

// same rule as video: every mainstream browser plays these with zero
// extensions (mp3/aac/m4a, wav, opus/ogg, flac)
const AUDIO_EXT = new Set(['mp3', 'm4a', 'aac', 'wav', 'ogg', 'oga', 'opus', 'flac']);

// every mainstream browser ships a pdf viewer (Chrome's, Safari's,
// Firefox's pdf.js, Android's Chrome); plain WebView embeds fall back to
// the pane's open-in-tab/download buttons instead of showing a dead frame
const PDF_EXT = new Set(['pdf']);

export type FileKind = 'yaml' | 'text' | 'image' | 'video' | 'audio' | 'pdf' | 'download';

export function fileKind(name: string): FileKind {
  const base = name.toLowerCase();
  const dot = base.lastIndexOf('.');
  if (dot <= 0) return 'text';
  const ext = base.slice(dot + 1);
  if (ext === 'yml' || ext === 'yaml') return 'yaml';
  if (IMAGE_EXT.has(ext)) return 'image';
  if (VIDEO_EXT.has(ext)) return 'video';
  if (AUDIO_EXT.has(ext)) return 'audio';
  if (PDF_EXT.has(ext)) return 'pdf';
  return TEXT_EXT.has(ext) ? 'text' : 'download';
}

// text and yaml can be edited in the pane; media can only be viewed —
// (used by the row menu to decide whether to offer "edit")
export const isEditable = (kind: FileKind) => kind === 'yaml' || kind === 'text';

export const rawUrl = (p: string) => '/api/v1/fs/raw?path=' + encodeURIComponent(p);

// the same stream asking to be shown inline instead of saved — the server
// only honours this for its own allowlist mirroring the sets above, so
// call it only for pdf/video/audio/image kinds
export const rawInlineUrl = (p: string) => rawUrl(p) + '&dispo=inline';
