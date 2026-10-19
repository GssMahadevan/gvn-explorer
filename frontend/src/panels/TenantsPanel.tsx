import {
  Alert,
  App as AntApp,
  Button,
  Form,
  Input,
  Popconfirm,
  Table,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { api, qs } from '../api';
import type { Tenant } from '../types';
import { useListView } from '../useListView';
import ListSearch from '../components/ListSearch';

// Tenants panel (super admin only): create + delete. Deleting a tenant
// cascades its users, groups and every ACL grant targeting them.
export default function TenantsPanel() {
  const { message } = AntApp.useApp();
  const lv = useListView<Tenant[]>((s) => `/api/v1/tenants?${qs(s)}`, { sort: 'name' });
  const [form] = Form.useForm();
  const tenants = lv.payload ?? [];

  const createTenant = async (v: { name: string }) => {
    try {
      await api('/api/v1/tenants', { method: 'POST', body: JSON.stringify({ name: v.name.trim() }) });
      message.success('tenant created');
      form.resetFields();
      lv.reload();
    } catch (e) {
      message.error((e as Error).message);
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
    },
    {
      title: 'created',
      key: 'created',
      sorter: true,
      sortOrder: lv.sortOrder('created'),
      showSorterTooltip: false,
      width: 220,
      render: (_, t) => new Date(t.created_at).toLocaleString(),
    },
    {
      title: '',
      width: 100,
      render: (_, t) => (
        <Popconfirm
          title={`delete tenant ${t.name}?`}
          description="removes its users, groups AND all their ACL grants"
          onConfirm={async () => {
            try {
              await api('/api/v1/tenants/' + t.id, { method: 'DELETE' });
              message.success('tenant deleted');
              lv.reload();
            } catch (e) {
              message.error((e as Error).message);
            }
          }}
        >
          <Button size="small" danger>
            delete
          </Button>
        </Popconfirm>
      ),
    },
  ];

  return (
    <>
      <div className="bar">
        <span>tenants</span>
        <span className="grow" />
        <ListSearch state={lv.state} patch={lv.patch} placeholder="search tenants" />
      </div>

      <Form form={form} layout="inline" onFinish={createTenant} style={{ marginBottom: 8, rowGap: 8 }}>
        <Form.Item name="name" noStyle rules={[{ required: true }]}>
          <Input style={{ width: 200 }} placeholder="tenant name" />
        </Form.Item>
        <Button type="primary" htmlType="submit">
          Create tenant
        </Button>
      </Form>

      <Alert
        style={{ marginBottom: 8 }}
        type="info"
        message="deleting a tenant also deletes its users, groups and every ACL grant that targets them"
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
        locale={{
          emptyText: lv.state.q ? `nothing matches "${lv.state.q}"` : 'no tenants',
        }}
      />
    </>
  );
}
