import { useState } from 'react';
import { Alert, Button, Space, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { api, bits, qs } from '../api';
import type { Grant, Me, PermsList } from '../types';
import { useListView } from '../useListView';
import { useFlash } from '../flash';
import ListSearch from '../components/ListSearch';
import RowActions from '../components/RowActions';
import GrantForm from '../components/forms/GrantForm';

// Permissions panel: grants on a target path (a directory — or one file via
// a row's "perms" action; nearest-ancestor inheritance applies server-side),
// effective-for-you summary, grant modal (admin+), revoke.
export default function PermsPanel({ path, me }: { path: string; me: Me }) {
  const flash = useFlash();
  const lv = useListView<PermsList>(
    (s) => `/api/v1/perms?path=${encodeURIComponent(path)}&${qs(s)}`,
    { sort: 'principal' },
  );
  const [grantOpen, setGrantOpen] = useState(false);
  const grants = lv.payload?.grants ?? [];
  const eff = lv.payload?.effective;
  const isMgr = me.is_admin || me.is_super;

  const columns: ColumnsType<Grant> = [
    {
      title: 'principal',
      key: 'principal',
      sorter: true,
      sortOrder: lv.sortOrder('principal'),
      showSorterTooltip: false,
      render: (_, g) => (
        <Space size={4}>
          <span className="mono">
            {g.principal_tenant}/{g.principal_name}
          </span>
          {g.principal_active === false && <Tag color="red">inactive principal</Tag>}
        </Space>
      ),
    },
    {
      title: 'type',
      key: 'type',
      sorter: true,
      sortOrder: lv.sortOrder('type'),
      showSorterTooltip: false,
      width: 90,
      render: (_, g) => <Tag>{g.principal_type}</Tag>,
    },
    { title: 'r', width: 44, render: (_, g) => (g.read ? <b>r</b> : <span>-</span>) },
    { title: 'w', width: 44, render: (_, g) => (g.write ? <b>w</b> : <span>-</span>) },
    { title: 'd', width: 44, render: (_, g) => (g.delete ? <b>d</b> : <span>-</span>) },
    {
      title: 'expires',
      key: 'expires',
      sorter: true,
      sortOrder: lv.sortOrder('expires'),
      showSorterTooltip: false,
      width: 150,
      render: (_, g) =>
        g.expired ? (
          <Tag color="red">expired</Tag>
        ) : g.expires_at ? (
          new Date(g.expires_at).toLocaleDateString()
        ) : (
          <Typography.Text type="secondary">never</Typography.Text>
        ),
    },

    {
      title: '',
      width: 56,
      render: (_, g) => (
        <RowActions
          actions={[
            {
              key: 'revoke',
              label: 'revoke',
              danger: true,
              confirm: { title: 'revoke this grant?' },
              onClick: async () => {
                try {
                  await api('/api/v1/perms/' + g.id, { method: 'DELETE' });
                  flash.ok(
                    `revoked ${g.principal_type} ${g.principal_tenant}/${g.principal_name} (${bits(g)}) on ${path}`,
                  );
                  lv.reload();
                } catch (e) {
                  flash.err((e as Error).message);
                }
              },
            },
          ]}
        />
      ),
    },
  ];

  return (
    <>
      <div className="bar">
        <span>
          permissions on <Typography.Text code>{path}</Typography.Text>
        </span>
        <span className="grow" />
        <ListSearch
          state={lv.state}
          patch={lv.patch}
          cols={['principal', 'tenant']}
          placeholder="search grants"
        />
        {isMgr && (
          <Button type="primary" onClick={() => setGrantOpen(true)}>
            Grant access
          </Button>
        )}
      </div>

      <Alert
        style={{ marginBottom: 8 }}
        type={me.is_super ? 'info' : eff && (eff.read || eff.write || eff.delete) ? 'success' : 'warning'}
        message={
          me.is_super
            ? 'you are platform super admin — ACLs do not apply to you'
            : `effective for you: ${eff ? bits(eff) : '---'}${
                eff?.source ? ` (inherited from ${eff.source})` : ' (no access)'
              }`
        }
        description="grants apply to this path and inherit down; a grant with all bits off excludes the subtree for that principal. only the platform super admin bypasses ACLs."
      />

      <Table
        rowKey={(g) => g.id}
        size="small"
        columns={columns}
        dataSource={grants}
        loading={lv.loading}
        onChange={lv.onTableChange}
        pagination={lv.pagination}
        sticky
        scroll={{ x: 'max-content' }}
        locale={{
          emptyText: lv.state.q
            ? `nothing matches "${lv.state.q}"`
            : 'no grants on this exact path (inheritance may still apply)',
        }}
      />

      {isMgr && (
        <GrantForm
          open={grantOpen}
          path={path}
          me={me}
          onClose={() => setGrantOpen(false)}
          onSaved={() => {
            setGrantOpen(false);
            lv.reload();
          }}
        />
      )}
    </>
  );
}
