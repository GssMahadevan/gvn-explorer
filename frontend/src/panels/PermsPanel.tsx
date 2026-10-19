import {
  Alert,
  App as AntApp,
  Button,
  Checkbox,
  Form,
  Input,
  InputNumber,
  Popconfirm,
  Select,
  Space,
  Table,
  Tag,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { api, bits, qs } from '../api';
import type { Grant, Me, PermsList } from '../types';
import { useListView } from '../useListView';
import ListSearch from '../components/ListSearch';

// Permissions panel: grants on a target path (a directory — or one file via
// a row's "perms" action; nearest-ancestor inheritance applies server-side),
// effective-for-you summary, grant form (admin+), revoke.
export default function PermsPanel({ path, me }: { path: string; me: Me }) {
  const { message } = AntApp.useApp();
  const lv = useListView<PermsList>(
    (s) => `/api/v1/perms?path=${encodeURIComponent(path)}&${qs(s)}`,
    { sort: 'principal' },
  );
  const [form] = Form.useForm();
  const grants = lv.payload?.grants ?? [];
  const eff = lv.payload?.effective;
  const isMgr = me.is_admin || me.is_super;

  const addGrant = async (v: {
    principal_type: 'user' | 'group';
    tenant?: string;
    principal: string;
    exp?: number | null;
    r: boolean;
    w: boolean;
    d: boolean;
  }) => {
    const body: Record<string, unknown> = {
      path,
      principal_type: v.principal_type,
      principal: v.principal.trim(),
      tenant: v.tenant?.trim() || me.tenant,
      read: v.r,
      write: v.w,
      delete: v.d,
    };
    if (v.exp) body.expires_days = v.exp;
    try {
      await api('/api/v1/perms', { method: 'PUT', body: JSON.stringify(body) });
      message.success('grant saved');
      form.resetFields();
      lv.reload();
    } catch (e) {
      message.error((e as Error).message);
    }
  };

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
      width: 100,
      render: (_, g) => (
        <Popconfirm
          title="revoke this grant?"
          onConfirm={async () => {
            try {
              await api('/api/v1/perms/' + g.id, { method: 'DELETE' });
              message.success('grant revoked');
              lv.reload();
            } catch (e) {
              message.error((e as Error).message);
            }
          }}
        >
          <Button size="small" danger>
            revoke
          </Button>
        </Popconfirm>
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

      {isMgr && (
        <Form
          form={form}
          layout="inline"
          onFinish={addGrant}
          initialValues={{ principal_type: 'user', r: true, w: false, d: false }}
          style={{ marginBottom: 12, rowGap: 8 }}
        >
          <Form.Item name="principal_type" noStyle>
            <Select
              style={{ width: 100 }}
              options={[
                { value: 'user', label: 'user' },
                { value: 'group', label: 'group' },
              ]}
            />
          </Form.Item>
          <Form.Item name="tenant" noStyle>
            <Input style={{ width: 130 }} placeholder={`tenant (${me.tenant})`} />
          </Form.Item>
          <Form.Item name="principal" noStyle rules={[{ required: true }]}>
            <Input style={{ width: 150 }} placeholder="principal name" />
          </Form.Item>
          <Form.Item name="exp" noStyle>
            <InputNumber style={{ width: 100 }} min={0.1} placeholder="days" />
          </Form.Item>
          <Form.Item name="r" valuePropName="checked" noStyle>
            <Checkbox>r</Checkbox>
          </Form.Item>
          <Form.Item name="w" valuePropName="checked" noStyle>
            <Checkbox>w</Checkbox>
          </Form.Item>
          <Form.Item name="d" valuePropName="checked" noStyle>
            <Checkbox>d</Checkbox>
          </Form.Item>
          <Button type="primary" htmlType="submit">
            Grant
          </Button>
        </Form>
      )}

      <Table
        rowKey={(g) => g.id}
        size="small"
        columns={columns}
        dataSource={grants}
        loading={lv.loading}
        onChange={lv.onTableChange}
        pagination={lv.pagination}
        sticky
        locale={{
          emptyText: lv.state.q
            ? `nothing matches "${lv.state.q}"`
            : 'no grants on this exact path (inheritance may still apply)',
        }}
      />
    </>
  );
}
