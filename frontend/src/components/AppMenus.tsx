// Standard app-shell header menus: a brand menu (Help / About / Feedback)
// on the left and a user menu (Profile / Settings / Sign out) on the
// right. Only real API endpoints are called; Settings holds the one
// genuine UI preference we support (dark mode, persisted locally).
import { useState } from 'react';
import {
  Alert,
  App as AntApp,
  Avatar,
  Button,
  Descriptions,
  Dropdown,
  Form,
  Input,
  Modal,
  Space,
  Switch,
  Typography,
} from 'antd';
import type { MenuProps } from 'antd';
import {
  FolderOpenOutlined,
  InfoCircleOutlined,
  LogoutOutlined,
  MailOutlined,
  QuestionCircleOutlined,
  SettingOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { api } from '../api';
import type { Me } from '../types';

const roleLabel = (me: Me) =>
  me.is_super ? 'Super administrator' : me.is_admin ? 'Tenant administrator' : 'Member';

const FEEDBACK_MAILTO =
  'mailto:sgorti@gmail.com?subject=' + encodeURIComponent('[file-explorer] feedback');

// BrandMenu replaces the old static header wordmark: clicking it opens
// the usual About/Help/Feedback application menu.
export function BrandMenu() {
  const [modal, setModal] = useState<'about' | 'help' | null>(null);

  const items: MenuProps['items'] = [
    { key: 'help', icon: <QuestionCircleOutlined />, label: 'Help' },
    { key: 'about', icon: <InfoCircleOutlined />, label: 'About' },
    { key: 'feedback', icon: <MailOutlined />, label: 'Feedback' },
  ];
  const onClick: MenuProps['onClick'] = ({ key }) => {
    if (key === 'about' || key === 'help') setModal(key);
    else if (key === 'feedback') location.href = FEEDBACK_MAILTO;
  };

  return (
    <>
      <Dropdown trigger={['click']} menu={{ items, onClick }}>
        <span className="app-brand">
          <FolderOpenOutlined style={{ color: '#1677ff' }} />
          File Explorer
        </span>
      </Dropdown>

      <Modal
        title="About File Explorer"
        open={modal === 'about'}
        onCancel={() => setModal(null)}
        footer={null}
        width={480}
      >
        <Descriptions
          column={1}
          size="small"
          items={[
            { key: 'n', label: 'Application', children: 'File Explorer console' },
            { key: 'v', label: 'Version', children: '0.1.0' },
            {
              key: 'b',
              label: 'Backend',
              children: 'Go net/http + SQLite (permission metadata only)',
            },
            {
              key: 'f',
              label: 'Frontend',
              children: 'React 19 + Ant Design v6 (MIT licensed)',
            },
            {
              key: 'a',
              label: 'API',
              children: <span className="mono">/api/v1</span>,
            },
          ]}
        />
      </Modal>

      <Modal
        title="Help"
        open={modal === 'help'}
        onCancel={() => setModal(null)}
        footer={null}
        width={560}
      >
        <Typography>
          <Typography.Paragraph>
            <ul>
              <li>
                <b>Files</b>: browse the server-root sandbox; open, edit, create, upload, rename
                and delete anything your permissions allow.
              </li>
              <li>
                <b>Permissions</b>: grants are per path with user/group inheritance (nearest
                ancestor wins, user beats group, deny by default). Use a row's <b>perms</b> action
                to set access on a single file or folder. A grant may expire on a date — expired
                grants simply disappear.
              </li>
              <li>
                <b>Users</b>: accounts, including “device” users for IoT firmware pulls — those get
                a one-time API key instead of a password (shown once at creation).
              </li>
              <li>
                <b>Groups</b> and <b>Tenants</b>: group members share grants; tenants namespace
                usernames and group names.
              </li>
              <li>
                Every list supports search, column sorting and paging. Suspended or expired
                principals are blocked everywhere but never deleted.
              </li>
            </ul>
          </Typography.Paragraph>
        </Typography>
      </Modal>
    </>
  );
}

function ChangePassword({ me }: { me: Me }) {
  const { message } = AntApp.useApp();
  const [form] = Form.useForm();
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    const v = await form.validateFields();
    setBusy(true);
    try {
      await api(`/api/v1/users/${me.id}/password`, {
        method: 'POST',
        body: JSON.stringify({ password: v.password }),
      });
      message.success('password updated');
      form.resetFields();
    } catch (e) {
      message.error((e as Error).message);
    }
    setBusy(false);
  };

  return (
    <>
      <Typography.Title level={5}>Change password</Typography.Title>
      <Form form={form} layout="vertical" onFinish={submit}>
        <Form.Item
          name="password"
          label="New password"
          rules={[{ required: true, min: 6, message: 'min 6 chars' }]}
        >
          <Input.Password autoComplete="new-password" />
        </Form.Item>
        <Form.Item
          name="confirm"
          label="Confirm"
          dependencies={['password']}
          rules={[
            { required: true },
            ({ getFieldValue }) => ({
              validator: (_, v) =>
                !v || getFieldValue('password') === v
                  ? Promise.resolve()
                  : Promise.reject(new Error('passwords do not match')),
            }),
          ]}
        >
          <Input.Password autoComplete="new-password" />
        </Form.Item>
        <Button type="primary" htmlType="submit" loading={busy}>
          Update password
        </Button>
      </Form>
    </>
  );
}

export function UserMenu({
  me,
  onLogout,
  dark,
  setDark,
}: {
  me: Me;
  onLogout: () => void;
  dark: boolean;
  setDark: (v: boolean) => void;
}) {
  const [modal, setModal] = useState<'profile' | 'settings' | null>(null);

  const items: MenuProps['items'] = [
    {
      key: 'who',
      disabled: true,
      label: (
        <div>
          <b>
            {me.tenant}/{me.username}
          </b>
          <br />
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {roleLabel(me)}
          </Typography.Text>
        </div>
      ),
    },
    { type: 'divider' },
    { key: 'profile', icon: <UserOutlined />, label: 'Profile' },
    { key: 'settings', icon: <SettingOutlined />, label: 'Settings' },
    { type: 'divider' },
    { key: 'logout', icon: <LogoutOutlined />, label: 'Sign out', danger: true },
  ];
  const onClick: MenuProps['onClick'] = ({ key }) => {
    if (key === 'profile') setModal('profile');
    else if (key === 'settings') setModal('settings');
    else if (key === 'logout') onLogout();
  };

  return (
    <>
      <Dropdown trigger={['click']} menu={{ items, onClick }}>
        <Avatar
          className="app-user"
          style={{
            cursor: 'pointer',
            backgroundColor: me.is_super ? '#faad14' : me.is_admin ? '#1677ff' : '#64748b',
          }}
          icon={<UserOutlined />}
        />
      </Dropdown>

      <Modal
        title="Profile"
        open={modal === 'profile'}
        onCancel={() => setModal(null)}
        footer={null}
        width={480}
      >
        <Descriptions
          column={1}
          size="small"
          items={[
            { key: 't', label: 'Tenant', children: me.tenant },
            { key: 'u', label: 'Username', children: me.username },
            { key: 'r', label: 'Role', children: roleLabel(me) },
            { key: 'i', label: 'User ID', children: String(me.id) },
          ]}
        />
        <div style={{ marginTop: 16 }}>
          {me.is_admin || me.is_super ? (
            <ChangePassword me={me} />
          ) : (
            <Alert
              type="info"
              message="Ask your administrator to change your password."
            />
          )}
        </div>
      </Modal>

      <Modal
        title="Settings"
        open={modal === 'settings'}
        onCancel={() => setModal(null)}
        footer={null}
        width={400}
      >
        <Space style={{ width: '100%', justifyContent: 'space-between' }}>
          <span>Dark mode</span>
          <Switch checked={dark} onChange={setDark} />
        </Space>
      </Modal>
    </>
  );
}
