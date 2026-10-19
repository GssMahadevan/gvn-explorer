import { Button, Table, Tag } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { isPair, isScalar, Pair, Scalar, type Node, type YAMLMap } from 'yaml';
import YamlLeaf, { YamlInlineComment, YamlNoteEditor } from './YamlLeaf';
import YamlRowActions from './YamlRowActions';
import {
  applyScalarText,
  isBlockish,
  lockedLabel,
  makeLeafAccess,
  scalarText,
  summaryOf,
  type EditorCtx,
  type Resolved,
} from './yamlutil';

// Mapping detail: one row per entry, columns K | V | description — the
// description column is the entry's inline YAML comment. Container
// values (map/seq) show a summary button that opens them in this panel;
// clicking any row selects it and moves the tree + detail to that node.
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
        if (val != null && lockedLabel(val)) return <Tag>{lockedLabel(val)}</Tag>;
        if (val == null)
          return (
            <YamlLeaf
              value=""
              placeholder="(empty value)"
              disabled={ctx.readOnly}
              testId={`yaml-val-${childPath}`}
              onCommit={(v) =>
                ctx.edit(() => {
                  const s = new Scalar(v);
                  applyScalarText(s, v);
                  p.value = s;
                })
              }
            />
          );
        if (!isScalar(val))
          return (
            <Button size="small" data-testid={`yaml-open-${childPath}`} onClick={(e) => { e.stopPropagation(); ctx.select(childPath); }}>
              {summaryOf(val)}
            </Button>
          );
        return (
          <YamlLeaf
            value={scalarText(val as Scalar)}
            multiline={isBlockish(val as Scalar)}
            disabled={ctx.readOnly}
            testId={`yaml-val-${childPath}`}
            onCommit={(v) => ctx.edit(() => applyScalarText(val as Scalar, v))}
          />
        );
      },
    },
    {
      title: 'description',
      width: 220,
      render: (p, _1, i) => {
        const acc = makeLeafAccess({ doc: r.doc, node: (p.value ?? null) as Node | null, parent: map, pair: p }, ctx.edit);
        if (!acc) return null;
        return <YamlInlineComment acc={acc} testPath={`${path}.${i}`} readOnly={ctx.readOnly} />;
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
