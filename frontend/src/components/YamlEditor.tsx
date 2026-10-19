import { useMemo, useState } from 'react';
import { Alert, Drawer, Grid, Tag, Typography } from 'antd';
import { isAlias, isMap, isSeq, parseAllDocuments } from 'yaml';
import YamlKvPanel from './YamlKvPanel';
import YamlNodeTree from './YamlNodeTree';
import YamlScalarPanel from './YamlScalarPanel';
import YamlSeqPanel from './YamlSeqPanel';
import {
  makeLeafAccess,
  mono,
  pathLabel,
  resolvePath,
  serialize,
  type EditorCtx,
} from './yamlutil';

// Overall aggregate panel for YAML config files, laid out like a file
// explorer: the left tree shows how the whole document is organized
// (parents above children, one row each — readable at phone widths),
// the right side edits the selected node as the shape it actually is:
// mappings as a K/V/description table, arrays as a value/comment table,
// scalars as a full-width editor. On phone widths the right side opens
// as a drawer over the tree. Text state lives in the parent: every
// mutation edits the parsed AST and re-serializes, which is what keeps
// comments and quoting styles intact.
export default function YamlEditor({
  text,
  onChange,
  readOnly,
}: {
  text: string;
  onChange: (t: string) => void;
  readOnly?: boolean;
}) {
  const docs = useMemo(() => parseAllDocuments(text), [text]);
  const [sel, setSel] = useState<string | null>(null);
  const screens = Grid.useBreakpoint();
  // md undefined during the first paint: treat unknown as desktop.
  const narrow = screens.md === false;

  const edit = (mut: () => void) => {
    mut();
    onChange(serialize(docs));
  };
  const ctx: EditorCtx = { readOnly: !!readOnly, edit, select: setSel };

  const broken = docs.flatMap((d, i) => d.errors.map((e) => `document ${i + 1}: ${e.message}`));
  // desktop defaults to the first document's root so the table is up
  // immediately; the mobile drawer only opens on an explicit tree tap.
  const shown = sel ?? (docs.length > 0 ? 'd0' : null);

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
        <div
          data-testid="yaml-tree-pane"
          style={{ width: narrow ? '100%' : 300, flexShrink: 0, maxHeight: '55vh', overflow: 'auto' }}
        >
          <YamlNodeTree docs={docs} selected={narrow ? sel : shown} onSelect={setSel} />
        </div>
        {!narrow && (
          <div style={{ flex: 1, minWidth: 0 }}>
            {shown && <DetailPane docs={docs} path={shown} ctx={ctx} />}
          </div>
        )}
      </div>
      <Drawer open={narrow && sel !== null} onClose={() => setSel(null)} width="92%" data-testid="yaml-drawer">
        {shown && <DetailPane docs={docs} path={shown} ctx={ctx} />}
      </Drawer>
    </div>
  );
}

// Right side: open the node in whatever editor matches its shape.
function DetailPane({ docs, path, ctx }: { docs: ReturnType<typeof parseAllDocuments>; path: string; ctx: EditorCtx }) {
  const r = resolvePath(docs, path);
  if (!r)
    return <Alert type="info" message="this node is gone — pick one in the tree" data-testid="yaml-detail-missing" />;
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
