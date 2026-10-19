// Which action a file row should offer, decided from the name alone so the
// table works before any content is fetched (same policy GitHub uses):
//   'yaml'   -> structured YAML view; edit mode runs the draft/publish flow
//   'text'   -> file page: read-only viewer, editing in CodeMirror (FilePane)
//   'download' -> everything else (binaries, unknown types): stream via /fs/raw
// Extension-less files (README, Makefile, LICENSE) count as text; if the
// server still sniffs them as binary (NUL bytes), the file page falls
// back to a download prompt — is_binary is the safety net either way.

const TEXT_EXT = new Set([
  'md', 'markdown', 'txt', 'text', 'rtf', 'json', 'csv', 'tsv', 'log',
  'ini', 'conf', 'cfg', 'properties', 'toml', 'env',
  'sh', 'bash', 'zsh', 'py', 'go', 'rs', 'c', 'h', 'js', 'mjs', 'cjs',
  'ts', 'tsx', 'jsx', 'css', 'scss', 'less', 'html', 'htm', 'xml',
  'sql', 'service', 'manifest', 'tpl', 'template',
]);

export type FileKind = 'yaml' | 'text' | 'download';

export function fileKind(name: string): FileKind {
  const base = name.toLowerCase();
  const dot = base.lastIndexOf('.');
  if (dot <= 0) return 'text';
  const ext = base.slice(dot + 1);
  if (ext === 'yml' || ext === 'yaml') return 'yaml';
  return TEXT_EXT.has(ext) ? 'text' : 'download';
}

export const rawUrl = (p: string) => '/api/v1/fs/raw?path=' + encodeURIComponent(p);
