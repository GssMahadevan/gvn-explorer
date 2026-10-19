import { useState } from 'react';
import { Alert, Button, Space, Table, Tag } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { api, qs } from '../api';
import type { Me, Tenant } from '../types';
import { useListView } from '../useListView';
import { useFlash } from '../flash';
import ListSearch from '../components/ListSearch';
import ActiveFilterSelect from '../components/ActiveFilterSelect';
import RowActions from '../components/RowActions';
import TenantForm from '../components/forms/TenantForm';

// Tenants panel (super admin only): create/edit (description), the
// tenant-wide suspend-instead-of-delete kill switch, and delete.
// Suspending a tenant kills its logins, live sessions and API keys at
// once while every row survives; deleting cascades users, groups and
// all ACL grants targeting them.
export default function TenantsPanel({ me }: { me: Me }) {
  const flash = useFlash();
  const lv = useListView<Tenant[]>((s) => `/api/v1/tenants?${qs(s)}`, { sort: 'name' });
  const [formFor, setFormFor] = useState<Tenant | 'new' | null>(null);
  const tenants = lv.payload ?? [];

  const setActive = async (t: Tenant, active: boolean) => {
    try {
      await api(`/api/v1/tenants/${t.id}/active`, {
        method: 'POST',
        body: JSON.stringify({ active }),
      });
      flash.ok(active ? 'tenant activated' : 'tenant suspended');
      lv.reload();
    } catch (e) {
      flash.err((e as Error).message);
    }
  };

  const columns: ColumnsType<Tenant> = [
    {
      title: 'name',
      key: 'name',
      dataIndex: 'name',
      sorter: true,
      sortOrder: lv.sortOrder('name'),
      showSorterTooltip: false,
      width: 180,
      render: (_, t) => (
        <Space size={4}>
          <span>{t.name}</span>
          {t.name === me.tenant && <Tag>your tenant</Tag>}
        </Space>
      ),
    },
    {
      title: 'active',
      key: 'active',
      sorter: true,
      sortOrder: lv.sortOrder('active'),
      showSorterTooltip: false,
      width: 100,
      render: (_, t) =>
        t.active === false ? <Tag color="red">suspended</Tag> : <Tag color="green">active</Tag>,
    },
    {
      title: 'description',
      key: 'description',
      dataIndex: 'description',
      showSorterTooltip: false,
      ellipsis: true,
    },
    {
      title: 'created',
      key: 'created',
      sorter: true,
      sortOrder: lv.sortOrder('created'),
      showSorterTooltip: false,
      width: 180,
      render: (_, t) => new Date(t.created_at).toLocaleString(),
    },

    {
      title: '',
      width: 56,
      render: (_, t) => (
        <RowActions
          actions={[
            { key: 'edit', label: 'edit', onClick: () => setFormFor(t) },
            t.active === false
              ? { key: 'activate', label: 'activate', onClick: () => setActive(t, true) }
              : {
                  key: 'suspend',
                  label: 'suspend',
                  disabled: t.name === me.tenant,
                  confirm: {
                    title: `suspend tenant ${t.name}?`,
                    description: 'its logins, live sessions and API keys die at once; all rows stay',
                  },
                  onClick: () => setActive(t, false),
                },
            {
              key: 'delete',
              label: 'delete',
              danger: true,
              confirm: {
                title: `delete tenant ${t.name}?`,
                description: 'removes its users, groups AND all their ACL grants',
              },
              onClick: async () => {
                try {
                  await api('/api/v1/tenants/' + t.id, { method: 'DELETE' });
                  flash.ok('tenant deleted');
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
        <span>tenants</span>
        <ActiveFilterSelect state={lv.state} patch={lv.patch} />
        <span className="grow" />
        <ListSearch
          state={lv.state}
          patch={lv.patch}
          cols={['name', 'description']}
          placeholder="search tenants"
        />
        <Button type="primary" onClick={() => setFormFor('new')}>
          Create tenant
        </Button>
      </div>

      <Alert
        style={{ marginBottom: 8 }}
        type="info"
        message="suspend kills logins/sessions/API keys of a tenant without touching any data; deleting removes its users, groups and every ACL grant that targets them"
      />

      <Table
        rowKey={(t) => t.id}
        size="small"
        columns={columns}
        dataSource={tenants}
        loading={lv.loading}
        onChange={lv.onTableChange}
        pagination={lv.pagination}
        sticky
        scroll={{ x: 'max-content' }}
        locale={{
          emptyText: lv.state.q ? `nothing matches "${lv.state.q}"` : 'no tenants',
        }}
      />

      <TenantForm
        open={formFor !== null}
        editing={formFor === 'new' || formFor === null ? null : formFor}
        onClose={() => setFormFor(null)}
        onSaved={() => {
          setFormFor(null);
          lv.reload();
        }}
      />
    </>
  );
}
