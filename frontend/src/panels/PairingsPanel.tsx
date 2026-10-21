import { useEffect, useState } from 'react';
import {
  Alert,
  Button,
  Checkbox,
  Col,
  Form,
  Input,
  InputNumber,
  Modal,
  Row,
  Select,
  Space,
  Table,
  Tag,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import QRCode from 'qrcode';
import { api, qs } from '../api';
import type { Me, Pairing, PairingCreated } from '../types';
import { useListView } from '../useListView';
import { useFlash } from '../flash';
import ListSearch from '../components/ListSearch';
import RowActions from '../components/RowActions';

// Pairings panel: open short-lived join invites (QR + short code), watch
// their status, revoke unused ones. An invite redeems itself into a plain
// account in this tenant — the joined device lands logged-in on the
// /join page; the generated password is shown to that device once.

const TTLS = [
  { value: 300, label: '5 minutes' },
  { value: 600, label: '10 minutes (default)' },
  { value: 1800, label: '30 minutes' },
  { value: 3600, label: '1 hour' },
  { value: 86400, label: '24 hours' },
];

const statusTag = (p: Pairing) =>
  p.status === 'pending' ? (
    <Tag color="green">open</Tag>
  ) : p.status === 'used' ? (
    <Tag>used by {p.used_by}</Tag>
  ) : p.status === 'revoked' ? (
    <Tag color="red">revoked</Tag>
  ) : (
    <Tag color="orange">expired</Tag>
  );

export default function PairingsPanel({ me }: { me: Me }) {
  const flash = useFlash();
  const lv = useListView<Pairing[]>((s) => `/api/v1/pairings?${qs(s)}`, {
    sort: 'created',
    dir: 'desc',
  });
  const [creating, setCreating] = useState(false);
  const [busy, setBusy] = useState(false);
  const [shown, setShown] = useState<PairingCreated | null>(null);
  const [qr, setQr] = useState('');
  const [form] = Form.useForm();

  // the QR encodes the join URL with the token in the URL *fragment*:
  // fragments are never sent to the server, so the secret stays out of
  // access logs and proxies only see "/join".
  useEffect(() => {
    if (!shown) {
      setQr('');
      return;
    }
    QRCode.toDataURL(`${location.origin}/join#${shown.token}`, { width: 320, margin: 2 })
      .then(setQr)
      .catch(() => setQr(''));
  }, [shown]);

  const open = async () => {
    const v = await form.validateFields();
    setBusy(true);
    try {
      const body: Record<string, unknown> = {
        description: v.description,
        ttl_seconds: v.ttl,
        apikey: !!v.apikey,
      };
      if (v.username) body.username = v.username;
      if (v.expires_days) body.expires_days = v.expires_days;
      const r = await api<PairingCreated>('/api/v1/pairings', {
        method: 'POST',
        body: JSON.stringify(body),
      });
      setCreating(false);
      form.resetFields();
      setShown(r);
      lv.reload();
    } catch (e) {
      flash.err((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const columns: ColumnsType<Pairing> = [
    {
      title: 'status',
      key: 'status',
      width: 170,
      render: (_, p) => statusTag(p),
    },
    {
      title: 'description',
      key: 'description',
      dataIndex: 'description',
      sorter: true,
      sortOrder: lv.sortOrder('description'),
      showSorterTooltip: false,
      ellipsis: true,
    },
    {
      title: 'tenant',
      key: 'tenant',
      dataIndex: 'tenant',
      sorter: true,
      sortOrder: lv.sortOrder('tenant'),
      showSorterTooltip: false,
      width: 100,
    },
    {
      title: 'username',
      key: 'username',
      dataIndex: 'username',
      sorter: true,
      sortOrder: lv.sortOrder('username'),
      showSorterTooltip: false,
      width: 130,
      render: (_, p) => p.username || <Typography.Text type="secondary">auto</Typography.Text>,
    },
    {
      title: 'key',
      key: 'apikey',
      width: 50,
      render: (_, p) => (p.apikey ? <Tag color="blue">yes</Tag> : ''),
    },
    {
      title: 'created',
      key: 'created',
      sorter: true,
      sortOrder: lv.sortOrder('created'),
      showSorterTooltip: false,
      width: 150,
      render: (_, p) => new Date(p.created_at).toLocaleString(),
    },
    {
      title: 'expires',
      key: 'expires',
      sorter: true,
      sortOrder: lv.sortOrder('expires'),
      showSorterTooltip: false,
      width: 150,
      render: (_, p) =>
        p.status === 'pending' ? (
          <span style={{ color: new Date(p.expires_at).getTime() - Date.now() < 60_000 ? '#d4380d' : undefined }}>
            {new Date(p.expires_at).toLocaleTimeString()}
          </span>
        ) : (
          ''
        ),
    },
    {
      title: 'opened by',
      key: 'created_by',
      dataIndex: 'created_by',
      width: 120,
    },
    {
      title: '',
      width: 56,
      render: (_, p) => (
        <RowActions
          actions={
            p.status === 'pending'
              ? [
                  {
                    key: 'revoke',
                    label: 'revoke',
                    danger: true,
                    confirm: {
                      title: 'revoke this invite?',
                      description: 'the QR and code stop working; the row stays for the record',
                    },
                    onClick: async () => {
                      try {
                        await api(`/api/v1/pairings/${p.id}`, { method: 'DELETE' });
                        flash.ok('pairing revoked');
                        lv.reload();
                      } catch (e) {
                        flash.err((e as Error).message);
                      }
                    },
                  },
                ]
              : []
          }
        />
      ),
    },
  ];

  return (
    <>
      <div className="bar">
        <span>pairing invites</span>
        <span className="grow" />
        <ListSearch
          state={lv.state}
          patch={lv.patch}
          cols={['description', 'username', 'tenant']}
          placeholder="search pairings"
        />
        <Button type="primary" onClick={() => setCreating(true)}>
          Pair device
        </Button>
      </div>

      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 12 }}
        message="How pairing works"
        description={
          <>
            Opening an invite shows a QR code and a 7-character code for ten minutes by default.
            On the new device, scan the QR (or open <Typography.Text code>/join</Typography.Text> and
            type the code); the device gets its own account in tenant{' '}
            <Typography.Text code>{me.tenant}</Typography.Text> and lands logged in. The generated
            password is shown on that device exactly once — the new account has no file access until
            you grant it on the Permissions tab.
          </>
        }
      />

      <Table
        rowKey={(p) => p.id}
        size="small"
        columns={columns}
        dataSource={lv.payload ?? []}
        loading={lv.loading}
        onChange={lv.onTableChange}
        pagination={lv.pagination}
        sticky
        scroll={{ x: 'max-content' }}
        locale={{
          emptyText: lv.state.q
            ? `nothing matches "${lv.state.q}"`
            : 'no pairing invites yet — click "Pair device"',
        }}
      />

      <Modal
        open={creating}
        title="Pair a new device"
        okText="Create invite"
        confirmLoading={busy}
        onOk={open}
        onCancel={() => setCreating(false)}
      >
        <Form
          form={form}
          layout="vertical"
          initialValues={{ ttl: 600 }}
          requiredMark={false}
        >
          <Form.Item
            name="description"
            label="What device is this?"
            rules={[{ required: true, message: 'a description is required (it appears in the audit trail)'}]}
          >
            <Input placeholder="e.g. Mia's phone, kitchen tablet, backup cron" autoFocus />
          </Form.Item>
          <Row gutter={12}>
            <Col span={12}>
              <Form.Item name="username" label="Account name (optional)">
                <Input placeholder="auto: dev-xxxxx" />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="ttl" label="Invite valid for">
                <Select options={TTLS} />
              </Form.Item>
            </Col>
          </Row>
          <Form.Item name="expires_days" label="Account auto-expires in (days, optional)">
            <InputNumber min={1} style={{ width: 220 }} placeholder="never" />
          </Form.Item>
          <Form.Item name="apikey" valuePropName="checked" style={{ marginBottom: 0 }}>
            <Checkbox>Also issue this device an API key (for scripts and backup tools)</Checkbox>
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        open={shown !== null}
        title={`Invite for ${shown?.pairing.description ?? ''}`}
        footer={
          <Button type="primary" onClick={() => setShown(null)}>
            Done
          </Button>
        }
        onCancel={() => setShown(null)}
      >
        <Alert
          type="warning"
          style={{ marginBottom: 12 }}
          message="Shown once — this invite cannot be re-displayed. If the device is not ready, revoke and open a new one."
        />
        <div style={{ textAlign: 'center' }}>
          {qr && <img src={qr} alt="pairing QR code" style={{ width: 260, height: 260 }} />}
          <Typography.Title level={2} copyable={{ text: shown?.code }} style={{ margin: '8px 0' }}>
            {shown?.code}
          </Typography.Title>
          <Typography.Text type="secondary">
            Scan the QR on the device, or open{' '}
            <Typography.Text code>
              <a href="/join" target="_blank" rel="noreferrer">
                /join
              </a>
            </Typography.Text>{' '}
            and type the code. Valid until{' '}
            {shown && new Date(shown.pairing.expires_at).toLocaleTimeString()}.
          </Typography.Text>
        </div>
      </Modal>
    </>
  );
}
