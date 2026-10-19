// GUI smoke-walk: login -> files table -> sort -> search -> file page
// (viewer by default: rendered markdown / read-only source / read-only
// structured YAML; edit mode via the row menu or the pane's Edit button)
// -> YAML draft autosave + publish, markdown ribbon + save, download
// links for binaries -> navigate ->
// perms -> users -> tenants, asserting along the way + screenshots.
// Covers the modal-based entity forms (create/edit), the per-row action
// menu (the vertical three-dots on every table row), the active-filter
// select, the flash centre strip under the header, and the event log
// drawer (the full audit table) behind it.
//
// Needs a running server + fixture data; scripts/test_gui.sh boots both.
// Env: BASE_URL (default http://localhost:8096), CHROME_PATH, OUT_DIR.
const fs = require('fs');
const os = require('os');
const path = require('path');
const puppeteer = require('puppeteer-core');
const CHROME = process.env.CHROME_PATH || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const B = process.env.BASE_URL || 'http://localhost:8096';
const OUT = process.env.OUT_DIR || path.join(os.tmpdir(), 'fx-gui');
fs.mkdirSync(OUT, { recursive: true });

(async () => {
  const errors = [];
  const ok = [];
  const fail = [];
  const check = (name, cond) => {
    (cond ? ok : fail).push(name);
    console.log(`  ${cond ? 'PASS' : 'FAIL'} ${name}`);
  };

  const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new' });
  const page = await browser.newPage();
  page.on('console', (m) => m.type() === 'error' && !/Failed to load resource/.test(m.text()) && errors.push(m.text()));
  page.on('pageerror', (e) => errors.push('pageerror: ' + e.message));
  const fsReqs = [];
  const userReqs = [];
  const eventReqs = [];
  page.on('request', (r) => {
    if (r.url().includes('/api/v1/fs?')) fsReqs.push(r.url());
    if (r.url().includes('/api/v1/users?')) userReqs.push(r.url());
    if (r.url().includes('/api/v1/events?')) eventReqs.push(r.url());
  });

  const click = (sel, re) =>
    page.evaluate(
      (s, r) =>
        [...document.querySelectorAll(s)].find((e) =>
          new RegExp(r, 'i').test(e.textContent))?.click(),
      sel,
      re,
    );

  // real mouse click helper (some antd widgets don't respond to synthetic
  // .click() inside evaluate; real input events also mirror users).
  // rowRe additionally requires the button's <tr> text to match. Long
  // tables scroll the target into view first — clicks outside the 800x600
  // viewport would hit nothing.
  const mouseClick = async (sel, re, rowRe) => {
    const p = await page.evaluate((s, r, rr) => {
      let pool = [...document.querySelectorAll(s)].filter((x) => x.offsetParent !== null);
      if (rr) pool = pool.filter((x) => new RegExp(rr, 'i').test(x.closest('tr')?.innerText ?? ''));
      const el = pool.find((x) => new RegExp(r, 'i').test(x.textContent));
      if (!el) return null;
      el.scrollIntoView({ block: 'center', inline: 'nearest' });
      const q = el.getBoundingClientRect();
      return { x: q.x + q.width / 2, y: q.y + q.height / 2 };
    }, sel, re, rowRe);
    if (!p) return false;
    await page.mouse.click(p.x, p.y);
    return true;
  };

  const fillModal = (ph, v) =>
    page.evaluate((p, val) => {
      const m = [...document.querySelectorAll('.ant-modal')].find((x) => x.offsetParent !== null);
      if (!m) return false;
      const i = p ? m.querySelector(`input[placeholder="${p}"]`) : m.querySelector('input:not([disabled])');
      if (!i) return false;
      Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set.call(i, val);
      i.dispatchEvent(new Event('input', { bubbles: true }));
      return true;
    }, ph, v);

  const openMenu = async (sel) => {
    await page.click(sel);
    await page.waitForFunction(
      () => [...document.querySelectorAll('.ant-dropdown')].some((d) => d.offsetParent !== null),
      { timeout: 4000 },
    );
  };
  const clickMenuItem = (txt) =>
    page.evaluate((t) => {
      const dds = [...document.querySelectorAll('.ant-dropdown')].filter((d) => d.offsetParent !== null);
      const it = dds
        .flatMap((d) => [...d.querySelectorAll('[role="menuitem"]')])
        .find((x) => x.textContent.includes(t));
      if (it) it.click();
      return !!it;
    }, txt);

  // Row action menus: click the row's ⋮ button (real mouse — then wait for
  // the dropdown; clicks fired during its opening animation get swallowed),
  // then pick an item with a real click too.
  const openRowMenu = async (rowRe) => {
    if (!(await mouseClick('[data-testid="row-actions"]', '', rowRe))) return false;
    try {
      await page.waitForFunction(
        () => [...document.querySelectorAll('.ant-dropdown')].some((d) => d.offsetParent !== null),
        { timeout: 4000 },
      );
    } catch {
      return false;
    }
    await new Promise((r) => setTimeout(r, 500)); // rc-motion settle
    return true;
  };
  const menuClick = async (label) => {
    const p = await page.evaluate((t) => {
      const it = [...document.querySelectorAll('.ant-dropdown [role="menuitem"]')]
        .filter((x) => x.offsetParent !== null)
        .find((x) => x.textContent.includes(t));
      if (!it) return null;
      it.scrollIntoView({ block: 'center' });
      const q = it.getBoundingClientRect();
      return { x: q.x + q.width / 2, y: q.y + q.height / 2 };
    }, label);
    if (!p) return false;
    await page.mouse.click(p.x, p.y);
    return true;
  };
  const visibleMenuText = () =>
    page.evaluate(() =>
      [...document.querySelectorAll('.ant-dropdown')]
        .filter((d) => d.offsetParent !== null)
        .map((d) => d.textContent)
        .join(' '),
    );
  const closeRowMenu = async () => {
    await page.keyboard.press('Escape');
    await new Promise((r) => setTimeout(r, 400));
  };

  const waitModal = () =>
    page.waitForFunction(
      () => [...document.querySelectorAll('.ant-modal')].some((x) => x.offsetParent !== null),
      { timeout: 4000 },
    );
  const modalText = () =>
    page.evaluate(() => {
      const m = [...document.querySelectorAll('.ant-modal')].find((x) => x.offsetParent !== null);
      return m ? m.innerText : '';
    });
  // close any open modal and WAIT for it to actually be gone: a lingering
  // mask silently swallows the next real-mouse clicks (maskClosable then
  // closes it on that click, cascading false failures downstream)
  const closeModals = async () => {
    await page.keyboard.press('Escape');
    await page
      .waitForFunction(
        () => ![...document.querySelectorAll('.ant-modal')].some((x) => x.offsetParent !== null),
        { timeout: 3000 },
      )
      .catch(() => {});
  };
  // the file page (not a modal): opens in VIEW mode; edit mode is entered
  // via the pane's Edit button or the row menu's "edit" action
  const openPane = () =>
    page
      .waitForFunction(() => document.querySelector('[data-testid="file-pane"]') ?? false, {
        timeout: 6000,
      })
      .catch(() => {});
  const closePane = async () => {
    await page.evaluate(() => document.querySelector('[data-testid="pane-back"]')?.click());
    await page
      .waitForFunction(() => !document.querySelector('[data-testid="file-pane"]'), { timeout: 3000 })
      .catch(() => {});
  };
  const toEditMode = async () => {
    await page.evaluate(() => document.querySelector('[data-testid="pane-edit"]')?.click());
    await page
      .waitForFunction(() => document.querySelector('[data-testid="editor-publish"], [data-testid="editor-save"]') ?? false, { timeout: 3000 })
      .catch(() => {});
  };
  const visiblePanelText = () =>
    page.evaluate(() => {
      const vis = [...document.querySelectorAll('main.ant-layout-content > div')].find(
        (d) => d.offsetParent !== null,
      );
      return vis ? vis.innerText : '';
    });

  try {
    await page.goto(B + '/', { waitUntil: 'networkidle2' });
    await page.waitForSelector('input[autocomplete="organization"]', { timeout: 8000 });
    check('login form rendered', true);

    await page.type('input[autocomplete="organization"]', 'system');
    await page.type('input[autocomplete="username"]', 'admin');
    await page.type('input[autocomplete="current-password"]', 'secret');
    await click('button', 'login');
    await page.waitForSelector('.app-user', { timeout: 8000 });
    check('login -> app shell', true);

    await page.waitForSelector('tbody tr td button', { timeout: 8000 });
    check('files rows rendered', true);
    check('content below header (no overlap)', await page.evaluate(() => {
      const h = document.querySelector('header.ant-layout-header')?.getBoundingClientRect();
      const c = document.querySelector('main.ant-layout-content')?.getBoundingClientRect();
      return !!h && !!c && c.top >= h.bottom - 1;
    }));
    check('header brand icon + text', await page.evaluate(() => {
      const b = document.querySelector('.app-header .app-brand');
      return !!b && !!b.querySelector('.anticon') && b.textContent.includes('File Explorer');
    }));
    // regression guard for the reported bug: on the dark header the default
    // Tabs colorText is near-black -> invisible; labels must compute light
    check('tab labels readable on dark header', await page.evaluate(() => {
      const t = [...document.querySelectorAll('[role="tab"]')].find(
        (x) => x.getAttribute('aria-selected') !== 'true',
      );
      if (!t) return false;
      const m = getComputedStyle(t.querySelector('.ant-tabs-tab-btn') || t).color.match(/[\d.]+/g);
      return !!m && +m[0] > 150 && +m[1] > 150 && +m[2] > 150;
    }));
    check('favicon served', await page.evaluate(async () => {
      const r = await fetch('/favicon.svg');
      return r.status === 200 && (await r.text()).includes('<svg');
    }));
    check('only active panel visible', await page.evaluate(() => {
      const c = document.querySelector('main.ant-layout-content');
      return !!c && [...c.querySelectorAll('.ant-table')].filter((t) => t.offsetParent !== null).length === 1;
    }));
    check('breadcrumb root', await page.evaluate(() => document.body.innerText.includes('root')));
    // every file row carries exactly one ⋮ trigger instead of a button row
    // (the pinned built-in help row is the one deliberate exception: no menu)
    check('file rows expose a single row-actions menu button; help row none', await page.evaluate(() => {
      const trs = [...document.querySelectorAll('tbody tr.ant-table-row')].filter(
        (x) => x.offsetParent !== null,
      );
      if (!trs.length) return false;
      for (const tr of trs) {
        const n = tr.querySelectorAll('[data-testid="row-actions"]').length;
        if (tr.innerText.includes('built-in')) {
          if (n !== 0) return false;
          continue;
        }
        if (n !== 1) return false;
        if (!tr.querySelector('[data-testid="row-actions"] .anticon')) return false;
        if ([...tr.querySelectorAll('button')].some((b) => /^(perms|rename|delete)$/.test(b.textContent.trim())))
          return false;
      }
      return true;
    }));
    // flash centre: no history yet -> no strip at all
    check('flash bar hidden with empty history', await page.evaluate(
      () => !document.querySelector('[data-testid="flash-bar"]'),
    ));

    // sort by size asc, then desc
    await click('th', 'size');
    await new Promise((r) => setTimeout(r, 900));
    const ascMark = await page.evaluate(
      () => [...document.querySelectorAll('th')].some((th) => /size/.test(th.textContent) && th.getAttribute('aria-sort') !== 'none'),
    );
    check('sort size header active', ascMark);

    // search 'deliverables' -> exactly one *visible* data row after
    // debounce (hidden panels are mounted too, so filter by visibility)
    await page.type('input[placeholder="search name"]', 'deliverables');
    await page
      .waitForFunction(
        () =>
          [...document.querySelectorAll('tbody tr.ant-table-row')].filter(
            (tr) => tr.offsetParent !== null,
          ).length === 1,
        { timeout: 6000 },
      )
      .then(() => check('search filters to 1 row', true))
      .catch(() => {
        check('search filters to 1 row', false);
        console.log('  last fs requests:\n    ' + fsReqs.slice(-3).join('\n    '));
      });

    // clear search, navigate into deliverables/
    await page.evaluate(() => {
      const el = document.querySelector('input[placeholder="search name"]');
      const set = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
      set.call(el, '');
      el.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await new Promise((r) => setTimeout(r, 1100));

    // ---- YAML structured editor: the flat path stack (file/document/
    // keys, top item = parent) navigates, table rows push deeper, the
    // leaf screen edits; edit -> draft autosave -> publish. The fixture
    // carries a leading comment, an inline comment, a block scalar and
    // two documents so the file assertions afterwards prove the AST
    // round-trip kept every one.
    const setLeaf = (sel, v) =>
      page.evaluate((s, val) => {
        const el = document.querySelector(s);
        if (!el) return false;
        el.focus(); // blur() on a never-focused input fires no event
        const proto = el.tagName === 'TEXTAREA' ? window.HTMLTextAreaElement : window.HTMLInputElement;
        Object.getOwnPropertyDescriptor(proto.prototype, 'value').set.call(el, val);
        el.dispatchEvent(new Event('input', { bubbles: true }));
        el.blur(); // YamlLeaf commits on blur
        return true;
      }, sel, v);
    const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
    await page.evaluate(() =>
      [...document.querySelectorAll('tbody button')].find((b) => b.textContent === 'walk-cfg.yml')?.click(),
    );
    await openPane();
    await page.waitForFunction(
      () =>
        document.querySelector('[data-testid="file-pane"]')?.querySelector('[data-testid="yaml-kv-d0"]') ?? false,
      { timeout: 5000 },
    ).catch(() => {});
    check('yaml name click opens the structured VIEWER (edit is a button/menu step)', await page.evaluate(() => {
      const m = document.querySelector('[data-testid="file-pane"]');
      return !!m && !!m.querySelector('[data-testid="yaml-editor"]') &&
        !!m.querySelector('[data-testid="yaml-nav"]') && !!m.querySelector('[data-testid="yaml-kv-d0"]') &&
        !m.querySelector('[data-testid="doc-editor"]') && !m.querySelector('.cm-editor') &&
        !!m.querySelector('[data-testid="pane-edit"]') && !m.querySelector('[data-testid="editor-publish"]');
    }));
    check('path stack is flat: file + document rows, keys table shows values', await page.evaluate(() => {
      const m = document.querySelector('[data-testid="file-pane"]');
      if (!m) return false;
      const file = m.querySelector('[data-testid="yaml-crumb-d"]');
      const doc = m.querySelector('[data-testid="yaml-crumb-d0"]');
      const val = m.querySelector('[data-testid="yaml-valview-d0.1"]');
      return !!file && /file/.test(file.textContent) && !!doc && /document 1/.test(doc.textContent) &&
        !!val && val.value === undefined && /2/.test(val.textContent) &&
        !!m.querySelector('[data-testid="yaml-cmtview-d0.0"]');
    }));
    // the file level of a multi-document file lists every document...
    await mouseClick('[data-testid="yaml-crumb-d"]');
    await sleep(600);
    check('file level switches between documents', await page.evaluate(() => {
      const m = document.querySelector('[data-testid="file-pane"]');
      const d2 = m?.querySelector('[data-testid="yaml-open-d1"]');
      return !!d2 && /document 2/.test(d2.textContent);
    }));
    await mouseClick('[data-testid="yaml-open-d1"]');
    await sleep(600);
    check('document 2 opens as its own tree', await page.evaluate(() => {
      const m = document.querySelector('[data-testid="file-pane"]');
      const k = m?.querySelector('[data-testid="yaml-key-d1.0"]');
      return !!m.querySelector('[data-testid="yaml-kv-d1"]') && !!k && k.value === 'agent';
    }));
    await mouseClick('[data-testid="yaml-crumb-d"]');
    await sleep(600);
    await mouseClick('[data-testid="yaml-open-d0"]');
    await sleep(600);
    // editing is the explicit step: the header Edit button enters it
    await toEditMode();
    // clicking a table row pushes the stack: the leaf screen carries
    // the note above the line and the inline comment
    await mouseClick('[data-testid="yaml-valview-d0.0"]');
    await sleep(600);
    check('row click pushes leaf screen editing note + value + comment', await page.evaluate(() => {
      const m = document.querySelector('[data-testid="file-pane"]');
      if (!m) return false;
      const note = m.querySelector('[data-testid="yaml-note-d0.0"]');
      const cmt = m.querySelector('[data-testid="yaml-cmt-d0.0"]');
      const val = m.querySelector('[data-testid="yaml-val-d0.0"]');
      return !!m.querySelector('[data-testid="yaml-scalar-d0.0"]') &&
        !!m.querySelector('[data-testid="yaml-crumb-d0.0"]') &&
        /(^|\.)(root|document 1)\.app/.test(m.querySelector('[data-testid="yaml-detail-path"]')?.textContent ?? '') &&
        !!note && note.value.includes('site process config') &&
        !!cmt && cmt.value.includes('do not touch') && val && val.value === 'web1';
    }));
    // the block-scalar value gets a real multiline editor
    await mouseClick('[data-testid="yaml-crumb-d0"]');
    await sleep(600);
    await mouseClick('[data-testid="yaml-valview-d0.3"]');
    await sleep(600);
    check('block scalar gets an in-panel multiline editor', await page.evaluate(() => {
      const m = document.querySelector('[data-testid="file-pane"]');
      const blk = m?.querySelector('[data-testid="yaml-val-d0.3"]');
      return !!blk && blk.tagName === 'TEXTAREA' && blk.value.includes('why this box exists');
    }));
    // edit one scalar on the leaf screen -> debounced autosave drafts it
    await mouseClick('[data-testid="yaml-crumb-d0"]');
    await sleep(600);
    await mouseClick('[data-testid="yaml-valview-d0.1"]');
    await sleep(600);
    const typed = await setLeaf('[data-testid="yaml-val-d0.1"]', '5');
    await sleep(2500);
    check('detail edit autosaves a draft server-side', typed && (await page.evaluate(() =>
      fetch('/api/v1/fs/draft?path=' + encodeURIComponent('/walk-cfg.yml'))
        .then((r) => r.json())
        .then((d) => !!d.draft && d.draft.content.includes('replicas: 5') &&
          d.draft.content.includes('# site process config')),
    )));
    check('draft banner shown in editor', await page.evaluate(() => {
      const m = document.querySelector('[data-testid="file-pane"]');
      return !!m && /Unpublished draft/.test(m.innerText);
    }));
    await mouseClick('[data-testid="editor-publish"]');
    await page
      .waitForFunction(() => {
        const m = document.querySelector('[data-testid="file-pane"]');
        return !m || !/Unpublished draft/.test(m.innerText);
      }, { timeout: 6000 })
      .catch(() => {});
    check(
      'publish rewrites the file, keeps comments/block/second doc, clears draft',
      await page.evaluate(async () => {
        const q = encodeURIComponent('/walk-cfg.yml');
        const c = await fetch('/api/v1/fs/content?path=' + q).then((r) => r.json());
        const d = await fetch('/api/v1/fs/draft?path=' + q).then((r) => r.json());
        const body = c.content || '';
        return body.includes('replicas: 5') && body.includes('# site process config') &&
          body.includes('# do not touch') && body.includes('why this box exists') &&
          body.includes('agent: a1') && !d.draft;
      }),
    );
    await closePane();

    // phone-width regression: at 390px the detail tables must NOT be
    // squeezed (values used to clip to a few characters) — they keep
    // real column widths and scroll horizontally, and the path stack
    // moves into the ☰ drawer.
    await page.setViewport({ width: 390, height: 844 });
    await new Promise((r) => setTimeout(r, 600));
    await page.evaluate(() =>
      [...document.querySelectorAll('tbody button')].find((b) => b.textContent === 'walk-cfg.yml')?.click(),
    );
    await openPane();
    await page.waitForFunction(
      () =>
        document.querySelector('[data-testid="file-pane"]')?.querySelector('[data-testid="yaml-kv-d0"]') ?? false,
      { timeout: 5000 },
    ).catch(() => {});
    await toEditMode();
    // antd's breakpoint hook flips "narrow" a beat after the emulated
    // viewport change — wait for the mobile toolbar, not a sleep.
    await page.waitForFunction(
      () =>
        document.querySelector('[data-testid="file-pane"]')?.querySelector('[data-testid="yaml-nav-open"]') ??
        false,
      { timeout: 5000 },
    ).catch(() => {});
    // let the resize settle: the table re-lays itself once antd flips to
    // the narrow layout (briefly measurably squeezed), so measure the key
    // column only after it stabilizes — generous budget, returns early
    await page.waitForFunction(
      () => {
        const m = document.querySelector('[data-testid="file-pane"]');
        const key = m?.querySelector('[data-testid="yaml-key-d0.0"]');
        return !!key && key.getBoundingClientRect().width >= 140;
      },
      { timeout: 10000 },
    ).catch(() => {});
    const mobileOk = await page.evaluate(() => {
      const m = document.querySelector('[data-testid="file-pane"]');
      if (!m) return { ok: false, why: 'no pane', panes: document.querySelectorAll('[data-testid="file-pane"]').length };
      const key = m.querySelector('[data-testid="yaml-key-d0.0"]');
      const kw = key ? key.getBoundingClientRect().width : 0;
      const box =
        m.querySelector('[data-testid="yaml-kv-d0"] .ant-table-content') ||
        m.querySelector('[data-testid="yaml-kv-d0"] .ant-table-body');
      const scrolls = !!box && box.scrollWidth > box.clientWidth + 40;
      return {
        ok: !!m.querySelector('[data-testid="yaml-nav-open"]') && kw >= 140 && scrolls,
        title: m.querySelector('[data-testid="file-pane"] .mono')?.innerText?.slice(0, 60) ?? '(none)',
        hasKv: !!m.querySelector('[data-testid="yaml-kv-d0"]'),
        hasEditor: !!m.querySelector('[data-testid="yaml-editor"]'),
        navBtn: !!m.querySelector('[data-testid="yaml-nav-open"]'),
        kw, scrolls,
      };
    });
    if (!mobileOk.ok) console.log('  dbg mobile:', JSON.stringify(mobileOk));
    check('phone width: path stack in drawer, tables keep width + scroll sideways', mobileOk.ok);
    await closePane();
    await page.setViewport({ width: 800, height: 600 });
    await new Promise((r) => setTimeout(r, 600));

    await page.evaluate(() =>
      [...document.querySelectorAll('tbody button')].find((b) => b.textContent === 'deliverables/')?.click(),
    );
    await new Promise((r) => setTimeout(r, 1000));
    check(
      'navigated into deliverables/',
      await page.evaluate(() => {
        const crumbs = document.querySelector('nav[aria-label="Breadcrumb"], .ant-breadcrumb');
        return !!crumbs && /deliverables/.test(crumbs.textContent) &&
          [...document.querySelectorAll('tbody button, tbody a')].some((b) => b.textContent === 'report-q3.pdf');
      }),
    );

    // file row menu: permissions/download/rename/delete live behind the ⋮;
    // "edit" is NOT offered for download-only files (filekind policy)
    check(
      'file row menu lists permissions/download/rename/delete, no edit for downloads',
      (await openRowMenu('report-q3')) &&
        (await visibleMenuText()).includes('permissions') &&
        (await visibleMenuText()).includes('download') &&
        (await visibleMenuText()).includes('rename') &&
        (await visibleMenuText()).includes('delete') &&
        !(await visibleMenuText()).includes('edit'),
    );
    await closeRowMenu();

    // binaries / non-allowlisted types never get an editor: the name
    // itself is a download link straight to /fs/raw. (The pdf fixture
    // holds plain-text bytes on purpose: the policy is name-based, like
    // GitHub's, not content-sniffed per row.)
    check('disallowed file name is a download link to /fs/raw', await page.evaluate(() => {
      const a = [...document.querySelectorAll('tbody a')].find((x) => x.textContent === 'report-q3.pdf');
      return !!a && (a.getAttribute('href') || '') === '/api/v1/fs/raw?path=%2Fdeliverables%2Freport-q3.pdf';
    }));

    // plain documents: the name click opens the VIEWER — markdown renders
    // like GitHub (Render/Code toggle, source read-only); editing is the
    // row menu's "edit", which gets a CodeMirror + markdown ribbon; save
    // is base_mtime-guarded and the bytes land on disk.
    await page.evaluate(() => document.querySelector('.ant-breadcrumb a')?.click());
    await new Promise((r) => setTimeout(r, 1000));
    await page.evaluate(() =>
      [...document.querySelectorAll('tbody button')].find((b) => b.textContent === 'readme.md')?.click(),
    );
    await openPane();
    await page.waitForFunction(
      () => document.querySelector('[data-testid="md-view"]') ?? false,
      { timeout: 8000 },
    ).catch(() => {});
    check('markdown name click opens the rendered viewer (no editor)', await page.evaluate(() => {
      const p = document.querySelector('[data-testid="file-pane"]');
      const v = p?.querySelector('[data-testid="md-view"]');
      return !!v && /readme/.test(v.innerText) && !!v.querySelector('h1') && !p.querySelector('.cm-editor') &&
        !!p.querySelector('[data-testid="pane-edit"]') && !!p.querySelector('[data-testid="pane-viewmode"]');
    }));
    await page.evaluate(() => {
      const seg = document.querySelector('[data-testid="pane-viewmode"]');
      const opt = [...seg.querySelectorAll('.ant-segmented-item')].find((x) => /Code/.test(x.textContent));
      opt?.click();
    });
    await page.waitForFunction(
      () => document.querySelector('[data-testid="file-pane"] .cm-content') ?? false,
      { timeout: 5000 },
    ).catch(() => {});
    check('viewer Code toggle shows the source (read-only)', await page.evaluate(() => {
      const p = document.querySelector('[data-testid="file-pane"]');
      const cm = p?.querySelector('[data-testid="doc-editor"] .cm-editor');
      return !!cm && /# readme/.test(p.querySelector('.cm-content')?.innerText ?? '') &&
        !p.querySelector('[data-testid="doc-toolbar"]') && !p.querySelector('[data-testid="editor-save"]');
    }));
    await closePane();

    check('text file row menu offers edit', (await openRowMenu('readme')) && (await visibleMenuText()).includes('edit'));
    await menuClick('edit');
    await openPane();
    check('menu edit opens CodeMirror with a markdown ribbon', await page
      .waitForFunction(
        () =>
          document.querySelector('[data-testid="file-pane"]')?.querySelector('[data-testid="doc-toolbar"]') &&
          document.querySelector('[data-testid="file-pane"] .cm-content') !== null,
        { timeout: 8000 },
      )
      .then(() => true)
      .catch(() => false));
    // ribbon sanity: real click on the heading line, click H2 -> the
    // line prefix lands in the doc (the ribbon edits the markdown source)
    await mouseClick('.cm-line', '# readme');
    await page.evaluate(() => document.querySelector('[data-testid="doc-tool-h2"]')?.click());
    await new Promise((r) => setTimeout(r, 300));
    check('ribbon button edits the markdown source at the caret', await page.evaluate(() =>
      /## # readme/.test(document.querySelector('[data-testid="file-pane"] .cm-content')?.innerText ?? ''),
    ));
    // caret to end of doc without modifier chords: ArrowDown clamps at the
    // last line; a click in the blank area below the text usually does too.
    for (let i = 0; i < 5; i++) await page.keyboard.press('ArrowDown');
    await page.keyboard.type('\nedited via doc editor\n');
    await page.evaluate(() => document.querySelector('[data-testid="editor-save"]')?.click());
    await new Promise((r) => setTimeout(r, 800));
    check('doc editor save writes to disk (ribbon edit + typed edit both land)', await page.evaluate(() =>
      fetch('/api/v1/fs/content?path=' + encodeURIComponent('/readme.md'))
        .then((r) => r.json())
        .then((c) => (c.content ?? '').includes('## # readme') && /edited via doc editor/.test(c.content ?? '')),
    ));
    await closePane();

    // perms tab (super admin sees the bypass notice instead of bits)
    await click('[role="tab"]', 'permissions');
    await page
      .waitForFunction(
        () => /super admin|effective for you/.test(document.body.innerText),
        { timeout: 8000 },
      )
      .then(() => check('perms tab effective line', true))
      .catch(() => check('perms tab effective line', false));

    // users tab
    await click('[role="tab"]', 'users');
    await page
      .waitForFunction(() => [...document.querySelectorAll('tbody tr td')].some((td) => td.innerText.includes('admin')), { timeout: 8000 })
      .then(() => check('users tab rows', true))
      .catch(() => check('users tab rows', false));

    // the full user action set lives in the row menu, not in a button row
    const userMenuOpen = await openRowMenu('alice');
    const userMenuTxt = userMenuOpen ? await visibleMenuText() : '';
    check(
      'user row menu lists edit/API key/password/suspend/delete',
      userMenuOpen &&
        ['edit', 'new API key', 'set password', 'suspend', 'delete'].every((a) =>
          userMenuTxt.includes(a)),
    );
    await closeRowMenu();

    // active + description columns exist on the entity list
    check('users active+description columns', await page.evaluate(() => {
      const hs = [...document.querySelectorAll('th')].filter((th) => th.offsetParent !== null).map((th) => th.innerText);
      return hs.some((h) => /active/.test(h)) && hs.some((h) => /description/.test(h));
    }));

    // create user goes through the modal form now
    check('users panel has Create user button', await page.evaluate(
      () => [...document.querySelectorAll('button')].some((b) => b.offsetParent !== null && /^Create user$/.test(b.textContent.trim())),
    ));
    await mouseClick('button', '^Create user$');
    await waitModal();
    check('create user modal has form fields', /Create user/.test(await modalText()) &&
      (await page.evaluate(() => {
        const m = [...document.querySelectorAll('.ant-modal')].find((x) => x.offsetParent !== null);
        return !!m && !!m.querySelector('input[type="password"]');
      })));
    await closeModals();

    // active filter select -> active=false on the wire, restored afterwards
    const selPos = await page.evaluate(() => {
      const vis = [...document.querySelectorAll('main.ant-layout-content > div')].find((d) => d.offsetParent !== null);
      const s = vis?.querySelector('.bar .ant-select'); // first select in the toolbar = active filter
      if (!s) return null;
      const q = s.getBoundingClientRect();
      return { x: q.x + q.width / 2, y: q.y + q.height / 2 };
    });
    // open the select, settle (rc-select swallows option clicks fired
    // during the dropdown's opening animation), click, then verify the
    // select actually changed and retry up to 3 times
    const pickOption = async (x, y, label) => {
      const re = '^' + label + '$';
      for (let attempt = 0; attempt < 3; attempt++) {
        const open = await page.evaluate(() =>
          [...document.querySelectorAll('.ant-select-dropdown')].some((d) => d.offsetParent !== null),
        );
        if (!open) await page.mouse.click(x, y);
        await new Promise((r) => setTimeout(r, 700));
        await mouseClick('.ant-select-item-option', re);
        // verify by the trigger's own rendered text — antd v6 does not use
        // .ant-select-selection-item; the dropdown is a body-level portal,
        // so the select element's text only ever contains the chosen label
        const applied = await page
          .waitForFunction(
            (x, y, l) => {
              const sel = document.elementFromPoint(x, y)?.closest('.ant-select');
              return !!sel && sel.innerText.includes(l);
            },
            { timeout: 1500 },
            x,
            y,
            label,
          )
          .then(() => true)
          .catch(() => false);
        if (applied) return true;
      }
      return false;
    };
    check('active filter select present', selPos !== null);
    if (selPos) {
      // capture the option labels while the dropdown is open (rc-select
      // unmounts its popup once closed)
      await page.mouse.click(selPos.x, selPos.y);
      await new Promise((r) => setTimeout(r, 700));
      check('active filter offers all/active/suspended', await page.evaluate(() => {
        const d = [...document.querySelectorAll('.ant-select-dropdown')].find(
          (x) => x.offsetParent !== null,
        );
        if (!d) return false;
        const labels = [...d.querySelectorAll('.ant-select-item-option')].map(
          (o) => o.textContent.trim(),
        );
        return labels.includes('all') && labels.includes('active') && labels.includes('suspended');
      }));
      await page.keyboard.press('Escape'); // close, then drive real selections
      await new Promise((r) => setTimeout(r, 400));
      const opened = await pickOption(selPos.x, selPos.y, 'suspended');
      check('active filter selects suspended', opened);
      await new Promise((r) => setTimeout(r, 1200));
      check('active=suspended sends active=false', /active=false/.test(userReqs.at(-1) ?? ''));
      // restore
      await pickOption(selPos.x, selPos.y, 'all');
      await new Promise((r) => setTimeout(r, 1200));
      check('filter reset drops the active param', !/active=/.test(userReqs.at(-1) ?? ''));
    }

    // tenants tab
    await click('[role="tab"]', 'tenants');
    await page
      .waitForFunction(() => [...document.querySelectorAll('tbody tr td')].some((td) => /\bsystem\b/.test(td.innerText)), { timeout: 8000 })
      .then(() => check('tenants tab lists system', true))
      .catch(() => check('tenants tab lists system', false));

    // the caller's own tenant cannot be suspended: its ⋮ menu item is disabled
    let suspendDisabled = false;
    if (await openRowMenu('your tenant')) {
      suspendDisabled = await page.evaluate(() => {
        const it = [...document.querySelectorAll('.ant-dropdown [role="menuitem"]')]
          .filter((x) => x.offsetParent !== null)
          .find((x) => x.textContent.trim() === 'suspend');
        return !!it && it.classList.contains('ant-dropdown-menu-item-disabled');
      });
      await closeRowMenu();
    }
    check('own tenant suspend disabled in row menu', suspendDisabled);

    // create tenant modal -> duplicate name -> flash bar shows the API error
    await mouseClick('button', '^Create tenant$');
    await waitModal();
    await fillModal('', 'system');
    await mouseClick('.ant-modal button', '^Create tenant$');
    await new Promise((r) => setTimeout(r, 1200));
    check('flash bar shows API error under header', await page.evaluate(() => {
      const b = document.querySelector('[data-testid="flash-bar"]');
      if (!b) return false;
      const underHeader = b.getBoundingClientRect().top >= document.querySelector('header.ant-layout-header').getBoundingClientRect().bottom - 1;
      return underHeader && /exists: tenant name taken/.test(b.innerText);
    }));
    check('flash log button with badge', await page.evaluate(() => {
      const b = document.querySelector('[data-testid="flash-bar"]');
      return !!b && !!b.querySelector('.ant-badge') && /event log/i.test(b.innerText);
    }));
    // close the still-open create modal via its Cancel button and wait
    // until it is gone (a lingering mask swallows the flash-bar click)
    await mouseClick('.ant-modal button', '^Cancel$');
    await page
      .waitForFunction(
        () => ![...document.querySelectorAll('.ant-modal')].some((x) => x.offsetParent !== null),
        { timeout: 4000 },
      )
      .catch(() => {});

    // clicking a flash entry opens the event log drawer: the full audit
    // table (same component as the Events tab) + session messages below
    const flashPos = await page.evaluate(() => {
      const el = document.querySelector('[data-testid="flash-bar"] .flash-item');
      if (!el) return null;
      const q = el.getBoundingClientRect();
      return { x: q.x + 20, y: q.y + q.height / 2 };
    });
    check('flash entry clickable', flashPos !== null);
    if (flashPos) await page.mouse.click(flashPos.x, flashPos.y);
    // NB: the .ant-drawer root is position:fixed so offsetParent is
    // always null — detect "open" via the ant-drawer-open class.
    await page
      .waitForFunction(
        () => {
          const d = document.querySelector('.ant-drawer-open');
          return (
            !!d &&
            [...d.querySelectorAll('th')].some((h) => /actor/i.test(h.textContent)) &&
            !!d.querySelector('tr.ant-table-row')
          );
        },
        { timeout: 8000 },
      )
      .catch(() => {});
    check('clicking flash opens event log drawer with the audit table', await page.evaluate(() => {
      const d = document.querySelector('.ant-drawer-open');
      if (!d) return false;
      const heads = [...d.querySelectorAll('th')].map((h) => h.textContent.toLowerCase());
      return ['time', 'actor', 'action', 'target', 'path'].every((c) => heads.some((h) => h.includes(c))) &&
        !!d.querySelector('[data-testid="events-table"]') &&
        !!d.querySelector('tr.ant-table-row');
    }));
    check('event log drawer keeps session messages outside the audit', await page.evaluate(() => {
      const d = document.querySelector('.ant-drawer-open');
      return !!d && /not in the audit trail/.test(d.innerText) && /tenant name taken/.test(d.innerText);
    }));
    // clear() empties the session history: the bar disappears (the
    // drawer and its audit table stay open)
    await mouseClick('.ant-drawer-open button', '^clear$');
    await new Promise((r) => setTimeout(r, 700));
    check('clear removes flash history and bar', await page.evaluate(
      () => !document.querySelector('[data-testid="flash-bar"]'),
    ));
    await page.click('.ant-drawer-open .ant-drawer-close');
    await page
      .waitForFunction(() => !document.querySelector('.ant-drawer-open'), { timeout: 4000 })
      .catch(() => {});
    await new Promise((r) => setTimeout(r, 500)); // let the mask unmount

    // edit tenant goes through the row menu -> prefilled, name-locked modal
    check('tenant edit via row menu', await openRowMenu('your tenant'));
    await menuClick('edit');
    await waitModal();
    check('tenant edit modal prefilled + name locked', /Edit tenant system/.test(await modalText()) &&
      (await page.evaluate(() => {
        const m = [...document.querySelectorAll('.ant-modal')].find((x) => x.offsetParent !== null);
        const i = m?.querySelector('input[disabled]');
        return i?.value === 'system';
      })));
    await closeModals();

    check(
      'pager totals shown',
      await page.evaluate(() => /rows \d+\D+\d+ of \d+/.test(document.body.innerText)),
    );
    // scrollIntoView above may have scrolled the header (and its tabs) out
    // of the viewport — rect-based header clicks land on empty space then
    await page.evaluate(() => window.scrollTo(0, 0));
    await new Promise((r) => setTimeout(r, 300));
    await page.screenshot({ path: path.join(OUT, 'tenants.png') });

    // back to files panel for the screenshot
    const tabPos = await page.evaluate(() => {
      const t = [...document.querySelectorAll('[role="tab"]')].find((x) => /files/i.test(x.textContent));
      const q = t.getBoundingClientRect();
      return { x: q.x + q.width / 2, y: q.y + q.height / 2 };
    });
    await page.mouse.click(tabPos.x, tabPos.y);
    await new Promise((r) => setTimeout(r, 1000));
    await page.screenshot({ path: path.join(OUT, 'files.png') });

    // ---- per-file permissions flow (row menu "permissions" -> GrantForm modal) ----
    await page.evaluate(() =>
      fetch('/api/v1/fs/write', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: '/walk-perms.md', content: 'grant target test' }),
      }),
    );
    await mouseClick('.ant-breadcrumb a', 'root'); // search is scoped to cwd
    await new Promise((r) => setTimeout(r, 800));
    await page.evaluate(() => {
      const i = document.querySelector('input[placeholder="search name"]');
      Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set.call(i, 'walk-perms');
      i.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await new Promise((r) => setTimeout(r, 900));
    check('row menu -> permissions', await openRowMenu('walk-perms'));
    await menuClick('permissions');
    await new Promise((r) => setTimeout(r, 900));
    check(
      'row perms action targets permissions panel at that file',
      (await visiblePanelText()).includes('walk-perms.md') &&
        (await visiblePanelText()).includes('permissions on'),
    );
    // grant r to alice@acme on the exact file via the modal. The tenant
    // field is a real picker now (super admin: remote-search of tenants)
    // and the principal box suggests names only inside the selected tenant.
    check('Grant access button opens grant modal', await (async () => {
      await mouseClick('button', '^Grant access$');
      try {
        await waitModal();
      } catch {
        return false;
      }
      return /Grant access/.test(await modalText()) && /inherit down/.test(await modalText());
    })());
    const tenantSelPos = await page.evaluate(() => {
      const m = [...document.querySelectorAll('.ant-modal')].find((x) => x.offsetParent !== null);
      const s = m?.querySelector('[data-testid="grant-tenant"]');
      if (!s) return null;
      const q = s.getBoundingClientRect();
      return { x: q.x + q.width / 2, y: q.y + q.height / 2 };
    });
    check('grant modal tenant field is a tenant select (super admin)', tenantSelPos !== null);
    check('tenant picker switches to acme', tenantSelPos !== null && (await pickOption(tenantSelPos.x, tenantSelPos.y, 'acme')));
    await page.evaluate(() => {
      const m = [...document.querySelectorAll('.ant-modal')].find((x) => x.offsetParent !== null);
      m?.querySelector('[data-testid="grant-principal"] input')?.focus();
    });
    await page.keyboard.type('alice');
    await new Promise((r) => setTimeout(r, 900)); // debounce + round trip
    check('principal suggestions are scoped to the selected tenant', await page.evaluate(() => {
      const d = [...document.querySelectorAll('.ant-select-dropdown')].find((x) => x.offsetParent !== null);
      if (!d) return false;
      const opts = [...d.querySelectorAll('.ant-select-item-option')].map((o) => o.textContent);
      return opts.some((o) => /alice/.test(o)) && !opts.some((o) => /carol/.test(o));
    }));
    await mouseClick('.ant-modal button', '^Save grant$');
    await new Promise((r) => setTimeout(r, 1400));
    check('grant on the file appears in grants table', (await visiblePanelText()).includes('acme/alice'));
    check('flash bar confirms grant saved', await page.evaluate(() => {
      const b = document.querySelector('[data-testid="flash-bar"]');
      return !!b && /grant saved: acme\/alice/.test(b.innerText);
    }));
    // …and revoke it again so the demo data stays tidy (row menu ->
    // themed confirm dialog whose OK button carries the action name)
    check('grant row menu -> revoke', await openRowMenu('acme/alice'));
    await menuClick('revoke');
    await waitModal();
    await mouseClick('.ant-modal button', '^revoke$');
    await new Promise((r) => setTimeout(r, 900));
    check('grant revoked', !(await visiblePanelText()).includes('acme/alice'));
    await page.evaluate(() => fetch('/api/v1/fs?path=%2Fwalk-perms.md', { method: 'DELETE' }));
    await page.evaluate(() => {
      const i = document.querySelector('input[placeholder="search name"]');
      Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set.call(i, '');
      i.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await new Promise((r) => setTimeout(r, 400));

    // ---- events tab: the audit trail as a real table — every column,
    // server-side paging/search/sort like the other lists, plus the
    // audit-only action= filter and per-row details expansion ----
    await click('[role="tab"]', 'events');
    await page
      .waitForFunction(
        () =>
          [...document.querySelectorAll('tbody tr')].some(
            (tr) => tr.offsetParent !== null && /perm_set|user_create|login/.test(tr.innerText),
          ),
        { timeout: 8000 },
      )
      .catch(() => {});
    check('events tab table shows every audit column', await page.evaluate(() => {
      const hs = [...document.querySelectorAll('th')]
        .filter((th) => th.offsetParent !== null)
        .map((th) => th.innerText.trim());
      return ['id', 'time', 'actor', 'tenant', 'action', 'target', 'path', 'ip'].every((c) =>
        hs.includes(c),
      );
    }));
    const eventsRows = () =>
      page.evaluate(() =>
        [...document.querySelectorAll('tbody tr.ant-table-row')]
          .filter((tr) => tr.offsetParent !== null)
          .map((tr) => (tr.innerText || '').replace(/\s+/g, ' ')),
      );
    const rowsNow = await eventsRows();
    check('events tab lists recorded audit actions', rowsNow.some((t) => /perm_set/.test(t)));
    if (!rowsNow.some((t) => /perm_set/.test(t)))
      console.log('EVENTS-DIAG rows=' + JSON.stringify(rowsNow.slice(0, 8)) +
        ' reqs=' + JSON.stringify(eventReqs.slice(-3)) +
        ' flash=' + JSON.stringify(await page.evaluate(() => document.querySelector('[data-testid="flash-bar"]')?.innerText ?? '')));
    // action filter: first visible toolbar select (other panels are hidden)
    const actPos = await page.evaluate(() => {
      const s = [...document.querySelectorAll('.bar .ant-select')].find(
        (x) => x.offsetParent !== null,
      );
      if (!s) return null;
      const q = s.getBoundingClientRect();
      return { x: q.x + q.width / 2, y: q.y + q.height / 2 };
    });
    // the action list is long, so the select is searchable: open it,
    // type to filter (defeats option virtualization), then click the hit
    const pickTyped = async (x, y, typeText, label) => {
      await page.mouse.click(x, y);
      await new Promise((r) => setTimeout(r, 600));
      await page.keyboard.type(typeText);
      await new Promise((r) => setTimeout(r, 500));
      await mouseClick('.ant-select-item-option', '^' + label + '$');
      await new Promise((r) => setTimeout(r, 400));
    };
    if (actPos) await pickTyped(actPos.x, actPos.y, 'perm_set', 'perm_set');
    await new Promise((r) => setTimeout(r, 1300));
    check('events action filter sends action=perm_set', /action=perm_set/.test(eventReqs.at(-1) ?? ''));
    const filteredRows = await eventsRows();
    check('action-filtered rows all show perm_set', filteredRows.length > 0 && filteredRows.every((t) => /perm_set/.test(t)));
    if (!filteredRows.length || !filteredRows.every((t) => /perm_set/.test(t)))
      console.log('EVENTS-DIAG filtered=' + JSON.stringify(filteredRows.slice(0, 8)));
    if (actPos) await pickTyped(actPos.x, actPos.y, 'all act', 'all actions');
    await new Promise((r) => setTimeout(r, 1000));
    check('events search sends q= scoped to audit cols', await (async () => {
      await page.type('input[placeholder="search events"]', 'walk-perms');
      await new Promise((r) => setTimeout(r, 1500));
      return /q=walk-perms/.test(eventReqs.at(-1) ?? '');
    })());
    check(
      'details expander shows the structured payload',
      await page.evaluate(() => {
        const b = [...document.querySelectorAll('.ant-table-row-expand-icon')].find(
          (x) => x.offsetParent !== null && !x.classList.contains('ant-table-row-expand-icon-disabled'),
        );
        if (!b) return false;
        b.click();
        return true;
      }),
    );
    await new Promise((r) => setTimeout(r, 500));
    check('expanded event details render as JSON', await page.evaluate(() => {
      const p = [...document.querySelectorAll('[data-testid="event-details"]')].find(
        (x) => x.offsetParent !== null,
      );
      return !!p && p.innerText.trim().startsWith('{') && p.innerText.includes('":');
    }));

    // ---- header menus ----
    // user menu (avatar top-right): identity header + profile/settings/sign out
    await openMenu('.app-user');
    check('user menu: identity + profile/settings/sign out', await page.evaluate(() => {
      const t = [...document.querySelectorAll('.ant-dropdown')]
        .filter((d) => d.offsetParent !== null)
        .map((d) => d.textContent)
        .join(' ');
      return /system\/admin/.test(t) && /Profile/.test(t) && /Settings/.test(t) && /Sign out/.test(t);
    }));
    await clickMenuItem('Profile');
    await waitModal();
    check('profile modal shows identity', /Username[\s\S]*admin/.test(await modalText()) && /Super administrator/.test(await modalText()));
    await closeModals();

    // brand menu (top-left): help/about/feedback
    await openMenu('.app-brand');
    check('brand menu: help/about/feedback', await page.evaluate(() => {
      const t = [...document.querySelectorAll('.ant-dropdown')]
        .filter((d) => d.offsetParent !== null)
        .map((d) => d.textContent)
        .join(' ');
      return /Help/.test(t) && /About/.test(t) && /Feedback/.test(t);
    }));
    await clickMenuItem('About');
    await waitModal();
    check('about modal names the stack', /Ant Design/.test(await modalText()));
    await closeModals();

    // ---- built-in user guide: embedded in the binary, /help tree ----
    // brand-menu Help opens the guide hub rendered in the file page;
    // links between guides navigate the pane in-app; the pane is
    // read-only for everyone (no Edit). The same tree is pinned in the
    // Files panel as a read-only help/ folder at root.
    await openMenu('.app-brand');
    await clickMenuItem('Help');
    await openPane();
    await page.waitForFunction(
      () => document.querySelector('[data-testid="file-pane"] [data-testid="md-view"]') ?? false,
      { timeout: 8000 },
    ).catch(() => {});
    check('help menu opens the built-in guide hub (rendered, no Edit)', await page.evaluate(() => {
      const p = document.querySelector('[data-testid="file-pane"]');
      const v = p?.querySelector('[data-testid="md-view"]');
      return !!v && /user guide/i.test(v.innerText) &&
        (p.querySelector('.mono')?.innerText ?? '').includes('/help/README.md') &&
        !p.querySelector('[data-testid="pane-edit"]') && !!p.querySelector('[data-testid="pane-dl"]');
    }));
    await page.evaluate(() => {
      const a = [...document.querySelectorAll('[data-testid="md-doclink"]')].find((x) =>
        /\/help\/permissions\.md/.test(x.getAttribute('href') || ''));
      a?.click();
    });
    await page.waitForFunction(
      () => (document.querySelector('[data-testid="file-pane"] .mono')?.innerText ?? '').includes('/help/permissions.md'),
      { timeout: 8000 },
    ).catch(() => {});
    check('guide cross-links navigate the pane in-app', await page.evaluate(() => {
      const v = document.querySelector('[data-testid="file-pane"] [data-testid="md-view"]');
      return !!v && /Expiring shares/i.test(v.innerText);
    }));
    await closePane();

    await click('[role="tab"]', 'files');
    await page.evaluate(() => {
      const el = document.querySelector('input[placeholder="search name"]');
      if (!el) return;
      const set = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
      set.call(el, '');
      el.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await page.evaluate(() => document.querySelector('.ant-breadcrumb a')?.click());
    await new Promise((r) => setTimeout(r, 1000));
    check('file explorer pins the read-only help folder at root', await page.evaluate(() => {
      const tr = [...document.querySelectorAll('tbody tr.ant-table-row')].filter((x) => x.offsetParent !== null);
      const help = tr.find((x) => /help\//.test(x.innerText));
      return !!help && /built-in/i.test(help.innerText) &&
        !help.querySelector('[data-testid="row-actions"]');
    }));
    await page.evaluate(() =>
      [...document.querySelectorAll('tbody button')].find((b) => b.textContent === 'help/')?.click(),
    );
    await page.waitForFunction(
      () => [...document.querySelectorAll('tbody tr.ant-table-row')].some(
        (x) => x.offsetParent !== null && /api-keys\.md/.test(x.innerText),
      ),
      { timeout: 8000 },
    ).catch(() => {});
    check('help/ browses the embedded guide listing', await page.evaluate(() => {
      const rows = [...document.querySelectorAll('tbody tr.ant-table-row')].filter((x) => x.offsetParent !== null);
      return rows.some((x) => /README\.md/.test(x.innerText)) &&
        rows.some((x) => /api-keys\.md/.test(x.innerText));
    }));
    check('inside /help: folder/file/upload actions are gone', await page.evaluate(() => {
      const labels = [...document.querySelectorAll('button')]
        .filter((b) => b.offsetParent !== null)
        .map((b) => b.textContent.trim());
      return !labels.includes('Folder') && !labels.includes('File') && !labels.includes('Upload');
    }));
    await page.evaluate(() =>
      [...document.querySelectorAll('tbody button')].find((b) => b.textContent === 'api.md')?.click(),
    );
    await openPane();
    check('embedded docs open rendered and read-only from the listing', await page.evaluate(() => {
      const p = document.querySelector('[data-testid="file-pane"]');
      const v = p?.querySelector('[data-testid="md-view"]');
      return !!v && /REST API for non-browser clients/i.test(v.innerText) &&
        !p.querySelector('[data-testid="pane-edit"]');
    }));
    await closePane();
    await page.evaluate(() => document.querySelector('.ant-breadcrumb a')?.click());
    await new Promise((r) => setTimeout(r, 600));

    // settings modal: dark mode toggle applies and persists
    await openMenu('.app-user');
    await clickMenuItem('Settings');
    await page.waitForSelector('.ant-modal .ant-switch', { timeout: 4000 });
    await page.evaluate(() =>
      [...document.querySelectorAll('.ant-modal .ant-switch')].find((s) => s.offsetParent !== null)?.click(),
    );
    await new Promise((r) => setTimeout(r, 600));
    check('dark mode applies + persists', await page.evaluate(
      () =>
        localStorage.getItem('fx.theme') === 'dark' &&
        getComputedStyle(document.querySelector('.ant-layout')).backgroundColor !== 'rgb(245, 245, 245)',
    ));
    await page.evaluate(() =>
      [...document.querySelectorAll('.ant-modal .ant-switch')].find((s) => s.offsetParent !== null)?.click(),
    );
    await new Promise((r) => setTimeout(r, 300));
    await closeModals();

    // sign out from the user menu returns to the login screen
    await openMenu('.app-user');
    await clickMenuItem('Sign out');
    await page.waitForSelector('input[autocomplete="organization"]', { timeout: 8000 });
    check('sign out returns to login', true);
  } catch (e) {
    console.error('FATAL during walk:', e.message);
    fail.push('walk completed: ' + e.message);
    await page.screenshot({ path: path.join(OUT, 'failure.png') }).catch(() => {});
  }

  check('no console errors', errors.length === 0);
  if (errors.length) console.log(errors.slice(0, 10).join('\n'));
  console.log(`\npassed=${ok.length} failed=${fail.length}`);
  await browser.close();
  process.exit(fail.length ? 1 : 0);
})();
