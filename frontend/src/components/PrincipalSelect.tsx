import { useRef, useState } from 'react';
import { AutoComplete } from 'antd';
import { api } from '../api';
import type { Group, User } from '../types';

// Name suggestions for principal fields (grant modal, group members).
// Suggestions are scoped to exactly the tenant the backend would resolve
// the name in, so "users for the selected tenant" is what you see.
// Free typing still lands: an AutoComplete never blocks an unlisted name.
export default function PrincipalSelect({
  kind,
  tenant,
  placeholder,
  value,
  onChange,
  style,
  testId,
}: {
  kind: 'user' | 'group';
  tenant: string;
  placeholder?: string;
  value?: string;
  onChange?: (v: string) => void;
  style?: React.CSSProperties;
  testId?: string;
}) {
  const [opts, setOpts] = useState<{ value: string }[]>([]);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);

  const search = async (q: string) => {
    const p = new URLSearchParams({
      sort: kind === 'user' ? 'username' : 'name',
      limit: '50',
      tenant,
    });
    if (q) p.set('q', q);
    const rows = await api<(User | Group)[]>('/api/v1/' + kind + 's?' + p.toString());
    setOpts(rows.map((r) => ({ value: (r as User).username ?? (r as Group).name })));
  };

  return (
    <AutoComplete
      data-testid={testId}
      style={style}
      value={value}
      options={opts}
      placeholder={placeholder}
      onSearch={(q) => {
        clearTimeout(timer.current);
        timer.current = setTimeout(() => void search(q), 250);
      }}
      onDropdownVisibleChange={(open) => open && void search('')}
      onChange={(v) => onChange?.(v)}
    />
  );
}
