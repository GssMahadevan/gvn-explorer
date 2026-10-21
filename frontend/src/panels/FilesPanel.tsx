import { useEffect, useRef, useState } from 'react';
import {
  Breadcrumb,
  Button,
  Input,
  Modal,
  Space,
  Table,
  Tag,
  Typography,
  Upload,
} from 'antd';
import { FileAddOutlined, FolderAddOutlined, UploadOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { api, bits, fmtSize, qs } from '../api';
import { useFlash } from '../flash';
import { fileKind, isEditable, rawUrl } from '../filekind';
import type { EntryView, FsList } from '../types';
import type { FileTarget } from '../components/FilePane';
import { useListView } from '../useListView';
import ListSearch from '../components/ListSearch';
import RowActions from '../components/RowActions';
import DateTime from '../components/DateTime';

// Files panel: breadcrumb browser + server-paged entry table (dirs first
// is done by the backend). Clicking a file name opens the file PAGE in
// the viewer (GitHub-style default); editing is the ⋮ menu's "edit"
// action. Files outside the text allowlist (binaries, pdf, …) never open
// a page: the name links to /fs/raw and the menu offers download too
// (see filekind.ts). mkdir/upload/rename/delete as before.
export default function FilesPanel({
  cur,
  setCur,
  showPerms,
  openFile,
  filesEpoch,
}: {
  cur: string;
  setCur: (p: string) => void;
  showPerms: (p: string) => void;
  openFile: (t: FileTarget) => void;
  filesEpoch: number;
}) {
  const flash = useFlash();
  const lv = useListView<FsList>(
    (s) => `/api/v1/fs?path=${encodeURIComponent(cur)}&${qs(s)}`,
    { sort: 'name' },
  );
  const [newKind, setNewKind] = useState<null | 'dir' | 'file'>(null);
  const [newName, setNewName] = useState('');
  const [rename, setRename] = useState<{ from: string; to: string } | null>(null);

  // the file page reports writes through filesEpoch; refresh our table
  const firstEpoch = useRef(true);
  useEffect(() => {
    if (firstEpoch.current) firstEpoch.current = false;
    else lv.reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filesEpoch]);

  const rows = lv.payload?.entries ?? [];
  const ro = cur === '/help' || cur.startsWith('/help/'); // built-in docs: read-only
  const download = (p: string) => {
    const a = document.createElement('a');
    a.href = rawUrl(p);
    a.download = '';
    document.body.appendChild(a);
    a.click();
    a.remove();
  };
  // The built-in docs tree lives in the server binary, not on disk, so it
  // is pinned here client-side: every user sees and can browse it at the
  // root, while the API's root listing stays exactly as it was.
  const viewRows: typeof rows =
    cur === '/' && !lv.state.q && !rows.some((x) => x.entry.path === '/help')
      ? [
          {
            entry: { name: 'help', path: '/help', is_dir: true, size: 0, mod_time: '', mod_time_ms: 0 },
            perms: { read: true, write: false, delete: false },
          },
          ...rows,
        ]
      : rows;
  const goto = (p: string) => {
    setCur(p);
    lv.patch({ page: 1 });
  };

  const create = async () => {
    if (!newName.trim() || !newKind) return;
    const p = (cur === '/' ? '' : cur) + '/' + newName.trim();
    try {
      if (newKind === 'dir') {
        await api('/api/v1/fs/mkdir', { method: 'POST', body: JSON.stringify({ path: p }) });
        flash.ok('created ' + p);
      } else {
        await api('/api/v1/fs/write', { method: 'POST', body: JSON.stringify({ path: p, content: '' }) });
        openFile({ path: p, mode: 'edit' });
      }
      setNewKind(null);
      setNewName('');
      lv.reload();
    } catch (e) {
      flash.err((e as Error).message);
    }
  };

  const doRename = async () => {
    if (!rename) return;
    if (!rename.to || rename.to === rename.from) {
      setRename(null);
      return;
    }
    try {
      await api('/api/v1/fs/rename', { method: 'POST', body: JSON.stringify({ from: rename.from, to: rename.to }) });
      flash.ok(`renamed ${rename.from} → ${rename.to}`);
      setRename(null);
      lv.reload();
    } catch (e) {
      flash.err((e as Error).message);
    }
  };

  const columns: ColumnsType<EntryView> = [
    {
      title: 'name',
      key: 'name',
      sorter: true,
      sortOrder: lv.sortOrder('name'),
      showSorterTooltip: false,
      render: (_, x) =>
        x.entry.is_dir ? (
          <Button type="link" size="small" style={{ padding: 0 }} onClick={() => goto(x.entry.path)}>
            {x.entry.name + '/'}
          </Button>
        ) : fileKind(x.entry.name) === 'download' ? (
          // not editor-able: the name IS the download (Content-Disposition
          // on /fs/raw makes the browser save it without navigating away)
          <Button
            type="link"
            size="small"
            style={{ padding: 0 }}
            href={rawUrl(x.entry.path)}
            data-testid={`file-dl-${x.entry.name}`}
          >
            {x.entry.name}
          </Button>
        ) : (
          // default click = the viewer page (editing comes from the menu)
          <Button type="link" size="small" style={{ padding: 0 }} onClick={() => openFile({ path: x.entry.path, mode: 'view' })}>
            {x.entry.name}
          </Button>
        ),
    },
    {
      title: 'size',
      key: 'size',
      sorter: true,
      sortOrder: lv.sortOrder('size'),
      showSorterTooltip: false,
      align: 'right',
      width: 110,
      render: (_, x) => (x.entry.is_dir ? '' : fmtSize(x.entry.size)),
    },
    {
      title: 'modified',
      key: 'mtime',
      sorter: true,
      sortOrder: lv.sortOrder('mtime'),
      showSorterTooltip: false,
      width: 210,
      render: (_, x) =>
        // embedded docs carry no real mtime (zero time) — label them honestly
        x.entry.mod_time_ms > 0 ? <DateTime v={x.entry.mod_time_ms} /> : 'built-in',
    },
    {
      title: 'perms',
      width: 200,
      render: (_, x) => (
        <Space size={4}>
          <Tag className="mono">{bits(x.perms)}</Tag>
          {x.perms.source && x.perms.source !== x.entry.path && (
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              via {x.perms.source}
            </Typography.Text>
          )}
        </Space>
      ),
    },

    {
      title: '',
      width: 56,
      render: (_, x) => {
        const embedded = x.entry.path === '/help' || x.entry.path.startsWith('/help/');
        if (embedded)
          return x.entry.is_dir ? null : (
            <RowActions
              actions={[{ key: 'download', label: 'download', onClick: () => download(x.entry.path) }]}
            />
          );
        return (
          <RowActions
            actions={[
              { key: 'perms', label: 'permissions', onClick: () => showPerms(x.entry.path) },
              ...(!x.entry.is_dir
                ? [
                    // editing is an explicit menu step — the name click is
                    // the viewer. Not offered for media or download-only files.
                    ...(isEditable(fileKind(x.entry.name))
                      ? [{ key: 'edit', label: 'edit', onClick: () => openFile({ path: x.entry.path, mode: 'edit' }) }]
                      : []),
                    { key: 'download', label: 'download', onClick: () => download(x.entry.path) },
                  ]
                : []),
              {
                key: 'rename',
                label: 'rename',
                onClick: () => setRename({ from: x.entry.path, to: x.entry.path }),
              },
              {
                key: 'delete',
                label: 'delete',
                danger: true,
                confirm: { title: `delete ${x.entry.path}?` },
                onClick: async () => {
                  try {
                    await api('/api/v1/fs?path=' + encodeURIComponent(x.entry.path), {
                      method: 'DELETE',
                    });
                    flash.ok('deleted ' + x.entry.path);
                    lv.reload();
                  } catch (e) {
                    flash.err((e as Error).message);
                  }
                },
              },
            ]}
          />
        );
      },
    },
  ];

  const segs = cur.split('/').filter(Boolean);
  const crumbItems = [{ title: <a onClick={() => goto('/')}>root</a> }];
  let acc = '';
  segs.forEach((seg, i) => {
    acc += '/' + seg;
    crumbItems.push({
      title:
        i === segs.length - 1 ? (
          <span>{seg}</span>
        ) : (
          <a onClick={() => goto(acc)}>{seg}</a>
        ),
    });
  });

  return (
    <>
      <div className="bar">
        <Breadcrumb items={crumbItems} />
        <span className="grow" />
        <ListSearch state={lv.state} patch={lv.patch} placeholder="search name" />
        {ro ? (
          <Tag color="blue">built-in docs · read-only</Tag>
        ) : (
          <>
            <Button icon={<FolderAddOutlined />} onClick={() => setNewKind('dir')}>
              Folder
            </Button>
            <Button icon={<FileAddOutlined />} onClick={() => setNewKind('file')}>
              File
            </Button>
            <Upload
              maxCount={1}
              showUploadList={false}
              customRequest={async ({ file, onSuccess, onError }) => {
                const fd = new FormData();
                fd.set('path', cur);
                fd.set('file', file as File);
                try {
                  const r = await fetch('/api/v1/fs/upload', { method: 'POST', body: fd });
                  const b = (await r.json().catch(() => ({}))) as {
                    path?: string;
                    error?: { message?: string };
                  };
                  if (!r.ok) throw new Error(b?.error?.message || `${r.status} ${r.statusText}`);
                  flash.ok('uploaded ' + (b.path ?? ''));
                  onSuccess?.(b);
                  lv.reload();
                } catch (e) {
                  flash.err((e as Error).message);
                  onError?.(e as Error);
                }
              }}
            >
              <Button icon={<UploadOutlined />}>Upload</Button>
            </Upload>
          </>
        )}
      </div>
      <Table
        rowKey={(x) => x.entry.path}
        size="small"
        columns={columns}
        dataSource={viewRows}
        loading={lv.loading}
        onChange={lv.onTableChange}
        pagination={lv.pagination}
        sticky
        scroll={{ x: 'max-content' }}
        locale={{
          emptyText: lv.state.q
            ? `nothing matches "${lv.state.q}"`
            : 'empty (or nothing readable here)',
        }}
      />

      <Modal
        open={newKind !== null}
        title={newKind === 'dir' ? 'New folder in ' + cur : 'New file in ' + cur}
        okText="Create"
        onOk={create}
        onCancel={() => setNewKind(null)}
      >
        <Input
          autoFocus
          value={newName}
          placeholder={newKind === 'dir' ? 'folder name' : 'file name'}
          onChange={(e) => setNewName(e.target.value)}
          onPressEnter={create}
        />
      </Modal>

      <Modal
        open={rename !== null}
        title="Rename"
        okText="Rename"
        onOk={doRename}
        onCancel={() => setRename(null)}
      >
        <Input
          autoFocus
          value={rename?.to ?? ''}
          onChange={(e) => setRename((r) => (r ? { ...r, to: e.target.value } : r))}
          onPressEnter={doRename}
        />
      </Modal>
    </>
  );
}
