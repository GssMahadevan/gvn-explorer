import { useEffect } from 'react';
import { Form, Input, Modal } from 'antd';
import { api } from '../../api';
import { useFlash } from '../../flash';
import type { Group, Me } from '../../types';
import TenantSelect from '../TenantSelect';

// Create/update modal for a group.
//   create: tenant (super admin only) + name + description.
//   update: description only — membership is managed from the panel's
//   member row, the active flag from the row's suspend/activate action.
export default function GroupForm({
  open,
  editing,
  me,
  onClose,
  onSaved,
}: {
  open: boolean;
  editing: Group | null;
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
        ? { tenant: me.tenant, name: '', description: '' }
        : { tenant: editing.tenant, name: editing.name, description: editing.description ?? '' },
    );
  }, [open, editing, me.tenant, form, isCreate]);

  const submit = async (v: { tenant?: string; name: string; description?: string }) => {
    try {
      if (isCreate) {
        await api('/api/v1/groups', {
          method: 'POST',
          body: JSON.stringify({
            tenant: v.tenant?.trim() || me.tenant,
            name: v.name.trim(),
            description: v.description ?? '',
          }),
        });
        flash.ok(`created group ${(v.tenant?.trim() || me.tenant) + '/' + v.name.trim()}`);
      } else {
        await api('/api/v1/groups/' + editing.id, {
          method: 'PUT',
          body: JSON.stringify({ description: v.description ?? '' }),
        });
        flash.ok(`updated group ${editing.tenant}/${editing.name}`);
      }
      onSaved();
    } catch (e) {
      flash.err((e as Error).message);
    }
  };

  return (
    <Modal
      open={open}
      title={isCreate ? 'Create group' : `Edit group ${editing.name}`}
      okText={isCreate ? 'Create group' : 'Save'}
      onOk={() => form.submit()}
      onCancel={onClose}
      destroyOnHidden
    >
      <Form form={form} layout="vertical" onFinish={submit}>
        {isCreate && (
          <Form.Item name="tenant" label="Tenant" rules={[{ required: true }]}>
            <TenantSelect me={me} testId="group-tenant" />
          </Form.Item>
        )}
        {!isCreate && (
          <Form.Item label="Tenant">
            <Input value={editing.tenant} disabled />
          </Form.Item>
        )}
        <Form.Item name="name" label="Group name" rules={[{ required: true }]}>
          <Input disabled={!isCreate} />
        </Form.Item>
        <Form.Item
          name="description"
          label="Description"
          extra={isCreate ? undefined : 'members and suspend/activate stay on the row'}
        >
          <Input.TextArea rows={3} maxLength={500} showCount />
        </Form.Item>
      </Form>
    </Modal>
  );
}
