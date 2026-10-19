import { Button, Table, Tag } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { isPair, isScalar, Pair, Scalar, type Node, type YAMLMap } from 'yaml';
import YamlLeaf, { YamlNoteEditor } from './YamlLeaf';
import YamlRowActions from './YamlRowActions';
import {
  cmtGet,
  lockedLabel,
  makeLeafAccess,
  mono,
  scalarText,
  short,
  summaryOf,
  type EditorCtx,
  type Resolved,
} from './yamlutil';

// Mapping level of the drill-down: one row per entry — K | V |
// description. Values and descriptions are shown, not edited here:
// clicking any row pushes the path stack and opens the leaf screen for
// a scalar, or the next level for a map/seq (whose row shows a ▸
// summary button). The table itself edits the structure of THIS node:
// rename keys, add, reorder, delete.
export default function YamlKvPanel({ r, path, ctx }: { r: Resolved; path: string; ctx: EditorCtx }) {
  const map = r.node as YAMLMap;
  const panelAcc = makeLeafAccess(r, ctx.edit);
  const move = (i: number, dx: number) => () =>
    ctx.edit(() => {
      const j = i + dx;
      if (j < 0 || j >= map.items.length) return;
      const a = map.items[i]!;
      const b = map.items[j]!;
      map.items[i] = b;
      map.items[j] = a;
    });
  const del = (i: number) => () => ctx.edit(() => void map.items.splice(i, 1));
  const add = () =>
    ctx.edit(() => void map.items.push(new Pair(new Scalar('new_key'), new Scalar(''))));

  const columns: ColumnsType<Pair> = [
    {
      title: 'key',
      width: 200,
      render: (p, _1, i) => {
        const childPath = `${path}.${i}`;
        const keyNode = p.key as Node;
        const isKeyScalar = isScalar(keyNode);
        return (
          <YamlLeaf
            value={isKeyScalar ? scalarText(keyNode as Scalar) : String(p.key)}
            disabled={ctx.readOnly || !isKeyScalar || !!lockedLabel(keyNode)}
            testId={`yaml-key-${childPath}`}
            onCommit={(v) => ctx.edit(() => void ((keyNode as Scalar).value = v))}
          />
        );
      },
    },
    {
      title: 'value',
      render: (p, _1, i) => {
        const childPath = `${path}.${i}`;
        const val = (p.value ?? null) as Node | null;
        const lock = val != null ? lockedLabel(val) : null;
        if (lock) return <Tag>{lock}</Tag>;
        if (val != null && !isScalar(val))
          return (
            <Button size="small" data-testid={`yaml-open-${childPath}`} onClick={(e) => { e.stopPropagation(); ctx.select(childPath); }}>
              {summaryOf(val)} ▸
            </Button>
          );
        const raw = val == null ? '' : scalarText(val as Scalar).replace(/\n+/g, ' ');
        return (
          <span data-testid={`yaml-valview-${childPath}`} style={{ ...mono, opacity: val == null ? 0.35 : 0.75, whiteSpace: 'pre' }}>
            {val == null ? '(empty)' : short(raw, 200)}
          </span>
        );
      },
    },
    {
      title: 'description',
      render: (p, _1, i) => {
        const holder = (p.value ?? p.key) as { comment?: string };
        const text = cmtGet(holder.comment);
        if (!text) return null;
        return (
          <span data-testid={`yaml-cmtview-${path}.${i}`} style={{ ...mono, fontSize: 12, opacity: 0.55, whiteSpace: 'pre' }}>
            {short(text, 160)}
          </span>
        );
      },
    },
    {
      title: '',
      width: 110,
      render: (_p, _1, i) =>
        ctx.readOnly ? null : (
          <YamlRowActions
            path={`${path}.${i}`}
            moves={{
              up: i > 0 ? move(i, -1) : undefined,
              down: i < map.items.length - 1 ? move(i, 1) : undefined,
              del: del(i),
            }}
          />
        ),
    },
  ];

  return (
    <div data-testid={`yaml-kv-${path}`}>
      {panelAcc && (
        <div style={{ marginBottom: 6 }}>
          <YamlNoteEditor acc={panelAcc} testPath={path} readOnly={ctx.readOnly} />
        </div>
      )}
      <Table
        size="small"
        pagination={false}
        // columns keep their real width and the table scrolls sideways
        // on narrow screens — squeezing the cells made keys unreadable
        // on phones (same pattern as the admin tables).
        scroll={{ x: 'max-content' }}
        rowKey={(p) => String(map.items.indexOf(p))}
        dataSource={map.items.filter(isPair) as Pair[]}
        columns={columns}
        onRow={(p) => ({
          onClick: () => ctx.select(`${path}.${map.items.indexOf(p)}`),
          style: { cursor: 'pointer' },
        })}
      />
      {!ctx.readOnly && (
        <Button size="small" style={{ marginTop: 8 }} data-testid={`yaml-act-add-${path}`} onClick={add}>
          + add entry
        </Button>
      )}
    </div>
  );
}
