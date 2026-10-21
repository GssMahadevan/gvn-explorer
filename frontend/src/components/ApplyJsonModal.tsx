import { useEffect, useMemo, useRef, useState } from 'react';
import { Alert, Input, Modal, Radio, Space, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { api, ApiError } from '../api';
import { useFlash } from '../flash';
import type { Doc, DocGrant, ImportReport, Me } from '../types';
import { docLabel } from './DocJsonModal';

// Apply a document: the exact text you paste (or pick from a file) is
// what POST /api/v1/import receives. The preview is not a second truth —
// it fetches the target's CURRENT document from the same export endpoint
// and shows the difference, so "replace" can name every row it will
// delete before anything is written. Validation stays the server's: a
// rejected import lists every problem, and nothing half-applies.

const KINDS = ['acl', 'dir', 'user', 'group', 'tenant'];

// parse a document leniently; returns the problem text instead of throwing.
export function parseDoc(text: string): { doc?: Doc; problem?: string } {
  if (!text.trim()) return { problem: 'nothing to apply yet — paste a document or pick a file.' };
  let v: unknown;
  try {
    v = JSON.parse(text);
  } catch (e) {
    return { problem: `not valid JSON: ${(e as Error).message}` };
  }
  const d = v as Doc;
  if (!d || typeof d !== 'object' || !KINDS.includes(String(d.kind)))
    return { problem: 'kind must be one of acl | dir | user | group | tenant' };
  if (d.version !== undefined && d.version !== 1)
    return { problem: `version ${d.version} is not understood (this server writes version 1)` };
  if (!Array.isArray(d.grants)) return { problem: 'grants must be a list' };
  return { doc: d };
}

const exportUrl = (d: Doc): string | null => {
  if (d.kind === 'acl' || d.kind === 'dir')
    return d.path ? '/api/v1/export?path=' + encodeURIComponent(d.path) : null;
  if (d.kind === 'user')
    return d.tenant && d.username
      ? '/api/v1/export?user=' + encodeURIComponent(`${d.tenant}/${d.username}`)
      : null;
  if (d.kind === 'group')
    return d.tenant && d.name
      ? '/api/v1/export?group=' + encodeURIComponent(`${d.tenant}/${d.name}`)
      : null;
  return d.name ? '/api/v1/export?tenant=' + encodeURIComponent(d.name) : null;
};

// Identity of one grant line, as the server sees it: path + type + the
// tenant the name resolves in (empty/"*" = the applying account's own) +
// the name itself. Bits/expiry do not identify a row — saving the same
// identity just updates it.
const lineKey = (d: Doc, g: DocGrant, myTenant: string): string => {
  const path = g.path ?? (d.kind === 'acl' || d.kind === 'dir' ? d.path ?? '/' : '');
  if (g.principal_type === 'all') return `${path}|all`;
  const t = !g.tenant || g.tenant === '*' ? myTenant : g.tenant;
  return `${path}|${g.principal_type}|${t.toLowerCase()}|${g.principal ?? ''}`;
};

const lineLabel = (g: DocGrant, path: string): string => {
  const who =
    g.principal_type === 'all'
      ? '* all signed-in users'
      : g.principal_type === 'tenant'
        ? `${g.principal} (all members)`
        : `${g.tenant || '?'}/${g.principal}`;
  const b = (g.read ? 'r' : '-') + (g.write ? 'w' : '-') + (g.delete ? 'd' : '-');
  return `${who} @ ${path} → ${b}`;
};

export default function ApplyJsonModal({
  open,
  me,
  onClose,
  onApplied,
}: {
  open: boolean;
  me: Me;
  onClose: () => void;
  onApplied: () => void;
}) {
  const flash = useFlash();
  const [text, setText] = useState('');
  const [mode, setMode] = useState<'merge' | 'replace'>('merge');
  const fileRef = useRef<HTMLInputElement>(null);

  const parsed = useMemo(() => parseDoc(text), [text]);
  const doc = parsed.doc;

  // current state of the same target, straight from GET /export.
  // 404 here means "this subject does not exist yet" (entity docs) —
  // which the preview renders as a creation; anything else (403 on a
  // foreign entity) just disables the diff, never the apply.
  const [current, setCurrent] = useState<{ ok: boolean; note?: string; grants: DocGrant[] } | null>(
    null,
  );
  useEffect(() => {
    if (!open) {
      setText('');
      setMode('merge');
      setCurrent(null);
      return;
    }
    if (!doc) {
      setCurrent(null);
      return;
    }
    const url = exportUrl(doc);
    if (!url) {
      setCurrent({ ok: false, note: 'the document names no target to preview', grants: [] });
      return;
    }
    let dead = false;
    api<{ grants: DocGrant[] }>(url)
      .then((r) => !dead && setCurrent({ ok: true, grants: r.grants ?? [] }))
      .catch((e) =>
        !dead &&
        setCurrent(
          (e as ApiError).status === 404
            ? { ok: true, grants: [] } // brand-new subject: nothing exists to diff yet
            : { ok: false, note: (e as Error).message, grants: [] },
        ),
      );
    return () => {
      dead = true;
    };
  }, [open, doc]);

  const diff = useMemo(() => {
    if (!doc || !current || !current.ok) return null;
    const curKeys = new Set(current.grants.map((g) => lineKey(doc, g, me.tenant)));
    const docKeys = new Set(doc.grants.map((g) => lineKey(doc, g, me.tenant)));
    const adds = doc.grants.filter((g) => !curKeys.has(lineKey(doc, g, me.tenant)));
    const same = doc.grants.length - adds.length;
    const removes = mode === 'replace' ? current.grants.filter((g) => !docKeys.has(lineKey(doc, g, me.tenant))) : [];
    return { adds, same, removes };
  }, [doc, current, mode, me.tenant]);

  const apply = async () => {
    if (!doc) return;
    try {
      const rep = await api<ImportReport>('/api/v1/import', {
        method: 'POST',
        body: JSON.stringify({ doc, mode }),
      });
      const parts = [`applied ${rep.applied} grant${rep.applied === 1 ? '' : 's'}`];
      if (rep.created) parts.push(`created ${rep.subject ?? rep.path ?? rep.kind}`);
      if (rep.memberships) parts.push(`joined ${rep.memberships} group${rep.memberships === 1 ? '' : 's'}`);
      if (rep.removed?.length) parts.push(`removed ${rep.removed.length}`);
      flash.ok(`${rep.kind} ${docLabel(doc)}: ${parts.join(', ')}`);
      onApplied();
      onClose();
    } catch (e) {
      flash.err((e as Error).message); // the server lists every problem at once
    }
  };

  const removeCols: ColumnsType<DocGrant> = [
    {
      title: 'these rows are NOT in the document',
      render: (_, g) => (
        <span className="mono">
          {lineLabel(g, g.path ?? doc?.path ?? '/')}
        </span>
      ),
    },
  ];

  return (
    <Modal
      open={open}
      width={760}
      data-testid="apply-json-modal"
      title="Apply JSON document"
      okText="Apply"
      okButtonProps={{ disabled: !doc }}
      onOk={apply}
      onCancel={onClose}
    >
      <Space direction="vertical" size={8} style={{ width: '100%' }}>
        <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
          paste a document, or pick a saved template file. import creates only the
          document&apos;s own subject; every other principal must already exist.{' '}
          imported users are device users (no password) until you give them one.
        </Typography.Paragraph>
        <input
          type="file"
          accept=".json,application/json"
          data-testid="apply-json-file"
          ref={fileRef}
          onChange={async (e) => {
            const f = e.target.files?.[0];
            if (!f) return;
            setText(await f.text());
            e.target.value = '';
          }}
        />
        <Input.TextArea
          className="mono"
          rows={12}
          data-testid="apply-json-text"
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder='{"kind":"acl","version":1,"path":"/shared/templates","grants":[…]}'
        />
        <Radio.Group
          data-testid="apply-json-mode"
          value={mode}
          onChange={(e) => setMode(e.target.value)}
          optionType="button"
          buttonStyle="solid"
          options={[
            { value: 'merge', label: 'merge (add/update)' },
            { value: 'replace', label: 'replace (delete what the doc omits)' },
          ]}
        />
        {parsed.problem && text.trim() !== '' && (
          <Alert type="error" message={parsed.problem} data-testid="apply-json-problem" />
        )}
        {doc && (
          <Space size={6} wrap>
            <Tag>{docLabel(doc)}</Tag>
            <Tag>{doc.grants.length} grant line{doc.grants.length === 1 ? '' : 's'}</Tag>
            {doc.kind === 'dir' && <Tag color="blue">creates the directory first</Tag>}
          </Space>
        )}
        {diff && (
          <div data-testid="apply-json-diff">
            <Space size={6} wrap>
              <Tag color="green">{diff.adds.length} to apply</Tag>
              <Tag>{diff.same} already match</Tag>
              {mode === 'replace' && (
                <Tag color={diff.removes.length ? 'red' : 'default'}>
                  {diff.removes.length} to remove
                </Tag>
              )}
              {current?.ok && current.grants.length === 0 && doc && (doc.kind === 'user' || doc.kind === 'group' || doc.kind === 'tenant') && (
                <Tag color="blue">this subject does not exist yet — it will be created</Tag>
              )}
            </Space>
            {diff.removes.length > 0 && (
              <Table
                size="small"
                style={{ marginTop: 8 }}
                columns={removeCols}
                dataSource={diff.removes}
                rowKey={(g) => lineKey(doc!, g, me.tenant)}
                pagination={false}
              />
            )}
          </div>
        )}
        {current && !current.ok && current.note && (
          <Typography.Text type="secondary">
            cannot preview current grants here: {current.note} — applying still works; the server
            validates everything at once.
          </Typography.Text>
        )}
      </Space>
    </Modal>
  );
}
