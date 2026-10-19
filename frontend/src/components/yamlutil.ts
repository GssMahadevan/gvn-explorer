// Shared plumbing for the YAML editor components: AST text/comment
// helpers, path resolution, and the leaf-accessor factory that every
// edit surface (tree panel, kv/seq tables, scalar panel) drives. Text
// state always lives in the parent; every mutation goes through edit().
import {
  isAlias,
  isCollection,
  isMap,
  isPair,
  isScalar,
  isSeq,
  Scalar,
  type Document,
  type Node,
  type Pair,
  type YAMLMap,
  type YAMLSeq,
} from 'yaml';
import type { CSSProperties } from 'react';

export const mono: CSSProperties = {
  fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
};

export const serialize = (docs: Document[]) => docs.map((d) => d.toString()).join('');

// Comment slots in the yaml AST carry text after "#", one line per \n.
// These helpers move between that shape and something an operator can read.
export const cmtGet = (raw?: string | null) =>
  (raw ?? '')
    .split('\n')
    .map((l) => l.replace(/^#\s?/, '').trim())
    .join('\n');
export const cmtSet = (v: string): string | undefined => {
  const lines = v
    .split('\n')
    .map((l) => l.replace(/^#\s?/, '').trim())
    .filter(Boolean);
  return lines.length ? ' ' + lines.join('\n ') : undefined;
};

// Scalars keep their original quoting/block style: we only ever replace
// the value (and re-resolve obvious number/bool text for plain scalars).
export const scalarText = (s: Scalar): string =>
  s.source ?? (s.value === null || s.value === undefined ? '' : String(s.value));

export const applyScalarText = (s: Scalar, raw: string) => {
  s.source = undefined;
  if ((s.type ?? '').startsWith('BLOCK')) {
    s.value = raw.endsWith('\n') ? raw : raw + '\n';
    return;
  }
  const implicit =
    s.tag == null || s.tag === '!!int' || s.tag === '!!float' || s.tag === '!!bool' || s.tag === '!!null';
  if (raw === '') s.value = null;
  else if (implicit && /^-?\d+$/.test(raw)) s.value = Number(raw);
  else if (implicit && /^-?(\d+\.\d+|\.\d+)$/.test(raw)) s.value = Number(raw);
  else if (implicit && (raw === 'true' || raw === 'false')) s.value = raw === 'true';
  else s.value = raw;
};

export const isBlockish = (s: Scalar) =>
  (s.type ?? '').startsWith('BLOCK') || (typeof s.value === 'string' && s.value.includes('\n'));

export const lockedLabel = (n: Node): string | null => {
  if (isAlias(n)) return 'alias';
  if ((isScalar(n) || isCollection(n)) && (n as Scalar).anchor) return 'anchor: ' + (n as Scalar).anchor;
  return null;
};

export const short = (s: string, n = 28) => (s.length > n ? s.slice(0, n) + '…' : s);

// one-line label for the structure tree: shows how things are organized
// without opening them.
export const summaryOf = (n: Node | null): string => {
  if (n == null) return '(empty)';
  if (isAlias(n)) return '*' + (((n as unknown) as { source?: string }).source ?? '?');
  if (isMap(n)) return '{' + n.items.length + '}';
  if (isSeq(n)) return '[' + n.items.length + ']';
  if (isScalar(n)) return short(scalarText(n).replace(/\n+/g, ' '));
  return '(?)';
};

export interface Resolved {
  doc: Document;
  node: Node | null;
  parent: YAMLMap | YAMLSeq | null;
  pair: Pair | null;
}

// paths are 'd0' for a document root, 'd0.3.1' for a child index.
export function resolvePath(docs: Document[], path: string): Resolved | null {
  const parts = path.split('.');
  const ds = parts[0] ?? '';
  const rest = parts.slice(1);
  if (!/^d\d+$/.test(ds)) return null;
  const doc = docs[Number(ds.slice(1))];
  if (!doc) return null;
  let node = (doc.contents ?? null) as Node | null;
  let parent: YAMLMap | YAMLSeq | null = null;
  let pair: Pair | null = null;
  for (const seg of rest) {
    const i = Number(seg);
    if (!Number.isInteger(i) || node == null || isAlias(node)) return null;
    if (isMap(node)) {
      const p = node.items[i];
      if (!p || !isPair(p)) return null;
      pair = p;
      parent = node;
      node = (p.value ?? null) as Node | null;
    } else if (isSeq(node)) {
      pair = null;
      parent = node;
      node = (node.items[i] ?? null) as Node | null;
    } else return null;
  }
  return { doc, node, parent, pair };
}

export const ancestorsOf = (path: string) => {
  const parts = path.split('.');
  return parts.slice(0, -1).map((_, i) => parts.slice(0, i + 1).join('.'));
};

// human-readable location string for the detail header: root.env[0].name
export function pathLabel(docs: Document[], path: string): string {
  const parts = path.split('.');
  const ds = parts[0] ?? '';
  const rest = parts.slice(1);
  if (!/^d\d+$/.test(ds)) return path;
  const di = Number(ds.slice(1));
  const doc = docs[di];
  if (!doc) return path;
  let out = docs.length > 1 ? `document ${di + 1}` : 'root';
  let node = (doc.contents ?? null) as Node | null;
  for (const seg of rest) {
    const i = Number(seg);
    if (node == null || isAlias(node)) break;
    if (isMap(node)) {
      const p = node.items[i];
      if (!p || !isPair(p)) break;
      out += '.' + String((p.key as { value?: unknown })?.value ?? '?');
      node = (p.value ?? null) as Node | null;
    } else if (isSeq(node)) {
      out += `[${i}]`;
      node = (node.items[i] ?? null) as Node | null;
    } else break;
  }
  return out;
}

export type NodeKind = 'file' | 'doc' | 'map' | 'seq' | 'scalar';

export const kindOf = (n: Node | null): NodeKind =>
  n == null || isScalar(n) || isAlias(n) ? 'scalar' : isMap(n) ? 'map' : isSeq(n) ? 'seq' : 'scalar';

// One row of the path stack: flat rows from root down to the selected
// node, top item = parent, so deep nesting costs zero horizontal width.
export interface Crumb {
  path: string;
  label: string;
  kind: NodeKind;
  summary: string;
}

export function segmentsOf(docs: Document[], path: string): Crumb[] {
  const parts = path.split('.');
  const ds = parts[0] ?? '';
  if (!/^d\d+$/.test(ds)) return [];
  const di = Number(ds.slice(1));
  const doc = docs[di];
  if (!doc) return [];
  const out: Crumb[] = [];
  if (docs.length > 1) out.push({ path: 'd', label: 'file', kind: 'file', summary: `${docs.length} documents` });
  out.push({
    path: ds,
    label: docs.length > 1 ? `document ${di + 1}` : 'root',
    kind: kindOf(doc.contents as Node),
    summary: summaryOf(doc.contents as Node),
  });
  let node = (doc.contents ?? null) as Node | null;
  let acc = ds;
  for (const seg of parts.slice(1)) {
    const i = Number(seg);
    acc += '.' + seg;
    if (node == null || isAlias(node)) break;
    if (isMap(node)) {
      const p = node.items[i];
      if (!p || !isPair(p)) break;
      out.push({
        path: acc,
        label: String((p.key as { value?: unknown })?.value ?? '?'),
        kind: kindOf(p.value as Node),
        summary: summaryOf(p.value as Node),
      });
      node = (p.value ?? null) as Node | null;
    } else if (isSeq(node)) {
      const item = (node.items[i] ?? null) as Node | null;
      out.push({ path: acc, label: `[${i}]`, kind: kindOf(item), summary: summaryOf(item) });
      node = item;
    } else break;
  }
  return out;
}

// what every detail panel needs: mutate-the-AST-and-reserialize, open
// another node in the detail panel, and whether editing is allowed.
export interface EditorCtx {
  readOnly: boolean;
  edit: (mut: () => void) => void;
  select: (path: string) => void;
}

// Leaf editor accessors for one selected path. "Notes" (the comment
// lines above a line) live on pair.key for scalar-valued pairs and on
// the container/sequence-item node otherwise — matching where the yaml
// parser puts them.
export interface LeafAccess {
  getValue: () => string;
  setValue: (v: string) => void;
  isBlock: () => boolean;
  getNote: () => string;
  setNote: (v: string) => void;
  hasNote: () => boolean;
  getComment: () => string;
  setComment: (v: string) => void;
  hasComment: () => boolean;
  locked: string | null;
}

export function makeLeafAccess(r: Resolved | null, edit: (mut: () => void) => void): LeafAccess | null {
  if (!r) return null;
  const node = r.node;
  const noteOwner: Node | null =
    r.pair && (node == null || isScalar(node)) ? (r.pair.key as Node) : node;
  const cmtHolder: Node | null = node ?? ((r.pair?.key as Node) ?? null);
  if (!noteOwner || !cmtHolder) return null;

  const scalar = node != null && isScalar(node);
  return {
    getValue: () => (scalar ? scalarText(node as Scalar) : ''),
    setValue: (v) =>
      edit(() => {
        if (scalar) applyScalarText(node as Scalar, v);
        else {
          const s = new Scalar(v);
          applyScalarText(s, v);
          if (r.pair) r.pair.value = s;
          else r.doc.contents = s as unknown as NonNullable<typeof r.doc.contents>;
        }
      }),
    isBlock: () => scalar && isBlockish(node as Scalar),
    getNote: () => cmtGet(noteOwner.commentBefore),
    setNote: (v) => edit(() => void (noteOwner.commentBefore = cmtSet(v))),
    hasNote: () => (noteOwner.commentBefore !== undefined && cmtGet(noteOwner.commentBefore) !== '') || false,
    getComment: () => cmtGet((cmtHolder as { comment?: string }).comment),
    setComment: (v) => edit(() => void ((cmtHolder as { comment?: string }).comment = cmtSet(v))),
    hasComment: () => ((cmtHolder as { comment?: string }).comment !== undefined && cmtGet((cmtHolder as { comment?: string }).comment) !== '') || false,
    locked: node ? lockedLabel(node) : null,
  };
}
