import { useRef } from 'react';
import CodeMirror from '@uiw/react-codemirror';
import { markdown } from '@codemirror/lang-markdown';
import { json } from '@codemirror/lang-json';
import { html } from '@codemirror/lang-html';
import { EditorSelection } from '@codemirror/state';
import type { EditorView } from '@codemirror/view';
import { Button, Tooltip } from 'antd';
import {
  BoldOutlined,
  ItalicOutlined,
  LinkOutlined,
  OrderedListOutlined,
  StrikethroughOutlined,
  UnorderedListOutlined,
} from '@ant-design/icons';
import { useIsDark } from '../theme';

// CodeMirror 6 — the editor family behind GitHub's in-browser file editing.
// Chosen over Monaco: an order of magnitude smaller in the bundle, and it
// behaves properly in phone browsers, which this GUI has to support.
// Markdown files get a formatting ribbon above the editor (GitHub-comment
// style: the icons edit the markdown source around the selection, so the
// file always stays plain lossless markdown). YAML files never land here —
// they get the structured editor.

const langFor = (path: string) => {
  const ext = path.slice(path.lastIndexOf('.') + 1).toLowerCase();
  if (ext === 'md' || ext === 'markdown') return [markdown()];
  if (ext === 'json') return [json()];
  if (ext === 'html' || ext === 'htm') return [html()];
  return []; // unknown text type: plain mode, still fully editable
};

const isMd = (path: string) => /\.(md|markdown)$/i.test(path);

export default function DocEditor({
  path,
  value,
  readOnly,
  onChange,
}: {
  path: string;
  value: string;
  readOnly: boolean;
  onChange: (v: string) => void;
}) {
  const dark = useIsDark();
  const viewRef = useRef<EditorView | null>(null);

  // ---- ribbon actions (operate on the CodeMirror selection) ----
  const wrapSel = (before: string, after = before) => {
    const view = viewRef.current;
    if (!view) return;
    const st = view.state;
    const tr = st.changeByRange((range) => {
      const sel = st.sliceDoc(range.from, range.to);
      return {
        changes: { from: range.from, to: range.to, insert: before + sel + after },
        range: EditorSelection.range(range.from + before.length, range.from + before.length + sel.length),
      };
    });
    view.dispatch(tr, { userEvent: 'input.format' });
    view.focus();
  };

  const prefixLines = (prefix: string | ((i: number) => string)) => {
    const view = viewRef.current;
    if (!view) return;
    const st = view.state;
    const tr = st.changeByRange((range) => {
      const changes: { from: number; insert: string }[] = [];
      let i = 0;
      for (let n = st.doc.lineAt(range.from).number; n <= st.doc.lineAt(range.to).number; n++, i++) {
        const p = typeof prefix === 'function' ? prefix(i) : prefix;
        changes.push({ from: st.doc.line(n).from, insert: p });
      }
      const shift = (pos: number) => changes.reduce((s, c) => s + (c.from < pos ? c.insert.length : 0), 0);
      return {
        changes,
        range: EditorSelection.range(range.from + shift(range.from), range.to + shift(range.to)),
      };
    });
    view.dispatch(tr, { userEvent: 'input.format' });
    view.focus();
  };

  const tools = [
    { id: 'bold', title: 'bold (wraps selection in **)', node: <BoldOutlined />, run: () => wrapSel('**') },
    { id: 'italic', title: 'italic (*)', node: <ItalicOutlined />, run: () => wrapSel('*') },
    { id: 'strike', title: 'strikethrough (~~)', node: <StrikethroughOutlined />, run: () => wrapSel('~~') },
    { id: 'h1', title: 'heading 1', node: 'H1', run: () => prefixLines('# ') },
    { id: 'h2', title: 'heading 2', node: 'H2', run: () => prefixLines('## ') },
    { id: 'h3', title: 'heading 3', node: 'H3', run: () => prefixLines('### ') },
    { id: 'quote', title: 'blockquote (>)', node: '❝', run: () => prefixLines('> ') },
    { id: 'ul', title: 'bullet list', node: <UnorderedListOutlined />, run: () => prefixLines('- ') },
    { id: 'ol', title: 'numbered list', node: <OrderedListOutlined />, run: () => prefixLines((i) => `${i + 1}. `) },
    { id: 'link', title: 'link', node: <LinkOutlined />, run: () => wrapSel('[', '](url)') },
    { id: 'code', title: 'inline code (`)', node: '</>', run: () => wrapSel('`') },
    { id: 'codeblock', title: 'code block (```)', node: '```', run: () => wrapSel('```\n', '\n```') },
  ];

  const ribbon = !readOnly && isMd(path);

  return (
    <div
      data-testid="doc-editor"
      style={{ border: '1px solid rgba(128,128,128,0.4)', borderRadius: 6, overflow: 'hidden' }}
    >
      {ribbon && (
        <div
          data-testid="doc-toolbar"
          style={{
            display: 'flex',
            flexWrap: 'wrap',
            gap: 2,
            padding: '4px 6px',
            borderBottom: '1px solid rgba(128,128,128,0.35)',
          }}
        >
          {tools.map((t) => (
            <Tooltip key={t.id} title={t.title}>
              <Button size="small" type="text" data-testid={`doc-tool-${t.id}`} onClick={t.run}>
                {t.node}
              </Button>
            </Tooltip>
          ))}
        </div>
      )}
      <CodeMirror
        value={value}
        theme={dark ? 'dark' : 'light'}
        extensions={langFor(path)}
        editable={!readOnly}
        height="55vh"
        onCreateEditor={(view) => {
          viewRef.current = view;
        }}
        basicSetup={{
          lineNumbers: true,
          foldGutter: true,
          highlightActiveLine: false,
        }}
        onChange={(v) => onChange(v)}
      />
    </div>
  );
}
