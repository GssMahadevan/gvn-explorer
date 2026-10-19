import { useMemo, useState } from 'react';
import { Alert, Button, Drawer, Grid, Tag, Typography } from 'antd';
import { isAlias, isMap, isSeq, parseAllDocuments, type Document } from 'yaml';
import YamlKvPanel from './YamlKvPanel';
import YamlScalarPanel from './YamlScalarPanel';
import YamlSeqPanel from './YamlSeqPanel';
import YamlStackNav from './YamlStackNav';
import {
  makeLeafAccess,
  mono,
  pathLabel,
  resolvePath,
  serialize,
  summaryOf,
  type EditorCtx,
} from './yamlutil';

// Overall aggregate panel, organized like drilling into folders: the
// left panel is a flat path stack (root on top, current node at the
// bottom, one row per level — deep nesting never costs width), and the
// right side edits the node the stack sits on: mappings as a K / V /
// description table, arrays as a value / comment table, scalars as a
// pushed full-width editor. Rows in a table push the stack one level
// deeper; rows in the stack jump back. On phone widths the stack lives
// in a drawer that hides itself as soon as you pick a level, and the
// editor keeps the full width. Text state lives in the parent: every
// mutation edits the parsed AST and re-serializes, which is what keeps
// comments and quoting styles intact.
export default function YamlEditor({
  text,
  onChange,
  readOnly,
  onClose,
}: {
  text: string;
  onChange: (t: string) => void;
  readOnly?: boolean;
  onClose?: () => void;
}) {
  const docs = useMemo(() => parseAllDocuments(text), [text]);
  const [sel, setSel] = useState<string | null>(null);
  const [navOpen, setNavOpen] = useState(false);
  const screens = Grid.useBreakpoint();
  // md undefined during the first paint: treat unknown as desktop.
  const narrow = screens.md === false;

  const edit = (mut: () => void) => {
    mut();
    onChange(serialize(docs));
  };
  // multi-document files get a "file" level above the documents so
  // there is somewhere to switch between them; single documents start
  // at their root.
  const hasFileLevel = docs.length > 1;
  const select = (p: string) => {
    setSel(p);
    setNavOpen(false);
  };
  const ctx: EditorCtx = { readOnly: !!readOnly, edit, select };

  const shown = sel ?? (docs.length > 0 ? 'd0' : null);
  const goUp = () => {
    if (!shown) return;
    const parent = shown.split('.').slice(0, -1).join('.');
    if (parent === 'd0' || parent === 'd') select(parent);
    else if (parent !== '') select(parent);
    else if (hasFileLevel && shown !== 'd') select('d');
    else onClose?.();
  };

  const broken = docs.flatMap((d, i) => d.errors.map((e) => `document ${i + 1}: ${e.message}`));
  const nav = shown && <YamlStackNav docs={docs} path={shown} onSelect={select} onBack={goUp} onClose={onClose} />;

  return (
    <div data-testid="yaml-editor">
      {broken.length > 0 && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 8 }}
          message="this file does not fully parse as YAML"
          description={<span style={mono}>{broken.join('; ')}</span>}
        />
      )}
      <div style={{ display: 'flex', gap: 12 }}>
        {!narrow && (
          <div data-testid="yaml-nav-pane" style={{ width: 240, flexShrink: 0, maxHeight: '55vh', overflow: 'auto' }}>
            {nav}
          </div>
        )}
        <div style={{ flex: 1, minWidth: 0 }}>
          {narrow && (
            <div style={{ display: 'flex', gap: 8, marginBottom: 8 }}>
              <Button size="small" data-testid="yaml-nav-open" onClick={() => setNavOpen(true)}>
                ☰ {shown ? pathLabel(docs, shown) : 'path'}
              </Button>
              <Button size="small" data-testid="yaml-back-m" onClick={goUp}>
                ↑ parent
              </Button>
            </div>
          )}
          {shown && <DetailPane docs={docs} path={shown} ctx={ctx} hasFileLevel={hasFileLevel} />}
        </div>
      </div>
      <Drawer placement="left" open={narrow && navOpen} onClose={() => setNavOpen(false)} width="72%" data-testid="yaml-nav-drawer">
        {nav}
      </Drawer>
    </div>
  );
}

// Right side: open the node in whatever editor matches its shape.
function DetailPane({
  docs,
  path,
  ctx,
  hasFileLevel,
}: {
  docs: Document[];
  path: string;
  ctx: EditorCtx;
  hasFileLevel: boolean;
}) {
  if (path === 'd')
    return (
      <div data-testid="yaml-docs">
        <div style={{ marginBottom: 8 }}>
          <Typography.Text style={{ ...mono, fontSize: 12 }} data-testid="yaml-detail-path">
            file
          </Typography.Text>
          <Tag>{docs.length} documents</Tag>
        </div>
        {docs.map((doc, i) => (
          <div key={i} style={{ marginBottom: 6 }}>
            <Button size="small" data-testid={`yaml-open-d${i}`} onClick={() => ctx.select(`d${i}`)}>
              document {i + 1} {summaryOf(doc.contents as never)} ▸
            </Button>
          </div>
        ))}
      </div>
    );
  const r = resolvePath(docs, path);
  if (!r)
    return <Alert type="info" message="this node is gone — pick one in the path stack" data-testid="yaml-detail-missing" />;
  const kind =
    r.node == null ? 'empty' : isMap(r.node) ? 'map' : isSeq(r.node) ? 'seq' : isAlias(r.node) ? 'alias' : 'value';
  const acc = makeLeafAccess(r, ctx.edit);
  return (
    <div data-testid="yaml-detail">
      <div style={{ marginBottom: 8, display: 'flex', gap: 8, alignItems: 'center' }}>
        <Typography.Text style={{ ...mono, fontSize: 12 }} data-testid="yaml-detail-path">
          {pathLabel(docs, path)}
        </Typography.Text>
        <Tag>{kind}</Tag>
        {hasFileLevel && (
          <Button size="small" type="text" onClick={() => ctx.select('d')}>
            switch document
          </Button>
        )}
      </div>
      {isMap(r.node) ? (
        <YamlKvPanel r={r} path={path} ctx={ctx} />
      ) : isSeq(r.node) ? (
        <YamlSeqPanel r={r} path={path} ctx={ctx} />
      ) : acc ? (
        <YamlScalarPanel r={r} path={path} ctx={ctx} acc={acc} />
      ) : (
        <Alert type="info" message="empty document — use the file view for raw text" />
      )}
    </div>
  );
}
