import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Segmented, Space, Tag, Typography } from 'antd';
import { ArrowLeftOutlined, DownloadOutlined, EditOutlined } from '@ant-design/icons';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { ApiError, api } from '../api';
import { useFlash } from '../flash';
import { fileKind, rawUrl } from '../filekind';
import type { PermsList } from '../types';
import DocEditor from './DocEditor';
import YamlEditor from './YamlEditor';

export type FileTarget = { path: string; mode: 'view' | 'edit' };

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

const fmtTs = (ts: number) => new Date(ts * 1000).toLocaleString();

// The file page — a real browser pane, not a modal (one component serves
// viewing AND editing, like GitHub's file pages). Default entry is the
// VIEWER: markdown renders (with a Code toggle), other text is read-only
// CodeMirror, YAML shows the read-only structured tree. Editing is an
// explicit step: the Files row ⋮ menu offers "edit", the pane header has
// an Edit button for convenience. Plain docs edit in CodeMirror with a
// markdown ribbon and the base_mtime guard (409 on clobber). YAML edits
// run the draft/publish model: every change autosaves to the server-side
// draft; the file on disk only changes on publish. Drafting needs the w
// bit; readers preview but cannot edit. Files outside the text allowlist
// never open this pane — their row action is download (see filekind.ts);
// the binary branch below is only the safety net for text-looking names
// that sniff binary on content.
export default function FilePane({
  target,
  onClose,
  onSaved,
}: {
  target: FileTarget;
  onClose: () => void;
  onSaved: () => void;
}) {
  const flash = useFlash();
  const { path } = target;
  const kind = fileKind(path);
  const yaml = kind === 'yaml';
  const md = /\.(md|markdown)$/i.test(path);

  const [mode, setMode] = useState<'view' | 'edit'>(target.mode);
  const [view, setView] = useState<'render' | 'code'>(md ? 'render' : 'code');
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

  useEffect(() => {
    setMode(target.mode);
    setView(md ? 'render' : 'code');
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target]);

  useEffect(() => {
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
    if (debounce.current) clearTimeout(debounce.current);
    try {
      await api('/api/v1/fs/draft?path=' + encodeURIComponent(path), { method: 'DELETE' });
      setDraft(null);
      setText(fileText);
      savedRef.current = fileText;
      setSaveState('clean');
      flash.ok(`discarded draft for ${path}, showing file contents`);
    } catch (e) {
      flash.err((e as Error).message);
    }
  };

  const savePlain = async () => {
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
          'The file changed on disk while you were editing — your text is kept in this pane; copy it out, reload the file, then re-apply. (' +
            err.message +
            ')',
        );
      else flash.err(err.message);
    }
  };

  const statusTag = () => {
    if (binary) return null;
    if (mode === 'view') return canW ? null : <Tag>view-only</Tag>;
    if (!canW) return <Tag>read-only</Tag>;
    if (saveState === 'pending') return <Tag>auto-saving draft…</Tag>;
    if (saveState === 'error') return <Tag color="red">draft save failed</Tag>;
    if (draft) return <Tag color="orange">draft auto-saved {fmtTs(draft.updated_at)}</Tag>;
    return null;
  };

  return (
    <div data-testid="file-pane">
      <div className="bar" style={{ marginBottom: 8 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={onClose} data-testid="pane-back">
          files
        </Button>
        <Space size={6}>
          <Typography.Text className="mono" strong>
            {path}
          </Typography.Text>
          {statusTag()}
        </Space>
        <span className="grow" />
        {md && mode === 'view' && !binary && (
          <Segmented
            size="small"
            data-testid="pane-viewmode"
            value={view}
            onChange={(v) => setView(v as 'render' | 'code')}
            options={[
              { label: 'Render', value: 'render' },
              { label: 'Code', value: 'code' },
            ]}
          />
        )}
        {mode === 'view' && !binary && canW && (
          <Button type="primary" size="small" icon={<EditOutlined />} onClick={() => setMode('edit')} data-testid="pane-edit">
            Edit
          </Button>
        )}
        <Button size="small" icon={<DownloadOutlined />} href={rawUrl(path)} data-testid="pane-dl" />
        {mode === 'edit' &&
          (yaml ? (
            <>
              <Button size="small" danger disabled={!canW || !draft} onClick={discardDraft} data-testid="editor-discard">
                Discard draft
              </Button>
              <Button size="small" type="primary" disabled={!canW} onClick={publish} data-testid="editor-publish">
                Publish to file
              </Button>
            </>
          ) : (
            <Button size="small" type="primary" onClick={savePlain} disabled={!canW} data-testid="editor-save">
              Save
            </Button>
          ))}
      </div>

      {busy ? (
        <div style={{ textAlign: 'center', margin: '60px auto' }}>loading…</div>
      ) : binary ? (
        <Alert type="info" message="Binary file — use download to fetch it." />
      ) : (
        <>
          {mode === 'edit' && yaml && draft && (
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
          {mode === 'edit' && yaml && !canW && (
            <Alert
              type="info"
              showIcon
              style={{ marginBottom: 8 }}
              message="You have r on this file, so it is view-only — drafting needs w."
            />
          )}
          {mode === 'edit' && commitErr && (
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
          {mode === 'edit' && conflict && (
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
            <YamlEditor
              text={text}
              onChange={onTreeChange}
              readOnly={!canW || mode === 'view'}
              onClose={onClose}
            />
          ) : md && mode === 'view' && view === 'render' ? (
            <div className="md-view" data-testid="md-view">
              <ReactMarkdown remarkPlugins={[remarkGfm]}>{text}</ReactMarkdown>
            </div>
          ) : (
            // keyed by path so each file gets a fresh editor (own undo history)
            <DocEditor
              key={path}
              path={path}
              value={text}
              readOnly={mode === 'view' || !canW}
              onChange={setText}
            />
          )}
        </>
      )}
    </div>
  );
}
