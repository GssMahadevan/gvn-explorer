import type { ReactNode } from 'react';
import { Button, Tooltip } from 'antd';
import { ArrowDownOutlined, ArrowUpOutlined, MinusCircleOutlined, PlusOutlined } from '@ant-design/icons';

// The small action strip used by the detail tables: reorder/remove rows
// and append new ones. Kept as icons-only so the tables stay narrow.
export default function YamlRowActions({
  path,
  moves,
}: {
  path: string;
  moves: Record<string, (() => void) | undefined>;
}) {
  const icons: Record<string, ReactNode> = {
    up: <ArrowUpOutlined />,
    down: <ArrowDownOutlined />,
    del: <MinusCircleOutlined />,
    add: <PlusOutlined />,
  };
  return (
    <span style={{ whiteSpace: 'nowrap' }}>
      {['up', 'down', 'del', 'add']
        .filter((k) => moves[k])
        .map((k) => (
          <Tooltip key={k} title={k}>
            <Button size="small" type="text" data-testid={`yaml-act-${k}-${path}`} icon={icons[k]} onClick={moves[k]} />
          </Tooltip>
        ))}
    </span>
  );
}
