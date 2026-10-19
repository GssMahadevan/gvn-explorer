import { useEffect, useState } from 'react';
import { Alert, Select, Table, Tag } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { api, qs } from '../api';
import type { EventRow, Me, Tenant } from '../types';
import { useListView } from '../useListView';
import ListSearch from '../components/ListSearch';

// Events panel: the forensic audit trail (the append-only `events` table)
// as a full table — time, actor, tenant, action, target, path, source IP,
// and the structured details of each entry behind an expand row. All
// filtering is server-side like every other list (limit/offset/q/col/
// sort/dir) plus the audit-only params the API accepts: action=, since=.
// Super admins see every tenant; tenant admins see events scoped to their
// own tenant (the server decides, the panel just shows the scope).
const ACTIONS = [
  'login', 'login_failed',
  'user_create', 'user_update', 'user_password', 'user_active', 'user_delete',
  'factor_create', 'factor_delete',
  'group_create', 'group_update', 'group_active', 'group_delete',
  'member_add', 'member_remove',
  'perm_set', 'perm_delete',
  'fs_commit',
  'tenant_create', 'tenant_update', 'tenant_active', 'tenant_delete',
];

const RANGES = [
  { value: '', label: 'any time' },
  { value: '3600', label: 'last hour' },
  { value: '86400', label: 'last 24 hours' },
  { value: '604800', label: 'last 7 days' },
];

export default function EventsPanel({ me, visible }: { me: Me; visible: boolean }) {
  const [action, setAction] = useState('');
  const [range, setRange] = useState('');

  // super admins see every tenant mixed together, so resolve tenant_id
  // to a name once (the audit rows only carry ids).
  const [tenantNames, setTenantNames] = useState<Record<number, string>>({});
  useEffect(() => {
    if (!me.is_super) return;
    api<Tenant[]>('/api/v1/tenants?limit=1000')
      .then((ts) => setTenantNames(Object.fromEntries(ts.map((t) => [t.id, t.name]))))
      .catch(() => {}); // best-effort decoration
  }, [me.is_super]);

  const lv = useListView<EventRow[]>((s) => {
    let u = `/api/v1/events?${qs(s)}`;
    if (action) u += '&action=' + encodeURIComponent(action);
    if (range)
      u += '&since=' + encodeURIComponent(new Date(Date.now() - Number(range) * 1000).toISOString());
    return u;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, { sort: 'ts', dir: 'desc', limit: 50 });

  // panels stay mounted, so the list fetched at boot is already stale by
  // the time the tab is opened: the trail is append-only, refresh on every
  // reveal so the newest events are always what you see.
  useEffect(() => {
    if (visible) lv.reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [visible]);

  const columns: ColumnsType<EventRow> = [
    { title: 'id', key: 'id', dataIndex: 'id', width: 64 },
    {
      title: 'time',
      key: 'ts',
      sorter: true,
      sortOrder: lv.sortOrder('ts'),
      showSorterTooltip: false,
      width: 180,
      render: (_, e) => new Date(e.ts * 1000).toLocaleString(),
    },
    {
      title: 'actor',
      key: 'actor',
      sorter: true,
      sortOrder: lv.sortOrder('actor'),
      showSorterTooltip: false,
      render: (_, e) => e.actor || <Tag>anonymous</Tag>,
    },
    ...(me.is_super
      ? [
          {
            title: 'tenant',
            key: 'tenant',
            width: 120,
            render: (_: unknown, e: EventRow) =>
              e.tenant_id ? tenantNames[e.tenant_id] ?? `#${e.tenant_id}` : <Tag>platform</Tag>,
          } as ColumnsType<EventRow>[number],
        ]
      : []),
    {
      title: 'action',
      key: 'action',
      sorter: true,
      sortOrder: lv.sortOrder('action'),
      showSorterTooltip: false,
      width: 140,
      render: (_, e) => <span className="mono">{e.action}</span>,
    },
    {
      title: 'target',
      key: 'target',
      sorter: true,
      sortOrder: lv.sortOrder('target'),
      showSorterTooltip: false,
      render: (_, e) =>
        e.target_name || e.target_type ? (
          <span className="mono">
            {[e.target_type, e.target_name].filter(Boolean).join(' ')}
          </span>
        ) : (
          '—'
        ),
    },
    {
      title: 'path',
      key: 'path',
      sorter: true,
      sortOrder: lv.sortOrder('path'),
      showSorterTooltip: false,
      render: (_, e) => (e.path ? <span className="mono">{e.path}</span> : '—'),
    },
    {
      title: 'ip',
      key: 'ip',
      width: 130,
      render: (_, e) => (e.ip ? <span className="mono">{e.ip}</span> : '—'),
    },
  ];

  return (
    <>
      <div className="bar">
        <span>audit event log</span>
        <Select
          style={{ width: 150 }}
          value={action}
          onChange={setAction}
          data-testid="events-action"
          showSearch
          optionFilterProp="label"
          options={[{ value: '', label: 'all actions' }, ...ACTIONS.map((a) => ({ value: a, label: a }))]}
        />
        <Select
          style={{ width: 140 }}
          value={range}
          onChange={setRange}
          data-testid="events-range"
          options={RANGES}
        />
        <span className="grow" />
        <ListSearch
          state={lv.state}
          patch={lv.patch}
          cols={['actor', 'action', 'target', 'path']}
          placeholder="search events"
        />
      </div>

      <Alert
        style={{ marginBottom: 8 }}
        type="info"
        message={
          me.is_super
            ? 'append-only trail of every authentication, account, permission and published-file change, across all tenants; expand a row for its structured details'
            : 'append-only trail scoped to your tenant — every authentication, account, permission and published-file change that touched it; expand a row for its structured details'
        }
      />

      <Table
        rowKey={(e) => e.id}
        size="small"
        data-testid="events-table"
        columns={columns}
        dataSource={lv.payload ?? []}
        loading={lv.loading}
        onChange={lv.onTableChange}
        pagination={lv.pagination}
        sticky
        scroll={{ x: 'max-content' }}
        expandable={{
          expandedRowRender: (e) => (
            <pre className="mono" data-testid="event-details" style={{ margin: 0, whiteSpace: 'pre-wrap' }}>
              {JSON.stringify(e.details, null, 2)}
            </pre>
          ),
          rowExpandable: (e) => !!e.details && Object.keys(e.details).length > 0,
        }}
        locale={{
          emptyText: lv.state.q ? `nothing matches "${lv.state.q}"` : 'no events',
        }}
      />
    </>
  );
}
