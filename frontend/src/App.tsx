import { useEffect, useState } from 'react';
import { Button, Layout, Space, Spin, Tabs, Tag } from 'antd';
import type { TabsProps } from 'antd';
import { api } from './api';
import { FolderOpenOutlined } from '@ant-design/icons';
import type { Me } from './types';
import Login from './components/Login';
import FilesPanel from './panels/FilesPanel';
import PermsPanel from './panels/PermsPanel';
import UsersPanel from './panels/UsersPanel';
import GroupsPanel from './panels/GroupsPanel';
import TenantsPanel from './panels/TenantsPanel';

export default function App() {
  // undefined = booting, null = anonymous (login screen), Me = session
  const [me, setMe] = useState<Me | null | undefined>(undefined);
  const [cur, setCur] = useState('/'); // current directory, shared: files + perms panels
  const [tab, setTab] = useState('files'); // active tab (hooks: all before early returns)

  useEffect(() => {
    api<Me>('/api/v1/auth/me')
      .then(setMe)
      .catch(() => setMe(null));
  }, []);

  if (me === undefined)
    return <Spin size="large" style={{ display: 'block', margin: '25vh auto' }} />;
  if (me === null) return <Login onLogin={setMe} />;

  const isMgr = me.is_admin || me.is_super;
  const role = me.is_super ? 'super' : me.is_admin ? 'admin' : '';

  const logout = async () => {
    try {
      await api('/api/v1/auth/logout', { method: 'POST' });
    } catch {
      /* session is gone either way */
    }
    location.reload();
  };

  const items: NonNullable<TabsProps['items']> = [
    { key: 'files', label: 'Files', children: <FilesPanel cur={cur} setCur={setCur} /> },
    { key: 'perms', label: 'Permissions', children: <PermsPanel cur={cur} me={me} /> },
  ];
  if (isMgr) {
    items.push({ key: 'users', label: 'Users', children: <UsersPanel me={me} /> });
    items.push({ key: 'groups', label: 'Groups', children: <GroupsPanel me={me} /> });
  }
  if (me.is_super) items.push({ key: 'tenants', label: 'Tenants', children: <TenantsPanel /> });

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Layout.Header className="app-header">
        <span className="app-brand">
          <FolderOpenOutlined style={{ color: '#1677ff' }} />
          File Explorer
        </span>
        <Tabs
          activeKey={tab}
          onChange={setTab}
          items={items.map(({ key, label }) => ({ key, label }))}
          style={{ flex: 1, minWidth: 0 }}
          tabBarStyle={{ margin: 0 }}
        />
        <Space>
          <Tag color={me.is_super ? 'gold' : me.is_admin ? 'blue' : 'default'}>
            {me.tenant}/{me.username}
            {role ? ` (${role})` : ''}
          </Tag>
          <Button size="small" onClick={logout}>
            Logout
          </Button>
        </Space>
      </Layout.Header>
      {/* all panels stay mounted; only the active one is visible —
          panel state (page, search, sort) survives tab switches */}
      <Layout.Content style={{ padding: 16 }}>
        {items.map(({ key, children }) => (
          <div key={key} style={{ display: tab === key ? undefined : 'none' }}>
            {children}
          </div>
        ))}
      </Layout.Content>
    </Layout>
  );
}
