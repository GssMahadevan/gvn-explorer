import { Select } from 'antd';
import type { ActiveFilter, ListState } from '../api';

// all / active / suspended selector for the entity lists — maps to the
// backend's exact-match active=true|false parameter.
export default function ActiveFilterSelect({
  state,
  patch,
}: {
  state: ListState;
  patch: (p: Partial<ListState>) => void;
}) {
  return (
    <Select
      style={{ width: 120 }}
      value={state.active}
      onChange={(active: ActiveFilter) => patch({ active, page: 1 })}
      options={[
        { value: '', label: 'all' },
        { value: 'true', label: 'active' },
        { value: 'false', label: 'suspended' },
      ]}
    />
  );
}
