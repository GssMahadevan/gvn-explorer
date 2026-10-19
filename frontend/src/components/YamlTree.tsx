import { useMemo, useState, type CSSProperties, type ReactNode } from 'react';
import { Alert, Button, Input, Tag, Tooltip, Typography } from 'antd';
import {
  ArrowDownOutlined,
  ArrowUpOutlined,
  MinusCircleOutlined,
  PlusCircleOutlined,
  PlusOutlined,
} from '@ant-design/icons';
import {
  isAlias,
  isCollection,
  isMap,
  isPair,
  isScalar,
  isSeq,
  Pair,
  parseAllDocuments,
  Scalar,
  type Document,
  type Node,
  type YAMLMap,
  type YAMLSeq,
} from 'yaml';

// Structured editor for YAML config files: mappings become key/value/
// comment rows, sequences become indexed rows, sections collapse, block
// scalars get a multiline editor inside the node. Raw text is never
// hand-edited — every change mutates the parsed AST and re-serializes,
// which is what keeps comments and quoting styles intact (the whole
// reason these files stay YAML rather than JSON). Anchors, aliases and
// complex keys are edit-locked in this version; the text is preserved,
// just not edited through the tree.

const mono: CSSProperties = { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace' };

const serialize = (docs: Document[]) => docs.map((d) => d.toString()).join('');

// Comment slots in the yaml AST carry text after "#", one line per \n.
// These helpers move between that shape and something an operator can read.
const cmtGet = (raw?: string | null) =>
  (raw ?? '')
    .split('\n')
    .map((l) => l.replace(/^#\s?/, '').trim())
    .join('\n');
const cmtSet = (v: string): string | undefined => {
  const lines = v
    .split('\n')
    .map((l) => l.replace(/^#\s?/, '').trim())
    .filter(Boolean);
  return lines.length ? ' ' + lines.join('\n ') : undefined;
};

// Scalars keep their original quoting/block style: we only ever replace
// the value (and re-resolve obvious number/bool text for plain scalars).
const scalarText = (s: Scalar): string =>
  s.source ?? (s.value === null || s.value === undefined ? '' : String(s.value));

const applyScalarText = (s: Scalar, raw: string) => {
  s.source = undefined;
  if ((s.type ?? '').startsWith('BLOCK')) {
    s.value = raw.endsWith('\n') ? raw : raw + '\n';
    return;
  }
  const implicit = s.tag == null || s.tag === '!!int' || s.tag === '!!float' || s.tag === '!!bool' || s.tag === '!!null';
  if (raw === '') s.value = null;
  else if (implicit && /^-?\d+$/.test(raw)) s.value = Number(raw);
  else if (implicit && /^-?(\d+\.\d+|\.\d+)$/.test(raw)) s.value = Number(raw);
  else if (implicit && (raw === 'true' || raw === 'false')) s.value = raw === 'true';
  else s.value = raw;
};

const isBlockish = (s: Scalar) =>
  (s.type ?? '').startsWith('BLOCK') || (typeof s.value === 'string' && s.value.includes('\n'));

const locked = (n: Node): string | null => {
  if (isAlias(n)) return 'alias';
  if (isScalar(n) || isCollection(n)) {
    const a = (n as Scalar | YAMLMap | YAMLSeq).anchor;
    if (a) return 'anchor: ' + a;
  }
  return null;
};

type EditFn = (mut: () => void) => void;

interface RowCtx {
  readOnly: boolean;
  edit: EditFn;
  collapsed: Set<string>;
  toggle: (p: string) => void;
  notesOpen: Set<string>;
  openNote: (p: string) => void;
}

// Commit-on-blur leaf input so typing never re-parses the document on
// every keystroke; the key pins it to the current raw value so an
// external reload (discard draft) resets what is shown.
function Leaf({
  value,
  onCommit,
  multiline,
  monoFont = true,
  disabled,
  placeholder,
  testId,
}: {
  value: string;
  onCommit: (v: string) => void;
  multiline?: boolean;
  monoFont?: boolean;
  disabled?: boolean;
  placeholder?: string;
  testId?: string;
}) {
  const Comp: any = multiline ? Input.TextArea : Input;
  return (
    <Comp
      key={value}
      defaultValue={value}
      autoSize={multiline ? { minRows: 1, maxRows: 16 } : undefined}
      size="small"
      disabled={disabled}
      placeholder={placeholder}
      data-testid={testId}
      style={monoFont ? mono : undefined}
      onBlur={(e: { target: { value: string } }) => {
        const v = e.target.value;
        if (v !== value) onCommit(v);
      }}
      onPressEnter={multiline ? undefined : (e: { target: { blur: () => void } }) => e.target.blur()}
    />
  );
}

function CommentNote({ node, path, ctx }: { node: Node; path: string; ctx: RowCtx }) {
  const raw = (node as Scalar).commentBefore;
  const has = raw !== undefined && cmtGet(raw) !== '';
  if (!has && !ctx.notesOpen.has(path)) return null;
  return (
    <div style={{ margin: '2px 0 2px 24px' }}>
      <Leaf
        value={cmtGet(raw)}
        multiline
        disabled={ctx.readOnly}
        placeholder="# note above this line"
        testId={`yaml-note-${path}`}
        onCommit={(v) => ctx.edit(() => void ((node as Scalar).commentBefore = cmtSet(v)))}
      />
    </div>
  );
}

function InlineComment({ holder, path, ctx }: { holder: { comment?: string }; path: string; ctx: RowCtx }) {
  if (ctx.readOnly && !holder.comment) return null;
  return (
    <Leaf
      value={cmtGet(holder.comment)}
      disabled={ctx.readOnly}
      placeholder="# comment"
      testId={`yaml-cmt-${path}`}
      onCommit={(v) => ctx.edit(() => void (holder.comment = cmtSet(v)))}
    />
  );
}

function RowActions({ path, ctx, moves }: { path: string; ctx: RowCtx; moves: Record<string, () => void> }) {
  if (ctx.readOnly) return null;
  const order = ['up', 'down', 'del', ...Object.keys(moves).filter((k) => k !== 'up' && k !== 'down')];
  const icons: Record<string, ReactNode> = {
    up: <ArrowUpOutlined />,
    down: <ArrowDownOutlined />,
    del: <MinusCircleOutlined />,
    add: <PlusOutlined />,
    addmap: <PlusCircleOutlined />,
  };
  return (
    <span style={{ whiteSpace: 'nowrap' }}>
      {order
        .filter((k) => moves[k])
        .map((k) => (
          <Tooltip key={k} title={k}>
            <Button
              size="small"
              type="text"
              data-testid={`yaml-act-${k}-${path}`}
              icon={icons[k]}
              onClick={moves[k]}
            />
          </Tooltip>
        ))}
    </span>
  );
}

function ScalarCell({ node, path, ctx }: { node: Node; path: string; ctx: RowCtx }) {
  if (isAlias(node) || (isScalar(node) && node.anchor)) {
    return (
      <>
        <Tag>{locked(node)}</Tag>
        <span style={{ ...mono, opacity: 0.7 }}>
          {isAlias(node) ? '*' + ((node as unknown) as { source?: string }).source : scalarText(node as Scalar)}
        </span>
      </>
    );
  }
  if (!isScalar(node)) return <span style={{ opacity: 0.5 }}>unprintable value</span>;
  const raw = scalarText(node);
  return (
    <Leaf
      value={raw}
      multiline={isBlockish(node)}
      disabled={ctx.readOnly}
      testId={`yaml-val-${path}`}
      onCommit={(v) => ctx.edit(() => applyScalarText(node, v))}
    />
  );
}

function CollectionCell({ node, path, ctx }: { node: YAMLMap | YAMLSeq; path: string; ctx: RowCtx }) {
  const isCollapsed = ctx.collapsed.has(path);
  const kind = isMap(node) ? 'map' : 'seq';
  const count = node.items.length;
  const addItem = () =>
    ctx.edit(() => {
      if (isMap(node)) node.items.push(new Pair(new Scalar('new_key'), new Scalar('')));
      else node.items.push(new Scalar(''));
    });
  const addNote = node.commentBefore === undefined && ctx.notesOpen.has(path + '#note');
  return (
    <>
      {addNote && (
        <Leaf
          value=""
          multiline
          disabled={ctx.readOnly}
          placeholder="# note above this section"
          onCommit={(v) => ctx.edit(() => void (node.commentBefore = cmtSet(v)))}
        />
      )}
      <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
        <Button size="small" type="text" data-testid={`yaml-toggle-${path}`} onClick={() => ctx.toggle(path)}>
          {isCollapsed ? '▸' : '▾'} {kind} ({count})
        </Button>
        <RowActions path={path} ctx={ctx} moves={{ add: addItem }} />
        {!ctx.readOnly && node.commentBefore === undefined && !addNote && (
          <Tooltip title="add a note above">
            <Button size="small" type="text" onClick={() => ctx.openNote(path + '#note')}>
              #
            </Button>
          </Tooltip>
        )}
      </div>
      {!isCollapsed &&
        (isMap(node)
          ? node.items.map((it, i) => (
              <MapRow key={i} pair={it} i={i} map={node} path={`${path}.${i}`} ctx={ctx} />
            ))
          : node.items.map((it, i) => (
              <SeqRow key={i} node={it as Node} i={i} seq={node} path={`${path}.${i}`} ctx={ctx} />
            )))}
    </>
  );
}

function MapRow({ pair, i, map, path, ctx }: { pair: Pair; i: number; map: YAMLMap; path: string; ctx: RowCtx }) {
  if (!isPair(pair)) return null;
  const keyNode = pair.key as Node;
  const valNode = pair.value as Node | null;
  const keyRaw = isScalar(keyNode) ? scalarText(keyNode) : String(keyNode);
  const keyLocked = !isScalar(keyNode) || !!locked(keyNode);
  const valComment = (valNode ?? keyNode) as { comment?: string };
  const noteOwner: Node = isScalar(valNode) || valNode == null ? keyNode : (valNode as Node);
  const hasNote = noteOwner.commentBefore !== undefined && cmtGet(noteOwner.commentBefore) !== '';
  const move = (dx: number) => () =>
    ctx.edit(() => {
      const j = i + dx;
      if (j < 0 || j >= map.items.length) return;
      const a = map.items[i]!;
      const b = map.items[j]!;
      map.items[i] = b;
      map.items[j] = a;
    });
  const del = () => ctx.edit(() => void map.items.splice(i, 1));
  return (
    <>
      <CommentNote node={noteOwner} path={path} ctx={ctx} />
      {!hasNote && !ctx.readOnly && !ctx.notesOpen.has(path + '#note') && (
        <div style={{ marginLeft: 24 }}>
          <Tooltip title="add a note above this line">
            <Button size="small" type="text" onClick={() => ctx.openNote(path + '#note')}>
              #
            </Button>
          </Tooltip>
        </div>
      )}
      {!hasNote && !ctx.readOnly && ctx.notesOpen.has(path + '#note') && (
        <div style={{ margin: '2px 0 2px 24px' }}>
          <Leaf
            value=""
            multiline
            placeholder="# note above this line"
            onCommit={(v) => ctx.edit(() => void (noteOwner.commentBefore = cmtSet(v)))}
          />
        </div>
      )}
      <div
        style={{
          display: 'flex',
          gap: 6,
          alignItems: 'flex-start',
          padding: '2px 0',
          borderBottom: '1px dashed #f0f0f0',
        }}
      >
        <span style={{ width: 220, flexShrink: 0 }}>
          <Leaf
            value={keyRaw}
            disabled={ctx.readOnly || keyLocked}
            testId={`yaml-key-${path}`}
            onCommit={(v) => ctx.edit(() => void ((keyNode as Scalar).value = v))}
          />
        </span>
        <span style={{ flex: 1, minWidth: 120, display: 'flex', gap: 6, alignItems: 'flex-start' }}>
          {valNode == null ? (
            <Leaf
              value=""
              placeholder="(empty value)"
              disabled={ctx.readOnly}
              testId={`yaml-val-${path}`}
              onCommit={(v) =>
                ctx.edit(() => {
                  const s = new Scalar(v);
                  applyScalarText(s, v);
                  pair.value = s;
                })
              }
            />
          ) : isMap(valNode) || isSeq(valNode) ? (
            <CollectionCell node={valNode} path={path} ctx={ctx} />
          ) : (
            <ScalarCell node={valNode} path={path} ctx={ctx} />
          )}
        </span>
        <span style={{ width: 200, flexShrink: 0 }}>
          <InlineComment holder={valComment} path={path} ctx={ctx} />
        </span>
        <RowActions path={path} ctx={ctx} moves={{ up: move(-1), down: move(1), del }} />
      </div>
    </>
  );
}

function SeqRow({ node, i, seq, path, ctx }: { node: Node; i: number; seq: YAMLSeq; path: string; ctx: RowCtx }) {
  const hasNote = node.commentBefore !== undefined && cmtGet(node.commentBefore) !== '';
  const move = (dx: number) => () =>
    ctx.edit(() => {
      const j = i + dx;
      if (j < 0 || j >= seq.items.length) return;
      [seq.items[i], seq.items[j]] = [seq.items[j], seq.items[i]];
    });
  const del = () => ctx.edit(() => void seq.items.splice(i, 1));
  return (
    <>
      <CommentNote node={node} path={path} ctx={ctx} />
      {!hasNote && !ctx.readOnly && !ctx.notesOpen.has(path + '#note') && (
        <div style={{ marginLeft: 40 }}>
          <Tooltip title="add a note above this item">
            <Button size="small" type="text" onClick={() => ctx.openNote(path + '#note')}>
              #
            </Button>
          </Tooltip>
        </div>
      )}
      {!hasNote && !ctx.readOnly && ctx.notesOpen.has(path + '#note') && (
        <div style={{ margin: '2px 0 2px 40px' }}>
          <Leaf
            value=""
            multiline
            placeholder="# note above this item"
            onCommit={(v) => ctx.edit(() => void (node.commentBefore = cmtSet(v)))}
          />
        </div>
      )}
      <div style={{ display: 'flex', gap: 6, alignItems: 'flex-start', padding: '2px 0' }}>
        <span style={{ ...mono, opacity: 0.4, width: 28, textAlign: 'right', flexShrink: 0 }}>{i}</span>
        <span style={{ flex: 1, minWidth: 120, display: 'flex', gap: 6, alignItems: 'flex-start' }}>
          {isMap(node) || isSeq(node) ? (
            <CollectionCell node={node} path={path} ctx={ctx} />
          ) : (
            <ScalarCell node={node} path={path} ctx={ctx} />
          )}
        </span>
        <span style={{ width: 200, flexShrink: 0 }}>
          <InlineComment holder={node as { comment?: string }} path={path} ctx={ctx} />
        </span>
        <RowActions path={path} ctx={ctx} moves={{ up: move(-1), down: move(1), del }} />
      </div>
    </>
  );
}

export default function YamlTree({
  text,
  onChange,
  readOnly,
}: {
  text: string;
  onChange: (t: string) => void;
  readOnly?: boolean;
}) {
  const docs = useMemo(() => parseAllDocuments(text), [text]);
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [notesOpen, setNotesOpen] = useState<Set<string>>(new Set());

  const ctx: RowCtx = {
    readOnly: !!readOnly,
    edit: (mut) => {
      mut();
      onChange(serialize(docs));
    },
    collapsed,
    toggle: (p) =>
      setCollapsed((s) => {
        const n = new Set(s);
        n.has(p) ? n.delete(p) : n.add(p);
        return n;
      }),
    notesOpen,
    openNote: (p) =>
      setNotesOpen((s) => new Set(s).add(p)),
  };

  const broken = docs.flatMap((d, i) => d.errors.map((e) => `document ${i + 1}: ${e.message}`));
  return (
    <div data-testid="yaml-tree">
      {broken.length > 0 && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 8 }}
          message="this file does not fully parse as YAML"
          description={<span style={mono}>{broken.join('; ')}</span>}
        />
      )}
      {docs.map((doc, di) => (
        <div key={di} style={{ marginBottom: 12 }}>
          {docs.length > 1 && (
            <Typography.Text type="secondary" style={{ ...mono, fontSize: 12 }}>
              document {di + 1} of {docs.length}
            </Typography.Text>
          )}
          {doc.contents == null ? (
            readOnly ? null : (
              <Leaf
                value=""
                placeholder="(empty document — type a value)"
                testId={`yaml-val-doc${di}`}
                onCommit={(v) =>
                  ctx.edit(() => {
                    const s = new Scalar(v);
                    applyScalarText(s, v);
                    doc.contents = s as unknown as NonNullable<typeof doc.contents>;
                  })
                }
              />
            )
          ) : isMap(doc.contents) || isSeq(doc.contents) ? (
            <CollectionCell node={doc.contents} path={`d${di}`} ctx={ctx} />
          ) : (
            <ScalarCell node={doc.contents} path={`d${di}`} ctx={ctx} />
          )}
        </div>
      ))}
    </div>
  );
}
