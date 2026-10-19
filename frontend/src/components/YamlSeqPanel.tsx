import { Button, Table, Tag } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { isScalar, Scalar, type Node, type YAMLSeq } from 'yaml';
import { YamlNoteEditor } from './YamlLeaf';
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

// Array level: value and comment columns, view-only — clicking a row
// pushes the stack (leaf screen for a scalar, next level for a map/seq
// item shown as a ▸ summary button). The table edits the shape of this
// array: add, reorder, delete items.
export default function YamlSeqPanel({ r, path, ctx }: { r: Resolved; path: string; ctx: EditorCtx }) {
  const seq = r.node as YAMLSeq;
  const panelAcc = makeLeafAccess(r, ctx.edit);
  const move = (i: number, dx: number) => () =>
    ctx.edit(() => {
      const j = i + dx;
      if (j < 0 || j >= seq.items.length) return;
      const a = seq.items[i];
      seq.items[i] = seq.items[j];
      seq.items[j] = a;
    });
  const del = (i: number) => () => ctx.edit(() => void seq.items.splice(i, 1));
  const add = () => ctx.edit(() => void seq.items.push(new Scalar('')));

  const columns: ColumnsType<Node> = [
    {
      title: 'value',
      render: (item, _1, i) => {
        const childPath = `${path}.${i}`;
        const lock = lockedLabel(item);
        if (lock) return <Tag>{lock}</Tag>;
        if (!isScalar(item))
          return (
            <Button size="small" data-testid={`yaml-open-${childPath}`} onClick={(e) => { e.stopPropagation(); ctx.select(childPath); }}>
              {summaryOf(item)} ▸
            </Button>
          );
        return (
          <span data-testid={`yaml-valview-${childPath}`} style={{ ...mono, opacity: 0.75 }}>
            {short(scalarText(item as Scalar).replace(/\n+/g, ' '))}
          </span>
        );
      },
    },
    {
      title: 'comment',
      width: 220,
      render: (item, _1, i) => {
        const text = cmtGet((item as { comment?: string }).comment);
        if (!text) return null;
        return (
          <span data-testid={`yaml-cmtview-${path}.${i}`} style={{ ...mono, fontSize: 12, opacity: 0.55 }}>
            {short(text, 60)}
          </span>
        );
      },
    },
    {
      title: '',
      width: 110,
      render: (_item, _1, i) =>
        ctx.readOnly ? null : (
          <YamlRowActions
            path={`${path}.${i}`}
            moves={{
              up: i > 0 ? move(i, -1) : undefined,
              down: i < seq.items.length - 1 ? move(i, 1) : undefined,
              del: del(i),
            }}
          />
        ),
    },
  ];

  return (
    <div data-testid={`yaml-seq-${path}`}>
      {panelAcc && (
        <div style={{ marginBottom: 6 }}>
          <YamlNoteEditor acc={panelAcc} testPath={path} readOnly={ctx.readOnly} />
        </div>
      )}
      <Table
        size="small"
        pagination={false}
        rowKey={(item) => String(seq.items.indexOf(item))}
        dataSource={seq.items as Node[]}
        columns={columns}
        onRow={(item) => ({
          onClick: () => ctx.select(`${path}.${seq.items.indexOf(item)}`),
          style: { cursor: 'pointer' },
        })}
      />
      {!ctx.readOnly && (
        <Button size="small" style={{ marginTop: 8 }} data-testid={`yaml-act-add-${path}`} onClick={add}>
          + add item
        </Button>
      )}
    </div>
  );
}
