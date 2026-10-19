# GUI Framework Survey (October 2026)

> **Decision (2026-10-19): migrated to React 19 + Ant Design** — the
> console now lives in `frontend/` (Vite, MIT stack). The vanilla `web/`
> GUI was kept as a zero-toolchain fallback for a while and has since
> been removed (it shadowed the real GUI when release binaries were run
> from a checkout). See README "Web GUI".

Goal: replace the hand-written `web/app.js` (vanilla JS, ~1,100 LOC) with a
framework + established MIT/Apache-licensed component library, for a
file-explorer + admin CRUD console. The backend JSON API (server-side
paging / column-search / sort on all 6 list endpoints, `X-Total-Count`)
stays untouched — every candidate consumes it as-is.

Deployment model unchanged: the framework builds to static files
(`web/dist/`) which the Go binary serves via the existing `-web` flag
(or nginx, if preferred). A build step is dev-time only; `go:embed`
can later fold `dist/` into the single binary.

---

## 1. What the current GUI needs from a library

| Feature used today | Component needed |
|---|---|
| 5 tabbed panels (files, perms, users, groups, tenants) | Tabs |
| paged tables, sortable headers, sticky thead | Data table with **server-side** pagination + sort |
| per-column search select + debounced box | Filter inputs on table headers |
| rows-per-page dropdown, first/prev/next/last | Table pagination control |
| breadcrumb file browser, dir-first list | Breadcrumb + table (or Tree for a future browser) |
| editors as fixed overlay, toasts, badges | Modal/Dialog, Toast/Notification, Tag |
| login screen, small forms | Form + validation |

Enterprise-oriented libraries ship **all** of this pre-built, accessible,
and themable — that is exactly what hand-rolling could not give us.

---

## 2. Framework cores

| Core | License | Notes (2026 state) |
|---|---|---|
| **React 19** | MIT | Biggest component-library selection; most hiring/docs momentum |
| **Vue 3.5+** | MIT | Gentalest learning curve from vanilla JS; SFCs; strong admin-UI ecosystem |
| **Svelte 5** | MIT | Great DX, but far fewer enterprise-grade grid/table options |
| **SolidJS** | MIT | Fast, small ecosystem; few polished component suites |
| **No core — Web Components** | varies | Framework-free custom elements; can keep app.js as glue (see §4) |

---

## 3. Component libraries (all permissive-licensed)

### For React

| Library | License | Look | Table/grid for our API | Verdict for this app |
|---|---|---|---|---|
| **Ant Design v5** (antd) | MIT | Clean desktop-enterprise | `Table` has built-in server-side pagination, header sort, column filter; `Tree` + `Breadcrumb` ideal for file explorer | **Strongest fit** — Table/Tree/Descriptions cover every panel out of the box |
| **MUI v7** (+ DataGrid core) | MIT | Material (Google) | DataGrid core is MIT with paging/sort/filter; server-side mode via `server` pagination model | Excellent; Material look; larger design footprint |
| **Mantine v8** | MIT | Modern SaaS | `DataTable` (MIT) is server-side-mode first-class | Best "most complete free library" per 2026 comparisons |
| **Fluent UI React v9** | MIT | Microsoft/Windows native | `DataGrid` is lower-level (needs glue); `DetailsList` alternative | Choose if Windows-native look is the goal |
| **React Spectrum (S2)** | Apache-2.0 | Adobe | Table exists but heavier patterns | Solid; smaller community outside Adobe |
| **Carbon v11** (`@carbon/react`) | Apache-2.0 | IBM enterprise | `DataTable` + `StructuredList`; pagination glue manual | Good, but grids less batteries-included |
| **shadcn/ui** (Base UI, copy-in) | MIT | Tailwind neutral | Bring TanStack Table (MIT) yourself | Great for bespoke SaaS; more assembly work here |

### For Vue

| Library | License | Look | Table/grid | Verdict |
|---|---|---|---|---|
| **Element Plus 2.x** | MIT | Clean desktop (Element admin heritage) | `el-table` + `el-pagination`: server paging/sort idiomatic, huge admin-template ecosystem | **Strongest Vue fit**; very close feature parity to antd |
| **Naive UI** | MIT | Modern, themeable | Full table w/ server paging | Good, smaller community |
| **Vuetify 4** | MIT | Material | `v-data-table` with `server-items-length` | Fine, but Material override work if you don't want Material |
| **Ant Design Vue** | MIT | Ant desktop | full port of antd Table | Fine; port trails React original in cadence |
| **PrimeVue** | MIT | Neutral | `DataTable` + lazy mode | Very complete incl. tree |

### Grids (framework-agnostic)

| Grid | License | Caveat |
|---|---|---|
| **AG Grid Community** | MIT | Sorting/filtering/paging/virtualization free; **Server-Side Row Model is paid Enterprise** — for our ≤1,000-row pages the client model + our API paging is enough |
| **TanStack Table** | MIT | Headless; pair with any styled lib (shadcn, Mantine, Element) |

### Web Components (no core framework needed)

| Library | License | Notes |
|---|---|---|
| **Web Awesome** 3.x | MIT | Successor of Shoelace, maintained under Font Awesome; tables, dialogs, toasts; **drop-in `<wa-*>` tags, no build toolchain** |
| **Carbon Web Components** | Apache-2.0 | IBM look, works without any framework |
| **Fluent UI Web Components** | MIT | Microsoft look, works without any framework |
| **Spectrum Web Components** | Apache-2.0 | Adobe look |
| **Material Web (MWC)** | Apache-2.0 | **Maintenance mode pending new maintainers — do not start new projects on it (2026)** |

---

## 4. Build & serving integration

| Option | Toolchain | Serving |
|---|---|---|
| React/Vue + lib (antd, MUI, Mantine, Element Plus) | **Node + Vite** dev-time; output `web/dist/` static | Go `-web web/dist` unchanged, or nginx; optional `go:embed` later |
| Web Components only (Web Awesome / Carbon WC / Fluent WC) | **None** — vendor 1–2 JS files into `web/` | Exactly today's setup; app.js stays as glue |
| shadcn/ui + TanStack | Node + Vite + Tailwind | same as row 1 |

Dev UX for Vite stacks: `vite dev` with proxy to `:8095`, or just
`npm run build && ./server -web web/dist`. The 219-assertion curl suite
and Go tests are unaffected either way.

---

## 5. "Native look of the platform" reality check

There is no mainstream, maintained macOS-native-look web component suite.
Honest mapping of permissive-licensed design systems to platform looks:

- Windows/native-ish: **Fluent UI** (Microsoft's design system, MIT)
- Android/Google: **MUI / Vuetify** (Material, MIT)
- neutral enterprise desktop: **Ant Design**, **Element Plus** (MIT)
- IBM/Apache enterprise: **Carbon** (Apache-2.0)
- Adobe: **Spectrum** (Apache-2.0)

Any of them can be restyled with CSS custom properties/design tokens;
none will feel *literally* like a Cocoa app — that bar is unmet by the
web platform regardless of library.

---

## 6. Performance under huge file lists (client renders everything)

Deployment model confirmed by the requirements: **Go serves only the
static bundle + REST API — zero server-side JS; all list rendering,
sorting feedback and interaction CPU lives in the browser.** Our design
already matches (no SSR anywhere). The question then becomes: *what
happens in the browser when a directory has 50k–1M entries?*

### Measured 2026 data (reproducible fixtures)

50,000 rows × 20 cols, 1200×600 viewport, production builds (vitashev
react-data-grid-benchmark, Jun 2026, M1 Pro):

| Grid (all MIT unless noted) | JS gzip | Ready | Scroll settle | Verdict |
|---|--:|--:|--:|---|
| React Data Grid 7 | 72 KB | 368 ms | 21 ms | leanest |
| TanStack Table + Virtual | 79 KB | 369 ms | 22 ms | DIY styling |
| **AG Grid Community 35** | 359 KB | 443 ms | 30 ms | automatic row+col virtualization |
| MUI X DataGrid Community | — | — | — | **MIT tier is capped at 100 rows/page pagination — cannot virtualize free-scroll lists; excluded from big-list duty** |

Consensus conclusion of the 2026 studies: *"at 50k rows the renderer is
not the bottleneck for anyone… Nobody is slow at 50k rows. Pick on
features, not FPS, below ~100k."* The plain non-virtualized table
(what a naive hand-rolled UI does) is the only one that **fails**:
2.6 s at 50k, never finishes at 100k. **Virtualization is mandatory
past ~1k–5k rows; every candidate below has it.**

1,000,000 rows (hckhanh stress bench, refreshed 2026-10-01): all
virtualized grids mount in 1–3 ms, but *keeping 1M rows as JS objects
in the browser* costs 230 MB–3 GB of heap (Glide canvas 230 MB, AG Grid
~460–690 MB, TanStack DOM model up to ~3 GB). Conclusion: the dataset
residency, not the framework, is the real 1M-row cost.

### Framework runtime overhead (krausest js-framework-benchmark, Chrome 147, weighted geomean slowdown vs vanilla)

| Runtime | Speed slowdown | Memory factor | Initial JS (brotli) |
|---|--:|--:|--:|
| vanilla JS | 1.00 | 1.00 | ~2.5 KB |
| Svelte 5 | 1.10 | 1.46 | ~12 KB |
| Solid | 1.10 | 1.21 | ~4.5 KB |
| **Vue 3** | 1.22 | 1.84 | ~23 KB |
| **React 19** | 1.43 | 2.49 | ~51 KB |

Real-world but small — and irrelevant *inside* a virtualized grid,
because the grid recycles ~30–50 DOM nodes no matter the row count.
Choose framework on ecosystem/ergonomics, not these factors.

### Two strategies for huge directories in the browser

| Strategy | How it maps to our API | Memory | When |
|---|---|---|---|
| **A. Lazy chunks as you scroll** (AG Grid *Infinite Row Model* — free tier; or page-sized fetches) | `?limit&offset` already is the chunk contract; zero backend change | tiny, bounded | the default; works to millions of rows |
| B. Fetch-all + virtualize client-side (instant client sort/search) | omit `limit` (our API supports it) | 100 MB+ past ~100k entries | only for bounded sets |

Recommend **A** with AG Grid's Infinite Row Model or equivalent paging.

### Grid picks, performance-first

- **AG Grid Community (MIT)** — the large-list benchmark of record;
  automatic row+column virtualization, Infinite Row Model free;
  adapters for React, Vue **and vanilla JS** (yes — usable with no
  framework at all). Cost: ~300–360 KB gzip bundle.
- **Ant Design Table `virtual`** / **Element Plus `el-table-v2`** /
  **Mantine `DataTable` virtualization** — all MIT, all comfortable to
  ~100k rows per the studies above; ~zero extra cost since they come
  with the UI kit we'd use anyway.
- Glide Data Grid (MIT, canvas renderer) — fastest at 500k+ rows
  (24 ms) but text isn't selectable and find-in-page is dead; overkill
  and hostile UX for a file console.

## 7. Revised shortlist (performance criterion included)

1. **React + Ant Design** — MIT; `Table virtual` handles our biggest
   realistic dirs; Tree/Breadcrumb/Forms all in one lib. Safe default.
2. **React or Vue + AG Grid Community** — best-in-class huge lists
   (Infinite Row Model ↔ our limit/offset API 1:1); React or Vue (or
   vanilla) shell around it; +Mantine or Element Plus for non-grid UI.
3. **Vue 3 + Element Plus (`el-table-v2`)** — MIT; runtime ~⅓ lighter
   than React (speed and memory), ~half the initial bundle; classic
   admin look.
4. **Vanilla + AG Grid Community (+ Web Awesome tags)** — no framework
   at all, AG Grid works framework-free; minimal diff, serious grid —
   but forms/tabs/toasts stay hand-written glue.
5. React + Mantine / Fluent UI — fine below ~100k rows; see §3 notes.

Avoid for this app: MUI/MUI X for the list views (MIT tier 100-row
page cap), Material Web (maintenance mode), plain non-virtualized
tables of any kind.

Migration plan (any stack): keep the `apiP` fetch layer as a thin
service module; port panels one at a time (files → perms →
users/groups/tenants); build to `web/dist` (Vite), serve via existing
`-web` flag; re-run the 219-assertion curl suite — zero backend
changes. Production ships as an npm-built static bundle exactly as
requested; the Go binary never runs JS.
