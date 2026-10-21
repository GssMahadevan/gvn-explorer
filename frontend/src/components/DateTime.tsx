// The one renderer for every timestamp the app displays, so the same
// kind of value always looks the same everywhere: wall-clock time with
// millisecond precision in the viewer's own zone (two audit rows a
// second apart are finally distinguishable by eye), and the exact
// unambiguous instant as the hover title in ISO/UTC.
//
// `v` accepts whatever an endpoint carries: the *_ms numbers the API
// now publishes (preferred at every call site), unix seconds, or an
// RFC3339 string — magnitude tells numbers apart (~1.7e9 vs ~1.7e12).
// dateOnly renders just the day, for values that are calendar facts
// rather than moments (account/grant expiry tags).

interface Props {
  v: string | number;
  dateOnly?: boolean;
}

export function toDate(v: string | number): Date {
  if (typeof v === 'number') return new Date(v >= 1e11 ? v : v * 1000);
  return new Date(v);
}

const p2 = (n: number) => String(n).padStart(2, '0');

// fmtLocal is the same wall-clock string the component renders, for
// places that need text rather than an element (modal prose).
export function fmtLocal(v: string | number): string {
  const d = toDate(v);
  if (isNaN(d.getTime())) return String(v);
  const date = `${d.getFullYear()}-${p2(d.getMonth() + 1)}-${p2(d.getDate())}`;
  return `${date} ${p2(d.getHours())}:${p2(d.getMinutes())}:${p2(d.getSeconds())}.${String(
    d.getMilliseconds(),
  ).padStart(3, '0')}`;
}

export default function DateTime({ v, dateOnly }: Props) {
  const d = toDate(v);
  if (isNaN(d.getTime())) return <span className="mono">{String(v)}</span>;
  const text = dateOnly ? fmtLocal(v).split(' ')[0] : fmtLocal(v);
  return (
    <span className="mono" title={d.toISOString()}>
      {text}
    </span>
  );
}
