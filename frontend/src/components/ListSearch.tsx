import { useEffect, useRef, useState } from 'react';
import { Input, Select, Space } from 'antd';
import type { ListState } from '../api';

// Debounced search box + optional "which columns" selector — the same
// server-side q/col contract the curl suite exercises.
export default function ListSearch({
  state,
  patch,
  cols,
  placeholder,
}: {
  state: ListState;
  patch: (p: Partial<ListState>) => void;
  cols?: string[];
  placeholder: string;
}) {
  const [text, setText] = useState(state.q);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);

  useEffect(() => {
    timer.current = setTimeout(() => {
      if (text.trim() !== state.q) patch({ q: text.trim(), page: 1 });
    }, 350);
    return () => clearTimeout(timer.current);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [text]);

  return (
    <Space.Compact>
      {cols && cols.length > 1 && (
        <Select
          value={state.col}
          style={{ width: 130 }}
          onChange={(col) => patch({ col, page: 1 })}
          options={[
            { value: '', label: 'all cols' },
            ...cols.map((c) => ({ value: c, label: c })),
          ]}
        />
      )}
      <Input.Search
        allowClear
        placeholder={placeholder}
        style={{ width: 220 }}
        value={text}
        onChange={(e) => setText(e.target.value)}
        onSearch={() => patch({ q: text.trim(), page: 1 })}
      />
    </Space.Compact>
  );
}
