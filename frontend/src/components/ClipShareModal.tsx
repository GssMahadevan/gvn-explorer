import { useEffect, useState } from 'react';
import { Alert, Input, Modal, Radio, Select, Space, Typography } from 'antd';
import { api } from '../api';
import { useFlash } from '../flash';
import { CLIP_MIMES, guessClipMime } from '../cliputil';
import type { Clip } from '../types';

export type ClipFlow = { mode: 'file'; path: string } | { mode: 'text'; dir: string };

// Share-to-clipboard: the one place that creates links in a tenant's
// feed. A share never copies bytes — it posts a link, and the link is
// only ever as strong as the target file's ACL (the server re-checks
// read on every open). Two faces: share a file that already exists, or
// save typed/pasted text as a new file in the current directory and
// share that. If the directory is not writable the server says so and
// we show it — a text clipboard has to live in SOME real file, and
// inventing a hidden one would hide it from the permissions model.
export default function ClipShareModal({
  flow,
  onClose,
  onDone,
}: {
  flow: ClipFlow | null;
  onClose: () => void;
  // called after a successful share so listings can refresh
  onDone: () => void;
}) {
  const flash = useFlash();
  const open = flow !== null;
  const [clips, setClips] = useState<Clip[]>([]);
  const [mode, setMode] = useState<'new' | 'existing'>('new');
  const [name, setName] = useState('');
  const [mime, setMime] = useState('*/*');
  const [pick, setPick] = useState<number | null>(null);
  const [text, setText] = useState('');
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!flow) return;
    setErr('');
    setBusy(false);
    setMode('new');
    setName('');
    setText('');
    setMime(flow.mode === 'file' ? guessClipMime(flow.path) : 'text/plain');
    setPick(null);
    api<Clip[]>('/api/v1/clipboards')
      .then((cs) => {
        setClips(cs);
        setPick(cs.length ? cs[0]?.id ?? null : null);
      })
      .catch(() => setClips([]));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [flow]);

  const share = async () => {
    if (!flow || busy) return;
    setBusy(true);
    setErr('');
    try {
      // the link target: an existing file, or text saved into one here
      let target = flow.mode === 'file' ? flow.path : '';
      if (flow.mode === 'text') {
        if (!text.trim()) throw new Error('type or paste some text first');
        const dir = flow.dir === '/' ? '' : flow.dir;
        target = dir + '/text-' + Date.now().toString(36) + '.txt';
        await api('/api/v1/fs/write', { method: 'POST', body: JSON.stringify({ path: target, content: text }) });
      }
      let clip: Clip;
      if (mode === 'existing') {
        const found = clips.find((c) => c.id === pick);
        if (!found) throw new Error('pick which clipboard to post into');
        clip = found;
      } else {
        clip = await api<Clip>('/api/v1/clipboards', {
          method: 'POST',
          body: JSON.stringify({ name: name.trim(), mime }),
        });
      }
      const it = await api<{ link_path: string }>('/api/v1/clipboards/' + clip.id + '/items', {
        method: 'PUT',
        body: JSON.stringify({ path: target }),
      });
      flash.ok('shared ' + target + ' → ' + it.link_path);
      onDone();
      onClose();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const what =
    flow?.mode === 'file' ? (
      <Typography.Paragraph>
        link a share to <Typography.Text className="mono">{flow.path}</Typography.Text> —
        anyone in the tenant can open it while the file's own permissions allow.
      </Typography.Paragraph>
    ) : (
      <Input.TextArea
        rows={6}
        value={text}
        placeholder="text to keep at hand"
        onChange={(e) => setText(e.target.value)}
        style={{ marginBottom: 8 }}
        data-testid="clip-text"
      />
    );

  return (
    <Modal
      open={open}
      title={flow?.mode === 'file' ? 'Share to a clipboard' : 'New text clipboard'}
      okText="Share"
      okButtonProps={{ disabled: busy }}
      onOk={share}
      onCancel={onClose}
      width={560}
    >
      {what}
      {clips.length > 0 && (
        <Radio.Group
          value={mode}
          onChange={(e) => setMode(e.target.value)}
          options={[
            { value: 'new', label: 'New clipboard' },
            { value: 'existing', label: 'Post into an existing one' },
          ]}
          optionType="button"
          style={{ marginBottom: 8 }}
        />
      )}
      {mode === 'new' ? (
        <Space.Compact block>
          <Input
            value={name}
            placeholder="clipboard name (lowercase, e.g. work-notes)"
            onChange={(e) => setName(e.target.value)}
            onPressEnter={share}
            data-testid="clip-name"
          />
          <Select value={mime} onChange={setMime} options={CLIP_MIMES} style={{ width: 210 }} data-testid="clip-mime" />
        </Space.Compact>
      ) : (
        <Select
          value={pick}
          onChange={setPick}
          style={{ width: '100%' }}
          options={clips.map((c) => ({
            value: c.id,
            label: `${c.name} · ${c.mime} · ${c.items} live ${c.items === 1 ? 'link' : 'links'}`,
          }))}
        />
      )}
      {err && (
        <Alert
          type="error"
          showIcon
          closable
          onClose={() => setErr('')}
          style={{ marginTop: 10 }}
          message="The server refused the share"
          description={<span className="mono">{err}</span>}
          data-testid="clip-error"
        />
      )}
    </Modal>
  );
}
