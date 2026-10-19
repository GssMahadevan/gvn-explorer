// GUI smoke-walk: login -> files table -> sort -> search -> YAML
// path-stack editor (drill-down + draft autosave + publish) -> navigate ->
// perms -> users -> tenants, asserting along the way + screenshots.
// Covers the modal-based entity forms (create/edit), the per-row action
// menu (the vertical three-dots on every table row), the active-filter
// select, and the flash centre strip under the header.
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
  page.on('request', (r) => {
    if (r.url().includes('/api/v1/fs?')) fsReqs.push(r.url());
    if (r.url().includes('/api/v1/users?')) userReqs.push(r.url());
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
    check('file rows expose a single row-actions menu button', await page.evaluate(() => {
      const tr = [...document.querySelectorAll('tbody tr.ant-table-row')].find(
        (x) => x.offsetParent !== null,
      );
      if (!tr) return false;
      return (
        tr.querySelectorAll('[data-testid="row-actions"]').length === 1 &&
        !!tr.querySelector('[data-testid="row-actions"] .anticon') &&
        ![...tr.querySelectorAll('button')].some((b) => /^(perms|rename|delete)$/.test(b.textContent.trim()))
      );
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
    await waitModal();
    await page.waitForFunction(
      () =>
        [...document.querySelectorAll('.ant-modal')]
          .find((x) => x.offsetParent !== null)
          ?.querySelector('[data-testid="yaml-kv-d0"]') ?? false,
      { timeout: 5000 },
    ).catch(() => {});
    check('yaml file opens path-stack editor, not a raw textarea', await page.evaluate(() => {
      const m = [...document.querySelectorAll('.ant-modal')].find((x) => x.offsetParent !== null);
      return !!m && !!m.querySelector('[data-testid="yaml-editor"]') &&
        !!m.querySelector('[data-testid="yaml-nav"]') && !!m.querySelector('[data-testid="yaml-kv-d0"]') &&
        !m.querySelector('textarea.code');
    }));
    check('path stack is flat: file + document rows, keys table shows values', await page.evaluate(() => {
      const m = [...document.querySelectorAll('.ant-modal')].find((x) => x.offsetParent !== null);
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
      const m = [...document.querySelectorAll('.ant-modal')].find((x) => x.offsetParent !== null);
      const d2 = m?.querySelector('[data-testid="yaml-open-d1"]');
      return !!d2 && /document 2/.test(d2.textContent);
    }));
    await mouseClick('[data-testid="yaml-open-d1"]');
    await sleep(600);
    check('document 2 opens as its own tree', await page.evaluate(() => {
      const m = [...document.querySelectorAll('.ant-modal')].find((x) => x.offsetParent !== null);
      const k = m?.querySelector('[data-testid="yaml-key-d1.0"]');
      return !!m.querySelector('[data-testid="yaml-kv-d1"]') && !!k && k.value === 'agent';
    }));
    await mouseClick('[data-testid="yaml-crumb-d"]');
    await sleep(600);
    await mouseClick('[data-testid="yaml-open-d0"]');
    await sleep(600);
    // clicking a table row pushes the stack: the leaf screen carries
    // the note above the line and the inline comment
    await mouseClick('[data-testid="yaml-valview-d0.0"]');
    await sleep(600);
    check('row click pushes leaf screen editing note + value + comment', await page.evaluate(() => {
      const m = [...document.querySelectorAll('.ant-modal')].find((x) => x.offsetParent !== null);
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
      const m = [...document.querySelectorAll('.ant-modal')].find((x) => x.offsetParent !== null);
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
      const m = [...document.querySelectorAll('.ant-modal')].find((x) => x.offsetParent !== null);
      return !!m && /Unpublished draft/.test(m.innerText);
    }));
    await mouseClick('[data-testid="editor-publish"]');
    await page
      .waitForFunction(() => {
        const m = [...document.querySelectorAll('.ant-modal')].find((x) => x.offsetParent !== null);
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
    await closeModals();

    // phone-width regression: at 390px the detail tables must NOT be
    // squeezed (values used to clip to a few characters) — they keep
    // real column widths and scroll horizontally, and the path stack
    // moves into the ☰ drawer.
    await page.setViewport({ width: 390, height: 844 });
    await new Promise((r) => setTimeout(r, 600));
    await page.evaluate(() =>
      [...document.querySelectorAll('tbody button')].find((b) => b.textContent === 'walk-cfg.yml')?.click(),
    );
    await waitModal();
    await page.waitForFunction(
      () =>
        [...document.querySelectorAll('.ant-modal')]
          .find((x) => x.offsetParent !== null)
          ?.querySelector('[data-testid="yaml-kv-d0"]') ?? false,
      { timeout: 5000 },
    ).catch(() => {});
    // antd's breakpoint hook flips "narrow" a beat after the emulated
    // viewport change — wait for the mobile toolbar, not a sleep.
    await page.waitForFunction(
      () =>
        [...document.querySelectorAll('.ant-modal')]
          .find((x) => x.offsetParent !== null)
          ?.querySelector('[data-testid="yaml-nav-open"]') ?? false,
      { timeout: 5000 },
    ).catch(() => {});
    const mobileOk = await page.evaluate(() => {
      const m = [...document.querySelectorAll('.ant-modal')].find((x) => x.offsetParent !== null);
      if (!m) return { ok: false, why: 'no modal', modals: document.querySelectorAll('.ant-modal').length };
      const key = m.querySelector('[data-testid="yaml-key-d0.0"]');
      const kw = key ? key.getBoundingClientRect().width : 0;
      const box =
        m.querySelector('[data-testid="yaml-kv-d0"] .ant-table-content') ||
        m.querySelector('[data-testid="yaml-kv-d0"] .ant-table-body');
      const scrolls = !!box && box.scrollWidth > box.clientWidth + 40;
      return {
        ok: !!m.querySelector('[data-testid="yaml-nav-open"]') && kw >= 140 && scrolls,
        title: m.querySelector('.ant-modal-title, .ant-modal-header')?.innerText?.slice(0, 60) ?? '(none)',
        hasKv: !!m.querySelector('[data-testid="yaml-kv-d0"]'),
        hasEditor: !!m.querySelector('[data-testid="yaml-editor"]'),
        navBtn: !!m.querySelector('[data-testid="yaml-nav-open"]'),
        kw, scrolls,
      };
    });
    if (!mobileOk.ok) console.log('  dbg mobile:', JSON.stringify(mobileOk));
    check('phone width: path stack in drawer, tables keep width + scroll sideways', mobileOk.ok);
    await closeModals();
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
          [...document.querySelectorAll('tbody button')].some((b) => b.textContent === 'report-q3.pdf');
      }),
    );

    // file row menu: permissions/rename/delete live behind the ⋮ now
    check(
      'file row menu lists permissions/rename/delete',
      (await openRowMenu('report-q3')) &&
        (await visibleMenuText()).includes('permissions') &&
        (await visibleMenuText()).includes('rename') &&
        (await visibleMenuText()).includes('delete'),
    );
    await closeRowMenu();

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

    // clicking a flash entry opens the recent-events log
    const flashPos = await page.evaluate(() => {
      const el = document.querySelector('[data-testid="flash-bar"] .flash-item');
      if (!el) return null;
      const q = el.getBoundingClientRect();
      return { x: q.x + 20, y: q.y + q.height / 2 };
    });
    check('flash entry clickable', flashPos !== null);
    if (flashPos) await page.mouse.click(flashPos.x, flashPos.y);
    await new Promise((r) => setTimeout(r, 800));
    check('clicking flash opens event log', await page.evaluate(() => {
      const p = [...document.querySelectorAll('.ant-popover')].find((x) => x.offsetParent !== null);
      return !!p && /tenant name taken/.test(p.innerText) && /clear/.test(p.innerText);
    }));
    // clear() empties the history: the log closes and the bar disappears
    await mouseClick('.ant-popover button', '^clear$');
    await new Promise((r) => setTimeout(r, 700));
    check('clear removes flash history and bar', await page.evaluate(
      () => !document.querySelector('[data-testid="flash-bar"]') &&
        ![...document.querySelectorAll('.ant-popover')].some((p) => p.offsetParent !== null),
    ));

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
      return !!b && /grant saved/.test(b.innerText);
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
