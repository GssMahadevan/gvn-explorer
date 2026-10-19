import { useState } from 'react';
import {
  App as AntApp,
  Breadcrumb,
  Button,
  Input,
  Modal,
  Popconfirm,
  Space,
  Table,
  Tag,
  Typography,
  Upload,
} from 'antd';
import { FileAddOutlined, FolderAddOutlined, UploadOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { api, bits, fmtSize, qs } from '../api';
import type { EntryView, FsList } from '../types';
import { useListView } from '../useListView';
import ListSearch from '../components/ListSearch';
import EditorModal from '../components/EditorModal';

// Files panel: breadcrumb browser + server-paged entry table (dirs first
// is done by the backend), text editor modal, mkdir/upload/rename/delete.
export default function FilesPanel({
  cur,
  setCur,
  showPerms,
}: {
  cur: string;
  setCur: (p: string) => void;
  showPerms: (p: string) => void;
}) {
  const { message } = AntApp.useApp();
  const lv = useListView<FsList>(
    (s) => `/api/v1/fs?path=${encodeURIComponent(cur)}&${qs(s)}`,
    { sort: 'name' },
  );
  const [newKind, setNewKind] = useState<null | 'dir' | 'file'>(null);
  const [newName, setNewName] = useState('');
  const [rename, setRename] = useState<{ from: string; to: string } | null>(null);
  const [editPath, setEditPath] = useState<string | null>(null);

  const rows = lv.payload?.entries ?? [];
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
        message.success('created ' + p);
      } else {
        await api('/api/v1/fs/write', { method: 'POST', body: JSON.stringify({ path: p, content: '' }) });
        setEditPath(p);
      }
      setNewKind(null);
      setNewName('');
      lv.reload();
    } catch (e) {
      message.error((e as Error).message);
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
      message.success('renamed');
      setRename(null);
      lv.reload();
    } catch (e) {
      message.error((e as Error).message);
    }
  };

  const columns: ColumnsType<EntryView> = [
    {
      title: 'name',
      key: 'name',
      sorter: true,
      sortOrder: lv.sortOrder('name'),
      showSorterTooltip: false,
      render: (_, x) => (
        <Button
          type="link"
          size="small"
          style={{ padding: 0 }}
          onClick={() => (x.entry.is_dir ? goto(x.entry.path) : setEditPath(x.entry.path))}
        >
          {x.entry.is_dir ? x.entry.name + '/' : x.entry.name}
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
      render: (_, x) => new Date(x.entry.mod_time).toLocaleString(),
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
      width: 240,
      render: (_, x) => (
        <Space>
          <Button size="small" onClick={() => showPerms(x.entry.path)}>
            perms
          </Button>
          <Button size="small" onClick={() => setRename({ from: x.entry.path, to: x.entry.path })}>
            rename
          </Button>
          <Popconfirm
            title={`delete ${x.entry.path}?`}
            onConfirm={async () => {
              try {
                await api('/api/v1/fs?path=' + encodeURIComponent(x.entry.path), { method: 'DELETE' });
                message.success('deleted ' + x.entry.path);
                lv.reload();
              } catch (e) {
                message.error((e as Error).message);
              }
            }}
          >
            <Button size="small" danger>
              delete
            </Button>
          </Popconfirm>
        </Space>
      ),
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
              message.success('uploaded ' + (b.path ?? ''));
              onSuccess?.(b);
              lv.reload();
            } catch (e) {
              message.error((e as Error).message);
              onError?.(e as Error);
            }
          }}
        >
          <Button icon={<UploadOutlined />}>Upload</Button>
        </Upload>
      </div>
      <Table
        rowKey={(x) => x.entry.path}
        size="small"
        columns={columns}
        dataSource={rows}
        loading={lv.loading}
        onChange={lv.onTableChange}
        pagination={lv.pagination}
        sticky
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

      <EditorModal path={editPath} onClose={() => setEditPath(null)} onSaved={lv.reload} />
    </>
  );
}
