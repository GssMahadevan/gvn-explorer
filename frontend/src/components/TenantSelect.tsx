import { useRef, useState } from 'react';
import { Input, Select } from 'antd';
import { api } from '../api';
import type { Me, Tenant } from '../types';

// Tenant picker used by every create/grant form. Tenant admins are pinned
// to their own tenant — the backend enforces exactly that, so showing it
// fixed beats letting someone type a name that 403s. Super admins get a
// remote-searchable select (tenant rosters can outgrow one page).
export default function TenantSelect({
  me,
  value,
  onChange,
  testId,
}: {
  me: Me;
  value?: string;
  onChange?: (v: string) => void;
  testId?: string;
}) {
  const [opts, setOpts] = useState<{ value: string; label: string }[]>([]);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);

  const search = async (q: string) => {
    const p = new URLSearchParams({ sort: 'name', limit: '50' });
    if (q) p.set('q', q);
    const ts = await api<Tenant[]>('/api/v1/tenants?' + p.toString());
    setOpts(
      ts.map((t) => ({
        value: t.name,
        label: t.active === false ? t.name + ' (suspended)' : t.name,
      })),
    );
  };

  if (!me.is_super) {
    // fixed tenant: this caller can only ever act inside their own
    return <Input value={value ?? me.tenant} disabled title="you always act inside your own tenant" />;
  }
  return (
    <Select
      showSearch
      data-testid={testId}
      placeholder={'defaults to ' + me.tenant}
      value={value}
      filterOption={false}
      options={opts}
      onSearch={(q) => {
        clearTimeout(timer.current);
        timer.current = setTimeout(() => void search(q), 250);
      }}
      onDropdownVisibleChange={(open) => open && void search('')}
      onChange={(v) => onChange?.(v)}
    />
  );
}
