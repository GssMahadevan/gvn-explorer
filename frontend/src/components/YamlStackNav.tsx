import type { ReactNode } from 'react';
import { Button } from 'antd';
import {
  DatabaseOutlined,
  FileOutlined,
  FileTextOutlined,
  FolderOutlined,
  UnorderedListOutlined,
} from '@ant-design/icons';
import type { Document } from 'yaml';
import { mono, segmentsOf, type NodeKind } from './yamlutil';

// The left "you are here" panel: one flat row per path level, root on
// top and the current node at the bottom. Deliberately NOT an indented
// tree — depth costs vertical space only, so deeply nested config files
// never need horizontal scrolling, on any screen width. Tapping any row
// jumps the editor to that level (that's the back/navigation action);
// the icons are intentionally plain per-node-type glyphs for now.
const icons: Record<NodeKind, ReactNode> = {
  file: <DatabaseOutlined />,
  doc: <FileOutlined />,
  map: <FolderOutlined />,
  seq: <UnorderedListOutlined />,
  scalar: <FileTextOutlined />,
};

export default function YamlStackNav({
  docs,
  path,
  onSelect,
  onBack,
  onClose,
}: {
  docs: Document[];
  path: string;
  onSelect: (p: string) => void;
  onBack: () => void;
  onClose?: () => void;
}) {
  const crumbs = segmentsOf(docs, path);
  // root of *this* file: a single document's root, or the file level
  // itself when there are several documents.
  const atRoot = crumbs.length <= 0 || (crumbs.length === 1 && crumbs[0]?.path !== 'd') || crumbs.at(-1)?.path === 'd';
  return (
    <div data-testid="yaml-nav">
      <Button
        size="small"
        type="text"
        data-testid="yaml-back"
        style={{ marginBottom: 4 }}
        onClick={atRoot ? onClose : onBack}
        disabled={!onClose && atRoot}
      >
        ↑ {atRoot ? 'close' : 'parent'}
      </Button>
      {crumbs.map((c, i) => (
        <div
          key={c.path}
          data-testid={`yaml-crumb-${c.path}`}
          onClick={() => onSelect(c.path)}
          style={{
            display: 'flex',
            gap: 6,
            alignItems: 'center',
            padding: '4px 8px',
            cursor: 'pointer',
            borderRadius: 6,
            background: i === crumbs.length - 1 ? 'rgba(22,119,255,0.10)' : undefined,
          }}
        >
          <span style={{ opacity: 0.7 }}>{icons[c.kind]}</span>
          <span style={{ ...mono, fontSize: 13 }}>{c.label}</span>
          <span style={{ ...mono, fontSize: 12, opacity: 0.4, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {c.summary}
          </span>
        </div>
      ))}
    </div>
  );
}
