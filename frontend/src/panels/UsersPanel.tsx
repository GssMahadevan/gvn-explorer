import { useState } from 'react';
import {
  Alert,
  Button,
  Input,
  Modal,
  Space,
  Table,
  Tag,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { api, qs } from '../api';
import type { Me, User } from '../types';
import { useListView } from '../useListView';
import { useFlash } from '../flash';
import ListSearch from '../components/ListSearch';
import ActiveFilterSelect from '../components/ActiveFilterSelect';
import RowActions from '../components/RowActions';
import UserForm from '../components/forms/UserForm';

// Users panel: search/filter/sort, api-key provisioning (shown once),
// password set, suspend/activate kill switch, delete. Creation and the
// description edit live in the UserForm modal. Suspension keeps the row
// and its history — it is the kill switch, not delete.
export default function UsersPanel({ me }: { me: Me }) {
  const flash = useFlash();
  const lv = useListView<User[]>((s) => `/api/v1/users?${qs(s)}`, { sort: 'username' });
  const [formFor, setFormFor] = useState<User | 'new' | null>(null);
  const [keyShown, setKeyShown] = useState<{ username: string; key: string } | null>(null);
  const [pwFor, setPwFor] = useState<User | null>(null);
  const [pw, setPw] = useState('');
  const users = lv.payload ?? [];

  const provisionKey = async (u: User) => {
    try {
      const r = await api<{ key: string }>(`/api/v1/users/${u.id}/factors`, {
        method: 'POST',
        body: JSON.stringify({ method: 'apikey' }),
      });
      setKeyShown({ username: u.username, key: r.key });
    } catch (e) {
      flash.err((e as Error).message);
    }
  };

  const setActive = async (u: User, active: boolean) => {
    try {
      await api(`/api/v1/users/${u.id}/active`, {
        method: 'POST',
        body: JSON.stringify({ active }),
      });
      flash.ok(`${active ? 'activated' : 'suspended'} user ${u.tenant}/${u.username}`);
      lv.reload();
    } catch (e) {
      flash.err((e as Error).message);
    }
  };

  const savePw = async () => {
    if (!pwFor) return;
    try {
      await api(`/api/v1/users/${pwFor.id}/password`, {
        method: 'POST',
        body: JSON.stringify({ password: pw }),
      });
      flash.ok(`password set for ${pwFor.tenant}/${pwFor.username}`);
      setPwFor(null);
      setPw('');
    } catch (e) {
      flash.err((e as Error).message);
    }
  };

  const columns: ColumnsType<User> = [
    {
      title: 'tenant',
      key: 'tenant',
      sorter: true,
      sortOrder: lv.sortOrder('tenant'),
      showSorterTooltip: false,
      width: 110,
    },
    {
      title: 'username',
      key: 'username',
      dataIndex: 'username',
      sorter: true,
      sortOrder: lv.sortOrder('username'),
      showSorterTooltip: false,
      width: 180,
      render: (_, u) => (
        <Space size={4}>
          <span>{u.username}</span>
          {u.expired && <Tag color="orange">expired</Tag>}
          {!u.expired && u.expires_at && (
            <Tag color="orange">till {new Date(u.expires_at).toLocaleDateString()}</Tag>
          )}
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
      render: (_, u) =>
        u.active === false ? <Tag color="red">suspended</Tag> : <Tag color="green">active</Tag>,
    },
    {
      title: 'description',
      key: 'description',
      dataIndex: 'description',
      sorter: true,
      sortOrder: lv.sortOrder('description'),
      showSorterTooltip: false,
      ellipsis: true,
    },
    {
      title: 'admin',
      width: 70,
      render: (_, u) => (u.is_super ? <Tag color="gold">super</Tag> : u.is_admin ? 'yes' : ''),
    },
    {
      title: 'created',
      key: 'created',
      sorter: true,
      sortOrder: lv.sortOrder('created'),
      showSorterTooltip: false,
      width: 110,
      render: (_, u) => new Date(u.created_at).toLocaleDateString(),
    },

    {
      title: '',
      width: 56,
      render: (_, u) => (
        <RowActions
          actions={[
            { key: 'edit', label: 'edit', onClick: () => setFormFor(u) },
            { key: 'apikey', label: 'new API key', onClick: () => provisionKey(u) },
            { key: 'password', label: 'set password', onClick: () => setPwFor(u) },
            u.active === false
              ? { key: 'activate', label: 'activate', onClick: () => setActive(u, true) }
              : {
                  key: 'suspend',
                  label: 'suspend',
                  confirm: {
                    title: `suspend ${u.username}?`,
                    description:
                      'live sessions and API keys die immediately; the row and its history stay',
                  },
                  onClick: () => setActive(u, false),
                },
            {
              key: 'delete',
              label: 'delete',
              danger: true,
              confirm: { title: `delete user ${u.username}?` },
              onClick: async () => {
                try {
                  await api('/api/v1/users/' + u.id, { method: 'DELETE' });
                  flash.ok(`deleted user ${u.tenant}/${u.username}`);
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
        <span>users</span>
        <ActiveFilterSelect state={lv.state} patch={lv.patch} />
        <span className="grow" />
        <ListSearch
          state={lv.state}
          patch={lv.patch}
          cols={['username', 'tenant', 'description']}
          placeholder="search users"
        />
        <Button type="primary" onClick={() => setFormFor('new')}>
          Create user
        </Button>
      </div>

      <Table
        rowKey={(u) => u.id}
        size="small"
        columns={columns}
        dataSource={users}
        loading={lv.loading}
        onChange={lv.onTableChange}
        pagination={lv.pagination}
        sticky
        scroll={{ x: 'max-content' }}
        locale={{
          emptyText: lv.state.q ? `nothing matches "${lv.state.q}"` : 'no users',
        }}
      />

      <UserForm
        open={formFor !== null}
        editing={formFor === 'new' || formFor === null ? null : formFor}
        me={me}
        onClose={() => setFormFor(null)}
        onSaved={() => {
          setFormFor(null);
          lv.reload();
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
