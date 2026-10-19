import { Alert, Tag, Typography } from 'antd';
import { isAlias, isScalar, type Scalar } from 'yaml';
import YamlLeaf, { YamlInlineComment, YamlNoteEditor } from './YamlLeaf';
import { isBlockish, mono, scalarText, type EditorCtx, type LeafAccess, type Resolved } from './yamlutil';

// Detail view for one selected scalar (or an empty/alias value). The
// full-width layout is what makes long values and block scalars usable
// on a phone: notes above, value in the middle, inline comment below.
export default function YamlScalarPanel({
  r,
  path,
  ctx,
  acc,
}: {
  r: Resolved;
  path: string;
  ctx: EditorCtx;
  acc: LeafAccess;
}) {
  const node = r.node;
  if (node != null && isAlias(node)) {
    const src = ((node as unknown) as { source?: string }).source ?? '?';
    return (
      <div data-testid={`yaml-scalar-${path}`}>
        <Alert
          type="info"
          showIcon
          message={<span>alias value — defined elsewhere as <Tag>&amp;{src}</Tag></span>}
          description="edit the anchored node instead; the alias follows it automatically"
        />
      </div>
    );
  }
  const locked = acc.locked;
  const blockish = !locked && node != null && isScalar(node) && isBlockish(node as Scalar);
  return (
    <div data-testid={`yaml-scalar-${path}`} style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      <div>
        <YamlNoteEditor acc={acc} testPath={path} readOnly={ctx.readOnly} />
      </div>
      {locked ? (
        <>
          <Tag>{locked}</Tag>
          <Typography.Text style={{ ...mono, opacity: 0.7 }}>
            {node != null && isScalar(node) ? scalarText(node as Scalar) : ''}
          </Typography.Text>
        </>
      ) : (
        <YamlLeaf
          value={acc.getValue()}
          multiline={blockish}
          monoFont
          disabled={ctx.readOnly}
          placeholder="(empty value)"
          testId={`yaml-val-${path}`}
          onCommit={(v) => acc.setValue(v)}
        />
      )}
      <YamlInlineComment acc={acc} testPath={path} readOnly={ctx.readOnly} />
    </div>
  );
}
