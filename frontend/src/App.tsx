import { lazy, Suspense, useEffect, useState } from 'react';
import { App as AntApp, ConfigProvider, Layout, Spin, Tabs, theme as antdTheme } from 'antd';
import type { TabsProps } from 'antd';
import { api } from './api';
import type { Me } from './types';
import { FlashBar, FlashProvider } from './flash';
import { notifyThemeChange } from './theme';
import Login from './components/Login';
import { BrandMenu, UserMenu } from './components/AppMenus';
import FilesPanel from './panels/FilesPanel';
import PermsPanel from './panels/PermsPanel';
import UsersPanel from './panels/UsersPanel';
import GroupsPanel from './panels/GroupsPanel';
import TenantsPanel from './panels/TenantsPanel';
import EventsPanel from './panels/EventsPanel';
import type { FileTarget } from './components/FilePane';

// the file page pulls in CodeMirror + markdown rendering; lazy so the
// base bundle stays lean and this only downloads when a file is opened
const FilePane = lazy(() => import('./components/FilePane'));

export default function App() {
  // undefined = booting, null = anonymous (login screen), Me = session
  const [me, setMe] = useState<Me | null | undefined>(undefined);
  const [cur, setCur] = useState('/'); // current directory, shared: files + perms panels
  const [permPath, setPermPath] = useState('/'); // path the perms panel targets (a row's "perms" can point it at a file)
  const [tab, setTab] = useState('files'); // active tab (hooks: all before early returns)
  const [dark, setDark] = useState(() => localStorage.getItem('fx.theme') === 'dark');
  // the file page: null = not viewing a file. view/edit mode lives here so
  // the Files row ⋮ menu can open either directly (the name opens 'view').
  const [fileTarget, setFileTarget] = useState<FileTarget | null>(null);
  const [filesEpoch, setFilesEpoch] = useState(0); // bump -> FilesPanel reloads after file writes

  useEffect(() => {
    api<Me>('/api/v1/auth/me')
      .then(setMe)
      .catch(() => setMe(null));
  }, []);

  useEffect(() => {
    localStorage.setItem('fx.theme', dark ? 'dark' : 'light');
    notifyThemeChange(); // CodeMirror chrome follows the antd theme
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
      children: (
        <FilesPanel cur={cur} setCur={goto} showPerms={showPerms} openFile={setFileTarget} filesEpoch={filesEpoch} />
      ),
    },
    { key: 'perms', label: 'Permissions', children: <PermsPanel path={permPath} me={me} /> },
  ];
  if (isMgr) {
    items.push({ key: 'users', label: 'Users', children: <UsersPanel me={me} /> });
    items.push({ key: 'groups', label: 'Groups', children: <GroupsPanel me={me} /> });
    items.push({ key: 'events', label: 'Events', children: <EventsPanel me={me} visible={tab === 'events' && !fileTarget} /> });
  }
  if (me.is_super)
    items.push({ key: 'tenants', label: 'Tenants', children: <TenantsPanel me={me} /> });

  return (
    <FlashProvider>
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
        <AntApp component={false}>
        <Layout style={{ minHeight: '100vh' }}>
          <Layout.Header className="app-header">
            <BrandMenu onHelp={() => setFileTarget({ path: '/help/README.md', mode: 'view' })} />
            <Tabs
              activeKey={tab}
              onChange={(k) => {
                setFileTarget(null); // switching tabs leaves the file page
                setTab(k);
              }}
              items={items.map(({ key, label }) => ({ key, label }))}
              style={{ flex: 1, minWidth: 0 }}
              tabBarStyle={{ margin: 0 }}
            />
            <UserMenu me={me} onLogout={logout} dark={dark} setDark={setDark} />
          </Layout.Header>
          <FlashBar me={me} />
          {/* all panels stay mounted; only the active one is visible —
              panel state (page, search, sort) survives tab switches.
              Opening a file hides them (still mounted) and shows the file
              page in their place. */}
          <Layout.Content style={{ padding: 16 }}>
            {items.map(({ key, children }) => (
              <div key={key} style={{ display: fileTarget || tab !== key ? 'none' : undefined }}>
                {children}
              </div>
            ))}
            {fileTarget && (
              <Suspense fallback={<Spin style={{ display: 'block', margin: '60px auto' }} />}>
                <FilePane
                  target={fileTarget}
                  onClose={() => setFileTarget(null)}
                  onSaved={() => setFilesEpoch((n) => n + 1)}
                  openDoc={(p) => setFileTarget({ path: p, mode: 'view' })}
                />
              </Suspense>
            )}
          </Layout.Content>
        </Layout>
        </AntApp>
      </ConfigProvider>
    </FlashProvider>
  );
}
