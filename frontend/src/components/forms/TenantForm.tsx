import { useEffect } from 'react';
import { Form, Input, Modal } from 'antd';
import { api } from '../../api';
import { useFlash } from '../../flash';
import type { Tenant } from '../../types';

// Create/update modal for a tenant (super admin panel).
//   create: name + description.   update: description only — suspending
//   a tenant (its kill switch) is the row's action, deletion too.
export default function TenantForm({
  open,
  editing,
  onClose,
  onSaved,
}: {
  open: boolean;
  editing: Tenant | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const flash = useFlash();
  const [form] = Form.useForm();
  const isCreate = editing === null;

  useEffect(() => {
    if (!open) return;
    form.setFieldsValue(
      isCreate ? { name: '', description: '' } : { name: editing.name, description: editing.description ?? '' },
    );
  }, [open, editing, form, isCreate]);

  const submit = async (v: { name: string; description?: string }) => {
    try {
      if (isCreate) {
        await api('/api/v1/tenants', {
          method: 'POST',
          body: JSON.stringify({ name: v.name.trim(), description: v.description ?? '' }),
        });
        flash.ok('tenant created');
      } else {
        await api('/api/v1/tenants/' + editing.id, {
          method: 'PUT',
          body: JSON.stringify({ description: v.description ?? '' }),
        });
        flash.ok('tenant updated');
      }
      onSaved();
    } catch (e) {
      flash.err((e as Error).message);
    }
  };

  return (
    <Modal
      open={open}
      title={isCreate ? 'Create tenant' : `Edit tenant ${editing.name}`}
      okText={isCreate ? 'Create tenant' : 'Save'}
      onOk={() => form.submit()}
      onCancel={onClose}
      destroyOnHidden
    >
      <Form form={form} layout="vertical" onFinish={submit}>
        <Form.Item name="name" label="Tenant name" rules={[{ required: true }]}>
          <Input disabled={!isCreate} />
        </Form.Item>
        <Form.Item
          name="description"
          label="Description"
          extra={isCreate ? 'free-text context for operators' : 'suspend and delete stay on the row'}
        >
          <Input.TextArea rows={3} maxLength={500} showCount />
        </Form.Item>
      </Form>
    </Modal>
  );
}
