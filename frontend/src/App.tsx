import { useEffect, useState } from 'react';
import { ConfigProvider, Layout, Spin, Tabs, theme as antdTheme } from 'antd';
import type { TabsProps } from 'antd';
import { api } from './api';
import type { Me } from './types';
import Login from './components/Login';
import { BrandMenu, UserMenu } from './components/AppMenus';
import FilesPanel from './panels/FilesPanel';
import PermsPanel from './panels/PermsPanel';
import UsersPanel from './panels/UsersPanel';
import GroupsPanel from './panels/GroupsPanel';
import TenantsPanel from './panels/TenantsPanel';

export default function App() {
  // undefined = booting, null = anonymous (login screen), Me = session
  const [me, setMe] = useState<Me | null | undefined>(undefined);
  const [cur, setCur] = useState('/'); // current directory, shared: files + perms panels
  const [permPath, setPermPath] = useState('/'); // path the perms panel targets (a row's "perms" can point it at a file)
  const [tab, setTab] = useState('files'); // active tab (hooks: all before early returns)
  const [dark, setDark] = useState(() => localStorage.getItem('fx.theme') === 'dark');

  useEffect(() => {
    api<Me>('/api/v1/auth/me')
      .then(setMe)
      .catch(() => setMe(null));
  }, []);

  useEffect(() => {
    localStorage.setItem('fx.theme', dark ? 'dark' : 'light');
  }, [dark]);

  if (me === undefined)
    return <Spin size="large" style={{ display: 'block', margin: '25vh auto' }} />;
  if (me === null) return <Login onLogin={setMe} />;

  const isMgr = me.is_admin || me.is_super;

  const logout = async () => {
    try {
      await api('/api/v1/auth/logout', { method: 'POST' });
    } catch {
      /* session is gone either way */
    }
    location.reload();
  };

  // navigating the browser follows the perms panel along (it shows grants
  // for the directory you are standing in)…
  const goto = (p: string) => {
    setCur(p);
    setPermPath(p);
  };
  // …unless a row's "perms" deliberately targets one specific file
  const showPerms = (p: string) => {
    setPermPath(p);
    setTab('perms');
  };

  const items: NonNullable<TabsProps['items']> = [
    {
      key: 'files',
      label: 'Files',
      children: <FilesPanel cur={cur} setCur={goto} showPerms={showPerms} />,
    },
    { key: 'perms', label: 'Permissions', children: <PermsPanel path={permPath} me={me} /> },
  ];
  if (isMgr) {
    items.push({ key: 'users', label: 'Users', children: <UsersPanel me={me} /> });
    items.push({ key: 'groups', label: 'Groups', children: <GroupsPanel me={me} /> });
  }
  if (me.is_super) items.push({ key: 'tenants', label: 'Tenants', children: <TenantsPanel /> });

  return (
    <ConfigProvider
      theme={{
        algorithm: dark ? antdTheme.darkAlgorithm : antdTheme.defaultAlgorithm,
        components: {
          // the header is dark navy in both themes, but default Tabs labels
          // use the light-theme colorText (near-black) and turn invisible on
          // it — only the active tab (primary blue) shows. force light text;
          // the header nav is the only Tabs instance in the app.
          Tabs: {
            itemColor: 'rgba(255, 255, 255, 0.72)',
            itemHoverColor: '#ffffff',
            itemSelectedColor: '#ffffff',
          },
        },
      }}
    >
      <Layout style={{ minHeight: '100vh' }}>
        <Layout.Header className="app-header">
          <BrandMenu />
          <Tabs
            activeKey={tab}
            onChange={setTab}
            items={items.map(({ key, label }) => ({ key, label }))}
            style={{ flex: 1, minWidth: 0 }}
            tabBarStyle={{ margin: 0 }}
          />
          <UserMenu me={me} onLogout={logout} dark={dark} setDark={setDark} />
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
    </ConfigProvider>
  );
}
