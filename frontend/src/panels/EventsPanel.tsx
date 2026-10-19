import { Alert } from 'antd';
import type { Me } from '../types';
import EventsTable from '../components/EventsTable';

// Events panel: the audit trail table (components/EventsTable) plus the
// scope note. The same component is what the flash bar's event log
// drawer shows for admins — this panel is its full-tab form.
export default function EventsPanel({ me, visible }: { me: Me; visible: boolean }) {
  return (
    <>
      <Alert
        style={{ marginBottom: 8 }}
        type="info"
        message={
          me.is_super
            ? 'append-only trail of every authentication, account, permission and published-file change, across all tenants; expand a row for its structured details'
            : 'append-only trail scoped to your tenant — every authentication, account, permission and published-file change that touched it; expand a row for its structured details'
        }
      />
      <EventsTable me={me} visible={visible} />
    </>
  );
}
