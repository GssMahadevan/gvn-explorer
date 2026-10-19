import { useEffect, useState } from 'react';
import { Alert, Button, Modal, Spin, Typography } from 'antd';
import { api } from '../api';
import { useFlash } from '../flash';

interface FsContent {
  content?: string;
  is_binary?: boolean;
}

// View/edit a text file; binaries only offer download (/fs/raw).
export default function EditorModal({
  path,
  onClose,
  onSaved,
}: {
  path: string | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const flash = useFlash();
  const [content, setContent] = useState('');
  const [binary, setBinary] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!path) return;
    setBusy(true);
    api<FsContent>('/api/v1/fs/content?path=' + encodeURIComponent(path))
      .then((d) => {
        setBinary(!!d.is_binary);
        setContent(d.content ?? '');
      })
      .catch((e: Error) => {
        flash.err(e.message);
        onClose();
      })
      .finally(() => setBusy(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path]);

  const save = async () => {
    try {
      await api('/api/v1/fs/write', { method: 'POST', body: JSON.stringify({ path, content }) });
      flash.ok('saved ' + path);
      onSaved();
    } catch (e) {
      flash.err((e as Error).message);
    }
  };

  return (
    <Modal
      open={path !== null}
      title={<span className="mono">{path}</span>}
      width={880}
      onCancel={onClose}
      footer={[
        binary ? (
          <Button key="dl" href={`/api/v1/fs/raw?path=${encodeURIComponent(path ?? '')}`}>
            Download
          </Button>
        ) : (
          <Button key="save" type="primary" onClick={save}>
            Save
          </Button>
        ),
        <Button key="close" onClick={onClose}>
          Close
        </Button>,
      ]}
    >
      {busy ? (
        <Spin style={{ display: 'block', margin: '40px auto' }} />
      ) : binary ? (
        <Alert type="info" message="Binary file — use download to fetch it." />
      ) : (
        <textarea
          className="code"
          value={content}
          spellCheck={false}
          onChange={(e) => setContent(e.target.value)}
        />
      )}
    </Modal>
  );
}
