import { useEffect, useRef, useState } from 'react';
import type { TablePaginationConfig } from 'antd';
import { apiList, qs } from './api';
import type { ActiveFilter, ListState } from './api';
import { useFlash } from './flash';

export const PAGE_SIZES = [10, 25, 50, 100];

// antd's sorter param is invariantly generic; any keeps this shared
// handler assignable to every Table's onChange.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
type AnySorter = any;

export interface ListApi<P> {
  payload: P | null;
  total: number;
  loading: boolean;
  state: ListState;
  patch: (p: Partial<ListState>) => void;
  reload: () => void;
  onTableChange: (pag: TablePaginationConfig, _filters: unknown, sorter: AnySorter) => void;
  sortOrder: (key: string) => 'ascend' | 'descend' | null;
  pagination: TablePaginationConfig;
}

// One panel's list state: paging + search + sort, all server-side —
// identical query-param contract to the old vanilla GUI (limit, offset,
// q, col, sort, dir) and its clampPage step-back after deletes.
export function useListView<P>(
  url: (s: ListState) => string,
  init: { sort: string; dir?: 'asc' | 'desc'; limit?: number; active?: ActiveFilter },
): ListApi<P> {
  const flash = useFlash();
  const [state, setState] = useState<ListState>({
    limit: init.limit ?? 25,
    page: 1,
    q: '',
    col: '',
    sort: init.sort,
    dir: init.dir ?? 'asc',
    active: init.active ?? '',
  });
  const [payload, setPayload] = useState<P | null>(null);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [nonce, setNonce] = useState(0);
  const reqId = useRef(0);

  const href = url(state);
  useEffect(() => {
    const id = ++reqId.current;
    setLoading(true);
    apiList<P>(href)
      .then(({ data, total }) => {
        if (id !== reqId.current) return;
        const off = (state.page - 1) * state.limit;
        if (total > 0 && off >= total) {
          // page moved past the end (deletes): step back and refetch
          setState((v) => ({ ...v, page: Math.floor((total - 1) / v.limit) + 1 }));
          return;
        }
        setPayload(data);
        setTotal(total);
      })
      .catch((err: Error) => {
        if (id === reqId.current) flash.err(err.message);
      })
      .finally(() => {
        if (id === reqId.current) setLoading(false);
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [href, nonce]);

  const patch = (p: Partial<ListState>) => setState((v) => ({ ...v, ...p }));
  const reload = () => setNonce((n) => n + 1);

  const onTableChange: ListApi<P>['onTableChange'] = (pag, _f, sorter) => {
    const next: Partial<ListState> = {
      page: pag.current ?? 1,
      limit: pag.pageSize ?? state.limit,
    };
    const so = Array.isArray(sorter) ? sorter[0] : sorter;
    if (so?.columnKey && so.order) {
      next.sort = String(so.columnKey);
      next.dir = so.order === 'descend' ? 'desc' : 'asc';
    }
    setState((v) => ({ ...v, ...next }));
  };

  const sortOrder = (key: string) =>
    state.sort === key ? (state.dir === 'asc' ? 'ascend' : 'descend') : null;

  const pagination: TablePaginationConfig = {
    current: state.page,
    pageSize: state.limit,
    total,
    showSizeChanger: true,
    pageSizeOptions: PAGE_SIZES,
    showTotal: (t, r) => `rows ${r[0]}\u2013${r[1]} of ${t}`,
  };

  return { payload, total, loading, state, patch, reload, onTableChange, sortOrder, pagination };
}

export { qs };
