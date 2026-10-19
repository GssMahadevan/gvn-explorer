import { App, Button, Dropdown } from 'antd';
import type { MenuProps } from 'antd';
import { MoreOutlined } from '@ant-design/icons';

// Per-row action menu: every action a row offers lives behind one vertical
// three-dots button, so tables stay one column wide and usable on phone
// widths no matter how many actions an entity has. Actions are plain data;
// danger styles the item red, and confirm pops a themed confirm dialog
// (via the App context, so dark mode carries over) before running.
export type RowAction = {
  key: string;
  label: string;
  onClick: () => void;
  danger?: boolean;
  disabled?: boolean;
  confirm?: { title: string; description?: string };
};

export default function RowActions({ actions }: { actions: RowAction[] }) {
  const { modal } = App.useApp();
  const items: MenuProps['items'] = actions.map((a) => ({
    key: a.key,
    label: a.label,
    danger: a.danger,
    disabled: a.disabled,
    onClick: () =>
      a.confirm
        ? modal.confirm({
            title: a.confirm.title,
            content: a.confirm.description,
            okText: a.label,
            okButtonProps: { danger: a.danger },
            onOk: () => a.onClick(),
          })
        : a.onClick(),
  }));
  return (
    <Dropdown menu={{ items }} trigger={['click']}>
      <Button
        type="text"
        size="small"
        aria-label="row actions"
        data-testid="row-actions"
        icon={<MoreOutlined />}
      />
    </Dropdown>
  );
}
