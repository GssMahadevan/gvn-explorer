import CodeMirror from '@uiw/react-codemirror';
import { markdown } from '@codemirror/lang-markdown';
import { json } from '@codemirror/lang-json';
import { html } from '@codemirror/lang-html';
import { useIsDark } from '../theme';

// CodeMirror 6 — the editor family behind GitHub's in-browser file editing.
// Chosen over Monaco: an order of magnitude smaller in the bundle, and it
// behaves properly in phone browsers, which this GUI has to support.
// Only plain documents land here; YAML files get the structured editor.

const langFor = (path: string) => {
  const ext = path.slice(path.lastIndexOf('.') + 1).toLowerCase();
  if (ext === 'md' || ext === 'markdown') return [markdown()];
  if (ext === 'json') return [json()];
  if (ext === 'html' || ext === 'htm') return [html()];
  return []; // unknown text type: plain mode, still fully editable
};

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
  return (
    <div
      data-testid="doc-editor"
      style={{ border: '1px solid rgba(128,128,128,0.4)', borderRadius: 6, overflow: 'hidden' }}
    >
      <CodeMirror
        value={value}
        theme={dark ? 'dark' : 'light'}
        extensions={langFor(path)}
        editable={!readOnly}
        height="55vh"
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
