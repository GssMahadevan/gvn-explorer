import { useState } from 'react';
import { Button, Form, Input, Popconfirm, Select, Space, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { api, qs } from '../api';
import type { Group, Me } from '../types';
import { useListView } from '../useListView';
import { useFlash } from '../flash';
import ListSearch from '../components/ListSearch';
import ActiveFilterSelect from '../components/ActiveFilterSelect';
import GroupForm from '../components/forms/GroupForm';

// Groups panel: search/filter/sort, member management (inline add + tag
// remove), suspend/activate, delete. Creation and the description edit
// live in the GroupForm modal.
export default function GroupsPanel({ me }: { me: Me }) {
  const flash = useFlash();
  const lv = useListView<Group[]>((s) => `/api/v1/groups?${qs(s)}`, { sort: 'name' });
  const [memberForm] = Form.useForm();
  const [formFor, setFormFor] = useState<Group | 'new' | null>(null);
  const groups = lv.payload ?? [];

  const addMember = async (v: { gid?: number; username: string }) => {
    if (!v.gid) {
      flash.warn('pick a group first (listed on this page)');
      return;
    }
    try {
      await api(`/api/v1/groups/${v.gid}/members`, {
        method: 'POST',
        body: JSON.stringify({ username: v.username.trim() }),
      });
      flash.ok('member added');
      memberForm.setFieldValue('username', '');
      lv.reload();
    } catch (e) {
      flash.err((e as Error).message);
    }
  };

  const setActive = async (g: Group, active: boolean) => {
    try {
      await api(`/api/v1/groups/${g.id}/active`, {
        method: 'POST',
        body: JSON.stringify({ active }),
      });
      flash.ok(active ? 'group activated' : 'group suspended');
      lv.reload();
    } catch (e) {
      flash.err((e as Error).message);
    }
  };

  const columns: ColumnsType<Group> = [
    {
      title: 'tenant',
      key: 'tenant',
      sorter: true,
      sortOrder: lv.sortOrder('tenant'),
      showSorterTooltip: false,
      width: 110,
    },
    {
      title: 'group',
      key: 'name',
      dataIndex: 'name',
      sorter: true,
      sortOrder: lv.sortOrder('name'),
      showSorterTooltip: false,
      width: 170,
    },
    {
      title: 'active',
      key: 'active',
      sorter: true,
      sortOrder: lv.sortOrder('active'),
      showSorterTooltip: false,
      width: 100,
      render: (_, g) =>
        g.active === false ? <Tag color="red">suspended</Tag> : <Tag color="green">active</Tag>,
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
                  flash.ok('member removed');
                  lv.reload();
                } catch (err) {
                  flash.err((err as Error).message);
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
      width: 240,
      render: (_, g) => (
        <Space>
          <Button size="small" onClick={() => setFormFor(g)}>
            edit
          </Button>
          {g.active === false ? (
            <Button size="small" onClick={() => setActive(g, true)}>
              activate
            </Button>
          ) : (
            <Popconfirm
              title="Suspend? grants through this group become inert until reinstated."
              onConfirm={() => setActive(g, false)}
            >
              <Button size="small">suspend</Button>
            </Popconfirm>
          )}
          <Popconfirm
            title="delete group?"
            onConfirm={async () => {
              try {
                await api('/api/v1/groups/' + g.id, { method: 'DELETE' });
                flash.ok('group deleted');
                lv.reload();
              } catch (e) {
                flash.err((e as Error).message);
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
        <ActiveFilterSelect state={lv.state} patch={lv.patch} />
        <span className="grow" />
        <ListSearch
          state={lv.state}
          patch={lv.patch}
          cols={['name', 'tenant', 'description']}
          placeholder="search groups"
        />
        <Button type="primary" onClick={() => setFormFor('new')}>
          Create group
        </Button>
      </div>

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
        scroll={{ x: 'max-content' }}
        locale={{
          emptyText: lv.state.q ? `nothing matches "${lv.state.q}"` : 'no groups',
        }}
      />

      <GroupForm
        open={formFor !== null}
        editing={formFor === 'new' || formFor === null ? null : formFor}
        me={me}
        onClose={() => setFormFor(null)}
        onSaved={() => {
          setFormFor(null);
          lv.reload();
        }}
      />
    </>
  );
}
