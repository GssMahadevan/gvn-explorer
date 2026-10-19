import { useState } from 'react';
import { Button, Input } from 'antd';
import { mono, type LeafAccess } from './yamlutil';

// Commit-on-blur leaf input: typing never re-parses the document on
// every keystroke, and the element key pins it to the current raw value
// so an external reload (discard draft / publish) resets what is shown.
export default function YamlLeaf({
  value,
  onCommit,
  multiline,
  monoFont = true,
  disabled,
  placeholder,
  testId,
}: {
  value: string;
  onCommit: (v: string) => void;
  multiline?: boolean;
  monoFont?: boolean;
  disabled?: boolean;
  placeholder?: string;
  testId?: string;
}) {
  const Comp: any = multiline ? Input.TextArea : Input;
  return (
    <Comp
      key={value}
      defaultValue={value}
      autoSize={multiline ? { minRows: 1, maxRows: 16 } : undefined}
      size="small"
      disabled={disabled}
      placeholder={placeholder}
      data-testid={testId}
      style={monoFont ? mono : undefined}
      onBlur={(e: { target: { value: string } }) => {
        const v = e.target.value;
        if (v !== value) onCommit(v);
      }}
      onPressEnter={multiline ? undefined : (e: { target: { blur: () => void } }) => e.target.blur()}
    />
  );
}

// "Note above the line" editor — the why-comments these config files
// carry. Starts as a quiet # button; becomes a real textarea once the
// note exists or the operator asks for one.
export function YamlNoteEditor({
  acc,
  testPath,
  readOnly,
}: {
  acc: LeafAccess;
  testPath: string;
  readOnly?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const has = acc.hasNote();
  if (!has && !open)
    return readOnly ? null : (
      <Button size="small" type="text" onClick={() => setOpen(true)} title="add a note above this line">
        # note
      </Button>
    );
  return (
    <YamlLeaf
      value={acc.getNote()}
      multiline
      disabled={readOnly}
      placeholder="# note above this line"
      testId={`yaml-note-${testPath}`}
      onCommit={(v) => acc.setNote(v)}
    />
  );
}

// inline "# trailing comment" editor for one node/pair.
export function YamlInlineComment({
  acc,
  testPath,
  readOnly,
}: {
  acc: LeafAccess;
  testPath: string;
  readOnly?: boolean;
}) {
  if (readOnly && !acc.hasComment()) return null;
  return (
    <YamlLeaf
      value={acc.getComment()}
      disabled={readOnly}
      placeholder="# comment"
      testId={`yaml-cmt-${testPath}`}
      onCommit={(v) => acc.setComment(v)}
    />
  );
}
