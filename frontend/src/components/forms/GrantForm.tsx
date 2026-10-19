import { useEffect } from 'react';
import { Checkbox, Form, Input, InputNumber, Modal, Select, Space, Typography } from 'antd';
import { api } from '../../api';
import { useFlash } from '../../flash';
import type { Me } from '../../types';

// Grant modal for one path: PUT /api/v1/perms is an upsert, so changing a
// grant = save it again (revoke stays the row's action). The target path
// is fixed by the panel (the browsed directory or a row's file).
export default function GrantForm({
  open,
  path,
  me,
  onClose,
  onSaved,
}: {
  open: boolean;
  path: string;
  me: Me;
  onClose: () => void;
  onSaved: () => void;
}) {
  const flash = useFlash();
  const [form] = Form.useForm();

  useEffect(() => {
    if (!open) return;
    form.setFieldsValue({
      principal_type: 'user',
      tenant: me.tenant,
      principal: '',
      exp: null,
      r: true,
      w: false,
      d: false,
    });
  }, [open, me.tenant, form]);

  const submit = async (v: {
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
      flash.ok('grant saved');
      onSaved();
    } catch (e) {
      flash.err((e as Error).message);
    }
  };

  return (
    <Modal
      open={open}
      title="Grant access"
      okText="Save grant"
      onOk={() => form.submit()}
      onCancel={onClose}
      destroyOnHidden
    >
      <Typography.Paragraph>
        grants apply to <Typography.Text code>{path}</Typography.Text> and inherit down; all bits
        off <b>excludes</b> the subtree for that principal.
      </Typography.Paragraph>
      <Form form={form} layout="vertical" onFinish={submit}>
        <Form.Item name="principal_type" label="Principal type">
          <Select
            options={[
              { value: 'user', label: 'user' },
              { value: 'group', label: 'group' },
            ]}
          />
        </Form.Item>
        <Form.Item name="tenant" label="Tenant" rules={[{ required: true }]}>
          <Input placeholder={`defaults to ${me.tenant}`} />
        </Form.Item>
        <Form.Item name="principal" label="Principal name" rules={[{ required: true }]}>
          <Input placeholder="user or group name in that tenant" />
        </Form.Item>
        <Form.Item name="exp" label="Expires in days">
          <InputNumber min={0.1} style={{ width: '100%' }} placeholder="blank = never" />
        </Form.Item>
        <Form.Item label="Permissions">
          <Space size={16}>
            <Form.Item name="r" valuePropName="checked" noStyle>
              <Checkbox>read</Checkbox>
            </Form.Item>
            <Form.Item name="w" valuePropName="checked" noStyle>
              <Checkbox>write</Checkbox>
            </Form.Item>
            <Form.Item name="d" valuePropName="checked" noStyle>
              <Checkbox>delete</Checkbox>
            </Form.Item>
          </Space>
        </Form.Item>
      </Form>
    </Modal>
  );
}
