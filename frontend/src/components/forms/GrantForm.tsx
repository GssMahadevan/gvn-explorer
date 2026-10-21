import { useEffect } from 'react';
import { Checkbox, Form, InputNumber, Modal, Select, Space, Typography } from 'antd';
import { api, bits, grantLabel } from '../../api';
import { useFlash } from '../../flash';
import type { Grant, Me } from '../../types';
import PrincipalSelect from '../PrincipalSelect';
import TenantSelect from '../TenantSelect';

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
  // live values feeding the principal suggestions: the backend resolves
  // names inside the selected tenant, so the picker follows that scope.
  // For the pseudo-principals there is no name to resolve: "all" needs
  // nothing, "tenant" addresses exactly the tenant picked below.
  const ptype = (Form.useWatch('principal_type', form) ?? 'user') as
    | 'user'
    | 'group'
    | 'tenant'
    | 'all';
  const tenantSel = (Form.useWatch('tenant', form) as string) || me.tenant;

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
    principal_type: 'user' | 'group' | 'tenant' | 'all';
    tenant?: string;
    principal?: string;
    exp?: number | null;
    r: boolean;
    w: boolean;
    d: boolean;
  }) => {
    const body: Record<string, unknown> = {
      path,
      principal_type: v.principal_type,
      read: v.r,
      write: v.w,
      delete: v.d,
    };
    if (v.principal_type === 'all') {
      body.principal = '*'; // ignored server-side; the row means everyone
    } else if (v.principal_type === 'tenant') {
      body.tenant = v.tenant;
      body.principal = v.tenant;
    } else {
      body.tenant = v.tenant?.trim() || me.tenant;
      body.principal = (v.principal ?? '').trim();
    }
    if (v.exp) body.expires_days = v.exp;
    try {
      const g = await api<Grant>('/api/v1/perms', { method: 'PUT', body: JSON.stringify(body) });
      flash.ok(`grant saved: ${grantLabel(g)} on ${g.path} → ${bits(g)}`);
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
            data-testid="grant-ptype"
            options={[
              { value: 'user', label: 'user' },
              { value: 'group', label: 'group' },
              { value: 'tenant', label: 'all members of a tenant' },
              { value: 'all', label: 'all signed-in users' },
            ]}
          />
        </Form.Item>
        {ptype !== 'all' && (
          <Form.Item
            name="tenant"
            label={ptype === 'tenant' ? 'Target tenant' : 'Tenant'}
            extra={ptype === 'tenant' ? 'the grant reaches every member of this tenant' : undefined}
            rules={[{ required: true }]}
          >
            <TenantSelect me={me} testId="grant-tenant" />
          </Form.Item>
        )}
        {ptype !== 'all' && ptype !== 'tenant' && (
          <Form.Item
            name="principal"
            label="Principal name"
            extra={`suggestions come from ${ptype} names in ${tenantSel}`}
            rules={[{ required: true }]}
          >
            <PrincipalSelect
              kind={ptype}
              tenant={tenantSel}
              placeholder="user or group name in that tenant"
              testId="grant-principal"
            />
          </Form.Item>
        )}
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
