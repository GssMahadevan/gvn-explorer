import { Button, Modal, Space, Typography } from 'antd';
import { CopyOutlined, DownloadOutlined } from '@ant-design/icons';
import { useFlash } from '../flash';
import type { Doc } from '../types';

// One entity, one JSON statement: exactly what GET /api/v1/export
// returned. Copy moves it to the clipboard; Download saves it under the
// /templates/<kind>-<name>.json naming convention — /templates is a
// folder convention, not something the server treats specially, so the
// file lands in your browser's downloads and you upload it wherever you
// like. Any instance takes the same text back through "Apply JSON".

export const docLabel = (d: Doc): string => {
  if (d.kind === 'acl' || d.kind === 'dir') return `${d.kind} ${d.path ?? '/'}`;
  if (d.kind === 'user') return `user ${d.tenant}/${d.username}`;
  if (d.kind === 'group') return `group ${d.tenant}/${d.name}`;
  return `tenant ${d.name}`;
};

// The download name follows the template convention; path separators
// collapse to dashes so one file per document stays valid everywhere.
export const docFileName = (d: Doc): string => {
  const who =
    d.kind === 'acl' || d.kind === 'dir'
      ? (d.path ?? '').replace(/^\/+/, '').replace(/\/+/g, '-') || 'root'
      : d.kind === 'user'
        ? d.username
        : d.name;
  return `${d.kind}-${who ?? 'doc'}.json`;
};

export default function DocJsonModal({
  doc,
  onClose,
}: {
  doc: Doc | null;
  onClose: () => void;
}) {
  const flash = useFlash();
  const text = doc ? JSON.stringify(doc, null, 2) : '';

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      flash.ok('document copied to clipboard');
    } catch {
      flash.warn('clipboard unavailable — select the text and copy manually');
    }
  };

  const download = () => {
    if (!doc) return;
    const a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([text], { type: 'application/json' }));
    a.download = docFileName(doc);
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(a.href);
  };

  return (
    <Modal
      open={doc !== null}
      width={720}
      data-testid="doc-json-modal"
      title={
        <span>
          document · <span className="mono">{doc ? docLabel(doc) : ''}</span>
        </span>
      }
      footer={
        <Space>
          <Button icon={<CopyOutlined />} data-testid="doc-copy" onClick={copy}>
            Copy
          </Button>
          <Button icon={<DownloadOutlined />} data-testid="doc-download" onClick={download}>
            Save as template ({doc ? docFileName(doc) : ''})
          </Button>
          <Button type="primary" onClick={onClose}>
            Close
          </Button>
        </Space>
      }
      onCancel={onClose}
    >
      <Typography.Paragraph type="secondary" style={{ marginBottom: 8 }}>
        names, never ids — the same text applies on any instance via Permissions ▸ Apply JSON.
        templates are plain files: keep them in <Typography.Text code>/templates/</Typography.Text>{' '}
        by convention.
      </Typography.Paragraph>
      <pre className="mono" style={{ maxHeight: '50vh', overflow: 'auto', margin: 0 }}>
        {text}
      </pre>
    </Modal>
  );
}
