import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Modal, Space, Spin, Tag, Typography } from 'antd';
import { ApiError, api } from '../api';
import { useFlash } from '../flash';
import type { PermsList } from '../types';
import YamlEditor from './YamlEditor';

interface FsContent {
  content?: string;
  is_binary?: boolean;
  mtime?: number;
}
interface Draft {
  content: string;
  updated_by: string;
  updated_at: number;
}
interface DraftResp {
  draft: Draft | null;
}

const isYamlPath = (p: string) => /\.(ya?ml)$/i.test(p);
const fmtTs = (ts: number) => new Date(ts * 1000).toLocaleString();

// View/edit a text file. Plain text keeps the classic textarea, now with
// the base_mtime guard so two writers get a 409 instead of clobbering.
// YAML config files (.yml/.yaml) get the structured editor — an
// explorer-style tree plus per-node detail tables — on top of the
// draft/publish model: every change autosaves to the server-side
// draft, and the file on disk only changes when the draft is published.
// Drafting needs the w bit; readers can preview the draft but not edit.
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
  const [busy, setBusy] = useState(true);
  const [binary, setBinary] = useState(false);
  const [canW, setCanW] = useState(false);
  const [mtime, setMtime] = useState<number | undefined>(undefined);
  const [fileText, setFileText] = useState(''); // bytes on disk
  const [text, setText] = useState(''); // working copy in the editor
  const [draft, setDraft] = useState<Draft | null>(null);
  const [saveState, setSaveState] = useState<'clean' | 'pending' | 'error'>('clean');
  const [commitErr, setCommitErr] = useState('');
  const [conflict, setConflict] = useState('');

  // last text known to be persisted server-side (in the draft, or the file)
  const savedRef = useRef('');
  const debounce = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const yaml = path !== null && isYamlPath(path);

  useEffect(() => {
    if (!path) return;
    setBusy(true);
    setCommitErr('');
    setConflict('');
    (async () => {
      try {
        const c = await api<FsContent>('/api/v1/fs/content?path=' + encodeURIComponent(path));
        setBinary(!!c.is_binary);
        setMtime(c.mtime);
        setFileText(c.content ?? '');
        let d: Draft | null = null;
        let w = false;
        if (!c.is_binary) {
          // bits + draft are best-effort extras on top of the file view.
          const [dr, pm] = await Promise.allSettled([
            api<DraftResp>('/api/v1/fs/draft?path=' + encodeURIComponent(path)),
            api<PermsList>('/api/v1/perms?path=' + encodeURIComponent(path)),
          ]);
          if (dr.status === 'fulfilled') d = dr.value.draft ?? null;
          if (pm.status === 'fulfilled') w = !!pm.value.effective?.write;
        }
        setDraft(d);
        setCanW(w);
        const working = d ? d.content : (c.content ?? '');
        setText(working);
        savedRef.current = working;
        setSaveState('clean');
      } catch (e) {
        flash.err((e as Error).message);
        onClose();
      } finally {
        setBusy(false);
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path]);

  const pushDraft = async (t: string) => {
    if (!path) return;
    setSaveState('pending');
    try {
      const r = await api<{ draft: Draft }>('/api/v1/fs/draft', {
        method: 'PUT',
        body: JSON.stringify({ path, content: t }),
      });
      setDraft(r.draft);
      savedRef.current = t;
      setSaveState('clean');
    } catch (e) {
      setSaveState('error');
      flash.err('draft save failed: ' + (e as Error).message);
    }
  };

  // every tree edit autosaves the draft (debounced); the API re-validates
  // and audits at publish, so a half-typed value is safe to leave drafted.
  const onTreeChange = (t: string) => {
    setText(t);
    if (!canW) return;
    if (debounce.current) clearTimeout(debounce.current);
    debounce.current = setTimeout(() => void pushDraft(t), 1200);
  };

  const publish = async () => {
    if (!path) return;
    if (debounce.current) clearTimeout(debounce.current);
    try {
      if (text !== savedRef.current) await pushDraft(text);
      await api('/api/v1/fs/draft/commit', { method: 'POST', body: JSON.stringify({ path }) });
      setFileText(text);
      savedRef.current = text;
      setDraft(null);
      setSaveState('clean');
      setCommitErr('');
      flash.ok('published ' + path);
      onSaved();
    } catch (e) {
      const err = e as ApiError;
      if (err.status === 422) setCommitErr(err.message);
      else flash.err(err.message);
    }
  };

  const discardDraft = async () => {
    if (!path) return;
    if (debounce.current) clearTimeout(debounce.current);
    try {
      await api('/api/v1/fs/draft?path=' + encodeURIComponent(path), { method: 'DELETE' });
      setDraft(null);
      setText(fileText);
      savedRef.current = fileText;
      setSaveState('clean');
      flash.ok('draft discarded, showing file contents');
    } catch (e) {
      flash.err((e as Error).message);
    }
  };

  const savePlain = async () => {
    if (!path) return;
    try {
      const body: Record<string, unknown> = { path, content: text };
      if (mtime !== undefined) body.base_mtime = mtime;
      await api('/api/v1/fs/write', { method: 'POST', body: JSON.stringify(body) });
      const c = await api<FsContent>('/api/v1/fs/content?path=' + encodeURIComponent(path));
      setMtime(c.mtime);
      setFileText(text);
      savedRef.current = text;
      setConflict('');
      flash.ok('saved ' + path);
      onSaved();
    } catch (e) {
      const err = e as Error;
      if ((e as ApiError).status === 409)
        setConflict(
          'The file changed on disk while you were editing — your text is kept in this box; copy it out, reload the file (close + reopen), then re-apply. (' +
            err.message +
            ')',
        );
      else flash.err(err.message);
    }
  };

  const statusTag = () => {
    if (!yaml || binary) return null;
    if (!canW) return <Tag>read-only</Tag>;
    if (saveState === 'pending') return <Tag>auto-saving draft…</Tag>;
    if (saveState === 'error') return <Tag color="red">draft save failed</Tag>;
    if (draft) return <Tag color="orange">draft auto-saved {fmtTs(draft.updated_at)}</Tag>;
    return null;
  };

  return (
    <Modal
      open={path !== null}
      title={
        <Space>
          <Typography.Text className="mono">{path}</Typography.Text>
          {statusTag()}
        </Space>
      }
      width={920}
      onCancel={onClose}
      footer={[
        binary ? (
          <Button key="dl" href={`/api/v1/fs/raw?path=${encodeURIComponent(path ?? '')}`}>
            Download
          </Button>
        ) : yaml ? (
          <Button key="discard" danger disabled={!canW || !draft} onClick={discardDraft} data-testid="editor-discard">
            Discard draft
          </Button>
        ) : (
          <Button key="save" onClick={savePlain} disabled={!canW}>
            Save
          </Button>
        ),
        yaml && (
          <Button key="publish" type="primary" disabled={!canW} onClick={publish} data-testid="editor-publish">
            Publish to file
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
        <>
          {yaml && draft && (
            <Alert
              type="warning"
              showIcon
              style={{ marginBottom: 8 }}
              message="Unpublished draft — the file on disk still has the old bytes"
              description={
                <span data-testid="editor-draft-meta">
                  draft last edited by {draft.updated_by} at {fmtTs(draft.updated_at)}; publishing writes it to the
                  file.
                </span>
              }
              action={
                canW ? (
                  <Button size="small" onClick={discardDraft}>
                    Discard draft
                  </Button>
                ) : undefined
              }
            />
          )}
          {yaml && !canW && (
            <Alert
              type="info"
              showIcon
              style={{ marginBottom: 8 }}
              message="You have r on this file, so it is view-only — drafting needs w."
            />
          )}
          {commitErr && (
            <Alert
              type="error"
              showIcon
              closable
              onClose={() => setCommitErr('')}
              style={{ marginBottom: 8 }}
              message="Publish refused: this draft is not valid YAML"
              description={<span className="mono">{commitErr}</span>}
              data-testid="editor-commit-error"
            />
          )}
          {conflict && (
            <Alert
              type="warning"
              showIcon
              closable
              onClose={() => setConflict('')}
              style={{ marginBottom: 8 }}
              message="Save refused: concurrent modification"
              description={conflict}
            />
          )}
          {yaml ? (
            <YamlEditor text={text} onChange={onTreeChange} readOnly={!canW} onClose={onClose} />
          ) : (
            <textarea
              className="code"
              value={text}
              readOnly={!canW}
              spellCheck={false}
              onChange={(e) => setText(e.target.value)}
            />
          )}
        </>
      )}
    </Modal>
  );
}
