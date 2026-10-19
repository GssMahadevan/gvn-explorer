import { useState } from 'react';
import { Button, Card, Form, Input, Typography } from 'antd';
import { api } from '../api';
import type { Me } from '../types';

export default function Login({ onLogin }: { onLogin: (me: Me) => void }) {
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);

  const submit = async (v: { tenant: string; username: string; password: string }) => {
    setBusy(true);
    setErr('');
    try {
      onLogin(await api<Me>('/api/v1/auth/login', { method: 'POST', body: JSON.stringify(v) }));
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div style={{ display: 'flex', justifyContent: 'center', marginTop: '12vh' }}>
      <Card title="File Explorer — sign in" style={{ width: 360 }}>
        <Form layout="vertical" onFinish={submit} requiredMark={false}>
          <Form.Item name="tenant" label="Tenant" rules={[{ required: true }]}>
            <Input autoComplete="organization" />
          </Form.Item>
          <Form.Item name="username" label="Username" rules={[{ required: true }]}>
            <Input autoComplete="username" />
          </Form.Item>
          <Form.Item name="password" label="Password" rules={[{ required: true }]}>
            <Input.Password autoComplete="current-password" />
          </Form.Item>
          {err && (
            <Typography.Paragraph type="danger">
              {err}
            </Typography.Paragraph>
          )}
          <Button type="primary" htmlType="submit" block loading={busy}>
            Login
          </Button>
        </Form>
      </Card>
    </div>
  );
}
