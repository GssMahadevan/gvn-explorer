import {
  App as AntApp,
  Button,
  Form,
  Input,
  Popconfirm,
  Select,
  Space,
  Table,
  Tag,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { api, qs } from '../api';
import type { Group, Me } from '../types';
import { useListView } from '../useListView';
import ListSearch from '../components/ListSearch';

// Groups panel: create, suspend/activate, delete, member list with
// inline add (group select + username) and tag-close removal.
export default function GroupsPanel({ me }: { me: Me }) {
  const { message } = AntApp.useApp();
  const lv = useListView<Group[]>((s) => `/api/v1/groups?${qs(s)}`, { sort: 'name' });
  const [createForm] = Form.useForm();
  const [memberForm] = Form.useForm();
  const groups = lv.payload ?? [];
  const isSuper = me.is_super;

  const createGroup = async (v: { tenant?: string; name: string }) => {
    try {
      await api('/api/v1/groups', {
        method: 'POST',
        body: JSON.stringify({ tenant: v.tenant?.trim() || me.tenant, name: v.name.trim() }),
      });
      message.success('group created');
      createForm.resetFields();
      lv.reload();
    } catch (e) {
      message.error((e as Error).message);
    }
  };

  const addMember = async (v: { gid?: number; username: string }) => {
    if (!v.gid) {
      message.warning('pick a group first (listed on this page)');
      return;
    }
    try {
      await api(`/api/v1/groups/${v.gid}/members`, {
        method: 'POST',
        body: JSON.stringify({ username: v.username.trim() }),
      });
      message.success('member added');
      memberForm.setFieldValue('username', '');
      lv.reload();
    } catch (e) {
      message.error((e as Error).message);
    }
  };

  const columns: ColumnsType<Group> = [
    {
      title: 'tenant',
      key: 'tenant',
      sorter: true,
      sortOrder: lv.sortOrder('tenant'),
      showSorterTooltip: false,
      width: 140,
    },
    {
      title: 'group',
      key: 'name',
      dataIndex: 'name',
      sorter: true,
      sortOrder: lv.sortOrder('name'),
      showSorterTooltip: false,
      width: 200,
      render: (_, g) => (
        <Space size={4}>
          <span>{g.name}</span>
          {g.active === false && <Tag color="red">inactive</Tag>}
        </Space>
      ),
    },
    {
      title: 'members',
      render: (_, g) =>
        (g.members ?? []).length === 0 ? (
          <Typography.Text type="secondary">none</Typography.Text>
        ) : (
          (g.members ?? []).map((m) => (
            <Tag
              key={m.id}
              closable
              onClose={async (e) => {
                e.preventDefault();
                try {
                  await api(`/api/v1/groups/${g.id}/members/${m.id}`, { method: 'DELETE' });
                  message.success('member removed');
                  lv.reload();
                } catch (err) {
                  message.error((err as Error).message);
                }
              }}
            >
              {m.username}
            </Tag>
          ))
        ),
    },
    {
      title: '',
      width: 170,
      render: (_, g) => (
        <Space>
          {g.active === false ? (
            <Button size="small" onClick={async () => {
              try {
                await api(`/api/v1/groups/${g.id}/active`, { method: 'POST', body: JSON.stringify({ active: true }) });
                message.success('group activated');
                lv.reload();
              } catch (e) {
                message.error((e as Error).message);
              }
            }}>
              activate
            </Button>
          ) : (
            <Popconfirm
              title="Suspend? grants through this group become inert until reinstated."
              onConfirm={async () => {
                try {
                  await api(`/api/v1/groups/${g.id}/active`, { method: 'POST', body: JSON.stringify({ active: false }) });
                  message.success('group suspended');
                  lv.reload();
                } catch (e) {
                  message.error((e as Error).message);
                }
              }}
            >
              <Button size="small">suspend</Button>
            </Popconfirm>
          )}
          <Popconfirm
            title="delete group?"
            onConfirm={async () => {
              try {
                await api('/api/v1/groups/' + g.id, { method: 'DELETE' });
                message.success('group deleted');
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
        <span>groups</span>
        <span className="grow" />
        <ListSearch
          state={lv.state}
          patch={lv.patch}
          cols={['name', 'tenant']}
          placeholder="search groups"
        />
      </div>

      <Form form={createForm} layout="inline" onFinish={createGroup} style={{ marginBottom: 8, rowGap: 8 }}>
        {isSuper && (
          <Form.Item name="tenant" noStyle>
            <Input style={{ width: 120 }} placeholder={`tenant (${me.tenant})`} />
          </Form.Item>
        )}
        <Form.Item name="name" noStyle rules={[{ required: true }]}>
          <Input style={{ width: 180 }} placeholder="group name" />
        </Form.Item>
        <Button type="primary" htmlType="submit">
          Create group
        </Button>
      </Form>

      <Form form={memberForm} layout="inline" onFinish={addMember} style={{ marginBottom: 12, rowGap: 8 }}>
        <Form.Item name="gid" noStyle>
          <Select
            style={{ width: 230 }}
            placeholder="group (on this page)"
            options={groups.map((g) => ({ value: g.id, label: `${g.tenant}/${g.name}` }))}
          />
        </Form.Item>
        <Form.Item name="username" noStyle rules={[{ required: true }]}>
          <Input style={{ width: 150 }} placeholder="username" />
        </Form.Item>
        <Button type="primary" htmlType="submit">
          Add member
        </Button>
      </Form>

      <Table
        rowKey={(g) => g.id}
        size="small"
        columns={columns}
        dataSource={groups}
        loading={lv.loading}
        onChange={lv.onTableChange}
        pagination={lv.pagination}
        sticky
        locale={{
          emptyText: lv.state.q ? `nothing matches "${lv.state.q}"` : 'no groups',
        }}
      />
    </>
  );
}
