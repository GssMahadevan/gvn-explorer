import { useEffect } from 'react';
import { Checkbox, Form, Input, InputNumber, Modal, Typography } from 'antd';
import { api } from '../../api';
import { useFlash } from '../../flash';
import type { Me, User } from '../../types';
import TenantSelect from '../TenantSelect';

// Create/update modal for a single user.
//   create (editing === null): full signup form (device users have a blank
//     password + an api-key factor).   update: only the description is
//     editable — the username, tenant and password have their own endpoints,
//     and the active flag is the row's suspend/activate action.
// onSaved reloads the list; onClose just dismisses.
export default function UserForm({
  open,
  editing,
  me,
  onClose,
  onSaved,
}: {
  open: boolean;
  editing: User | null;
  me: Me;
  onClose: () => void;
  onSaved: () => void;
}) {
  const flash = useFlash();
  const [form] = Form.useForm();
  const isCreate = editing === null;

  useEffect(() => {
    if (!open) return;
    form.setFieldsValue(
      isCreate
        ? { tenant: me.tenant, username: '', password: '', exp: null, admin: false, description: '' }
        : {
            tenant: editing.tenant,
            username: editing.username,
            description: editing.description ?? '',
          },
    );
  }, [open, editing, me.tenant, form, isCreate]);

  const submit = async (v: {
    tenant?: string;
    username: string;
    password?: string;
    exp?: number | null;
    admin?: boolean;
    description?: string;
  }) => {
    try {
      if (isCreate) {
        const body: Record<string, unknown> = {
          tenant: v.tenant?.trim() || me.tenant,
          username: v.username.trim(),
          password: v.password ?? '',
          is_admin: !!v.admin,
          description: v.description ?? '',
        };
        if (v.exp) body.expires_days = v.exp;
        await api('/api/v1/users', { method: 'POST', body: JSON.stringify(body) });
        flash.ok(`created user ${(v.tenant?.trim() || me.tenant) + '/' + v.username.trim()}`);
      } else {
        await api('/api/v1/users/' + editing.id, {
          method: 'PUT',
          body: JSON.stringify({ description: v.description ?? '' }),
        });
        flash.ok(`updated user ${editing.tenant}/${editing.username}`);
      }
      onSaved();
    } catch (e) {
      flash.err((e as Error).message);
    }
  };

  return (
    <Modal
      open={open}
      title={isCreate ? 'Create user' : `Edit user ${editing.username}`}
      okText={isCreate ? 'Create user' : 'Save'}
      onOk={() => form.submit()}
      onCancel={onClose}
      destroyOnHidden
    >
      <Form form={form} layout="vertical" onFinish={submit}>
        {isCreate && (
          <Form.Item name="tenant" label="Tenant" rules={[{ required: true }]}>
            <TenantSelect me={me} testId="user-tenant" />
          </Form.Item>
        )}
        {!isCreate && (
          <Form.Item label="Tenant">
            <Input value={editing.tenant} disabled />
          </Form.Item>
        )}
        <Form.Item name="username" label="Username" rules={[{ required: true }]}>
          <Input disabled={!isCreate} />
        </Form.Item>
        {isCreate && (
          <>
            <Form.Item name="password" label="Password" extra="blank = device user (api-key only)">
              <Input.Password autoComplete="new-password" />
            </Form.Item>
            <Form.Item name="exp" label="Expires in days">
              <InputNumber min={0.1} style={{ width: '100%' }} placeholder="blank = never" />
            </Form.Item>
            <Form.Item name="admin" valuePropName="checked">
              <Checkbox>Tenant admin</Checkbox>
            </Form.Item>
          </>
        )}
        <Form.Item
          name="description"
          label="Description"
          extra={isCreate ? undefined : 'username/tenant are fixed; password and suspend live on the row'}
        >
          <Input.TextArea rows={3} maxLength={500} showCount />
        </Form.Item>
        {isCreate && (
          <Typography.Text type="secondary">
            device users get an API key from the row's <b>apikey</b> action after creation.
          </Typography.Text>
        )}
      </Form>
    </Modal>
  );
}
