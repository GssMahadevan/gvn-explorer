// Flash centre: one place for app feedback (API errors, action results)
// instead of antd toasts floating over the UI. Entries render in a strip
// directly under the app header for FLASH_TTL ms, then hide — the full
// log stays reachable behind the badge button (or by clicking any entry).

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import type { ReactNode } from 'react';
import { Badge, Button, Popover, theme } from 'antd';

export type FlashKind = 'error' | 'warn' | 'info' | 'success';

export interface FlashItem {
  id: number;
  ts: number; // ms epoch; visible in the strip until ts + FLASH_TTL
  kind: FlashKind;
  text: string;
}

export const FLASH_TTL = 30_000; // strip lifetime per entry
const CAP = 100; // history cap
const HISTORY_SHOW = 50; // entries listed in the log popover

interface FlashApi {
  items: FlashItem[]; // newest first; the strip filters by TTL itself
  push: (kind: FlashKind, text: string) => void;
  err: (text: string) => void;
  warn: (text: string) => void;
  info: (text: string) => void;
  ok: (text: string) => void;
  clear: () => void;
}

const Ctx = createContext<FlashApi | null>(null);

export function useFlash(): FlashApi {
  const c = useContext(Ctx);
  if (!c) throw new Error('useFlash called outside FlashProvider');
  return c;
}

export function FlashProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<FlashItem[]>([]);
  const seq = useRef(0);

  const push = useCallback((kind: FlashKind, text: string) => {
    setItems((v) =>
      [{ id: ++seq.current, ts: Date.now(), kind, text: String(text ?? '') }, ...v].slice(0, CAP),
    );
  }, []);
  const clear = useCallback(() => setItems([]), []);

  const value = useMemo<FlashApi>(
    () => ({
      items,
      push,
      clear,
      err: (t: string) => push('error', t),
      warn: (t: string) => push('warn', t),
      info: (t: string) => push('info', t),
      ok: (t: string) => push('success', t),
    }),
    [items, push, clear],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

const KIND_COLOR: Record<FlashKind, string> = {
  error: '#ff4d4f',
  warn: '#fa8c16',
  info: '#1677ff',
  success: '#52c41a',
};

const fmtTime = (ts: number) => new Date(ts).toLocaleTimeString();

const Dot = ({ k }: { k: FlashKind }) => (
  <span
    style={{ width: 8, height: 8, borderRadius: 4, background: KIND_COLOR[k], flex: '0 0 auto' }}
  />
);

// The strip itself. Lives between the header and the content, so mount
// it inside the provider and the themed ConfigProvider.
export function FlashBar() {
  const { items, clear } = useFlash();
  const { token } = theme.useToken();
  const [now, setNow] = useState(() => Date.now());
  const [logOpen, setLogOpen] = useState(false);

  // one chained timer: wake up when the earliest live entry expires;
  // the effect re-arms (via `now`) until none are left
  useEffect(() => {
    const upcoming = items.map((it) => it.ts + FLASH_TTL).filter((t) => t > now);
    if (!upcoming.length) return;
    const t = setTimeout(() => setNow(Date.now()), Math.max(100, Math.min(...upcoming) - now));
    return () => clearTimeout(t);
  }, [items, now]);

  // live = still inside the TTL window; dedupe identical texts — when a
  // session dies every mounted panel's next fetch fails the same way.
  const live: FlashItem[] = [];
  const seen = new Set<string>();
  for (const it of items) {
    if (it.ts <= now - FLASH_TTL) continue;
    if (seen.has(it.text)) continue;
    seen.add(it.text);
    live.push(it);
  }

  if (!items.length) return null; // no history, no bar

  const history = (
    <div style={{ width: 480 }}>
      <div style={{ maxHeight: 320, overflowY: 'auto' }}>
        {items.slice(0, HISTORY_SHOW).map((it) => (
          <div key={it.id} className="flash-row">
            <Dot k={it.kind} />
            <span className="flash-text">{it.text}</span>
            <span style={{ color: token.colorTextTertiary, fontSize: 12, flex: '0 0 auto' }}>
              {fmtTime(it.ts)}
            </span>
          </div>
        ))}
      </div>
      <div style={{ textAlign: 'right', marginTop: 8 }}>
        <Button
          size="small"
          onClick={() => {
            clear();
            setLogOpen(false);
          }}
        >
          clear
        </Button>
      </div>
    </div>
  );

  return (
    <div
      className="flash-bar"
      data-testid="flash-bar"
      style={{ background: token.colorFillQuaternary, borderBottom: `1px solid ${token.colorSplit}` }}
    >
      {live.map((it) => (
        <div
          key={it.id}
          className="flash-item"
          title="click to open the event log"
          onClick={() => setLogOpen(true)}
        >
          <Dot k={it.kind} />
          <span className="flash-text" style={{ color: KIND_COLOR[it.kind] }}>
            {it.text}
          </span>
        </div>
      ))}
      <span style={{ flex: 1 }} />
      <Popover
        open={logOpen}
        onOpenChange={setLogOpen}
        content={history}
        trigger="click"
        placement="bottomRight"
      >
        <Badge count={Math.min(items.length, 99)} size="small" offset={[-2, 2]}>
          <Button size="small">event log</Button>
        </Badge>
      </Popover>
    </div>
  );
}
