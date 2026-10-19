import { useState } from 'react';
import { Tree } from 'antd';
import type { DataNode } from 'antd/es/tree';
import { isAlias, isMap, isPair, isSeq, type Document, type Node } from 'yaml';
import { mono, summaryOf, ancestorsOf } from './yamlutil';

// The explorer-style left panel: every mapping key, sequence index and
// document reads as one row, parents above children — same mental model
// as a file tree, which is why it works at phone widths too. Clicking a
// row opens that node in the detail panel; selecting a deep node
// auto-expands its ancestors so the stack scrolls to the right place.
export default function YamlNodeTree({
  docs,
  selected,
  onSelect,
}: {
  docs: Document[];
  selected: string | null;
  onSelect: (path: string) => void;
}) {
  const [expanded, setExpanded] = useState<string[]>(docs.map((_, i) => `d${i}`));

  const childNodes = (node: Node | null, path: string): DataNode[] | undefined => {
    if (!node || isAlias(node)) return undefined;
    if (isMap(node))
      return node.items.map((p, i) => {
        const key = `${path}.${i}`;
        if (!isPair(p)) return { key, title: <span data-testid={`yaml-node-${key}`}>(complex)</span> };
        const k = String((p.key as { value?: unknown })?.value ?? '?');
        return {
          key,
          title: (
            <span data-testid={`yaml-node-${key}`} style={mono}>
              {k}
              <span style={{ opacity: 0.45, fontWeight: 400 }}> {summaryOf(p.value as Node)}</span>
            </span>
          ),
          children: childNodes(p.value as Node, key),
        };
      });
    if (isSeq(node))
      return node.items.map((it, i) => {
        const key = `${path}.${i}`;
        return {
          key,
          title: (
            <span data-testid={`yaml-node-${key}`} style={mono}>
              {i}
              <span style={{ opacity: 0.45, fontWeight: 400 }}> {summaryOf(it as Node)}</span>
            </span>
          ),
          children: childNodes(it as Node, key),
        };
      });
    return undefined;
  };

  const treeData: DataNode[] = docs.map((doc, di) => ({
    key: `d${di}`,
    title: (
      <span data-testid={`yaml-node-d${di}`} style={{ ...mono, opacity: 0.75 }}>
        {docs.length > 1 ? `document ${di + 1} of ${docs.length}` : 'root'}
      </span>
    ),
    children: childNodes(doc.contents as Node, `d${di}`),
  }));

  return (
    <Tree
      treeData={treeData}
      selectedKeys={selected ? [selected] : []}
      expandedKeys={expanded}
      onExpand={(ks) => setExpanded(ks as string[])}
      onSelect={(ks) => {
        const k = ks[0] as string | undefined;
        if (!k) return; // clicking the selected row again: keep it open
        setExpanded((prev) => Array.from(new Set([...prev, k, ...ancestorsOf(k)])));
        onSelect(k);
      }}
      blockNode
    />
  );
}
