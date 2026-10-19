import { useState } from 'react';
import {
  Alert,
  App as AntApp,
  Button,
  Checkbox,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Space,
  Table,
  Tag,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { api, qs } from '../api';
import type { Me, User } from '../types';
import { useListView } from '../useListView';
import ListSearch from '../components/ListSearch';

// Users panel: create, api-key provisioning (shown once), password set,
// suspend/activate kill switch, delete. Suspension keeps row + history.
export default function UsersPanel({ me }: { me: Me }) {
  const { message } = AntApp.useApp();
  const lv = useListView<User[]>((s) => `/api/v1/users?${qs(s)}`, { sort: 'username' });
  const [form] = Form.useForm();
  const [keyShown, setKeyShown] = useState<{ username: string; key: string } | null>(null);
  const [pwFor, setPwFor] = useState<User | null>(null);
  const [pw, setPw] = useState('');
  const users = lv.payload ?? [];
  const isSuper = me.is_super;

  const createUser = async (v: {
    tenant?: string;
    username: string;
    password?: string;
    exp?: number | null;
    admin?: boolean;
  }) => {
    const body: Record<string, unknown> = {
      tenant: v.tenant?.trim() || me.tenant,
      username: v.username.trim(),
      password: v.password ?? '',
      is_admin: !!v.admin,
    };
    if (v.exp) body.expires_days = v.exp;
    try {
      await api('/api/v1/users', { method: 'POST', body: JSON.stringify(body) });
      message.success('user created');
      form.resetFields();
      lv.reload();
    } catch (e) {
      message.error((e as Error).message);
    }
  };

  const provisionKey = async (u: User) => {
    try {
      const r = await api<{ key: string }>(`/api/v1/users/${u.id}/factors`, {
        method: 'POST',
        body: JSON.stringify({ method: 'apikey' }),
      });
      setKeyShown({ username: u.username, key: r.key });
    } catch (e) {
      message.error((e as Error).message);
    }
  };

  const setActive = async (u: User, active: boolean) => {
    try {
      await api(`/api/v1/users/${u.id}/active`, {
        method: 'POST',
        body: JSON.stringify({ active }),
      });
      message.success(active ? 'user activated' : 'user suspended');
      lv.reload();
    } catch (e) {
      message.error((e as Error).message);
    }
  };

  const savePw = async () => {
    if (!pwFor) return;
    try {
      await api(`/api/v1/users/${pwFor.id}/password`, {
        method: 'POST',
        body: JSON.stringify({ password: pw }),
      });
      message.success('password updated');
      setPwFor(null);
      setPw('');
    } catch (e) {
      message.error((e as Error).message);
    }
  };

  const columns: ColumnsType<User> = [
    {
      title: 'tenant',
      key: 'tenant',
      sorter: true,
      sortOrder: lv.sortOrder('tenant'),
      showSorterTooltip: false,
      width: 140,
    },
    {
      title: 'username',
      key: 'username',
      dataIndex: 'username',
      sorter: true,
      sortOrder: lv.sortOrder('username'),
      showSorterTooltip: false,
      render: (_, u) => (
        <Space size={4}>
          <span>{u.username}</span>
          {u.active === false && <Tag color="red">inactive</Tag>}
          {u.expired && <Tag color="orange">expired</Tag>}
          {!u.expired && u.expires_at && (
            <Tag color="orange">till {new Date(u.expires_at).toLocaleDateString()}</Tag>
          )}
        </Space>
      ),
    },
    {
      title: 'admin',
      width: 80,
      render: (_, u) => (u.is_super ? <Tag color="gold">super</Tag> : u.is_admin ? 'yes' : ''),
    },
    {
      title: 'created',
      key: 'created',
      sorter: true,
      sortOrder: lv.sortOrder('created'),
      showSorterTooltip: false,
      width: 130,
      render: (_, u) => new Date(u.created_at).toLocaleDateString(),
    },
    {
      title: '',
      width: 330,
      render: (_, u) => (
        <Space>
          <Button size="small" title="manage API keys for headless devices" onClick={() => provisionKey(u)}>
            apikey
          </Button>
          <Button size="small" title="set a new password" onClick={() => setPwFor(u)}>
            password
          </Button>
          {u.active === false ? (
            <Button size="small" onClick={() => setActive(u, true)}>
              activate
            </Button>
          ) : (
            <Popconfirm
              title="Suspend? live sessions and API keys die immediately; the row and history stay."
              onConfirm={() => setActive(u, false)}
            >
              <Button size="small">suspend</Button>
            </Popconfirm>
          )}
          <Popconfirm
            title={`delete user ${u.username}?`}
            onConfirm={async () => {
              try {
                await api('/api/v1/users/' + u.id, { method: 'DELETE' });
                message.success('user deleted');
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
        </Space>
      ),
    },
  ];

  return (
    <>
      <div className="bar">
        <span>users</span>
        <span className="grow" />
        <ListSearch
          state={lv.state}
          patch={lv.patch}
          cols={['username', 'tenant']}
          placeholder="search users"
        />
      </div>

      <Form
        form={form}
        layout="inline"
        onFinish={createUser}
        style={{ marginBottom: 12, rowGap: 8 }}
      >
        {isSuper && (
          <Form.Item name="tenant" noStyle>
            <Input style={{ width: 120 }} placeholder={`tenant (${me.tenant})`} />
          </Form.Item>
        )}
        <Form.Item name="username" noStyle rules={[{ required: true }]}>
          <Input style={{ width: 150 }} placeholder="username" />
        </Form.Item>
        <Form.Item name="password" noStyle>
          <Input.Password style={{ width: 190 }} placeholder="password (blank = device user)" />
        </Form.Item>
        <Form.Item name="exp" noStyle>
          <InputNumber style={{ width: 120 }} min={0.1} placeholder="expires in days" />
        </Form.Item>
        <Form.Item name="admin" valuePropName="checked" noStyle>
          <Checkbox>admin</Checkbox>
        </Form.Item>
        <Button type="primary" htmlType="submit">
          Create user
        </Button>
      </Form>

      <Table
        rowKey={(u) => u.id}
        size="small"
        columns={columns}
        dataSource={users}
        loading={lv.loading}
        onChange={lv.onTableChange}
        pagination={lv.pagination}
        sticky
        locale={{
          emptyText: lv.state.q ? `nothing matches "${lv.state.q}"` : 'no users',
        }}
      />

      <Modal
        open={keyShown !== null}
        title={`API key for ${keyShown?.username}`}
        footer={
          <Button type="primary" onClick={() => setKeyShown(null)}>
            Done
          </Button>
        }
        onCancel={() => setKeyShown(null)}
      >
        <Alert
          type="warning"
          style={{ marginBottom: 12 }}
          message="This key is shown ONCE — store it in the device now."
        />
        <Typography.Paragraph copyable={{ text: keyShown?.key }} className="mono">
          {keyShown?.key}
        </Typography.Paragraph>
      </Modal>

      <Modal
        open={pwFor !== null}
        title={`New password for ${pwFor?.username}`}
        okText="Set password"
        onOk={savePw}
        onCancel={() => setPwFor(null)}
      >
        <Input.Password
          autoFocus
          value={pw}
          onChange={(e) => setPw(e.target.value)}
          onPressEnter={savePw}
        />
      </Modal>
    </>
  );
}
