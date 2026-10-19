// Demo-video recorder: builds docs/demo-<scenario>.mp4 from real footage.
//
//   node record.js standalone|docker   (expects the app at FX_DEMO_URL)
//
// Scenes come from scenarios.js. Every scene gets a narration track
// generated with macOS `say`, and frames captured from headless Chrome
// while the scripted GUI actions run in real time (real mouse + keyboard
// events, so the cursor work looks like a human demo; vocabulary mirrors
// scripts/gui/walk.js: row menus, modals, antd selects, CodeMirror).
// A GUI scene never ends before its (estimated) actions finish, even if
// the narration is shorter. ffmpeg muxes each scene with padded
// narration, then concatenates with +faststart so browsers can stream
// the file from GitHub without buffering the whole thing.
// All intermediates live in /tmp/fx-demo/<scenario>/.

const { spawnSync } = require("child_process");
const fs = require("fs");
const path = require("path");
const { SCENARIOS } = require("./scenarios");

const FPS = Number(process.env.DEMO_FPS || 12);
const VOICE = process.env.DEMO_VOICE || "Samantha";
const RATE = Number(process.env.DEMO_RATE || 168);
const W = 1280, H = 720;
const BASE = process.env.FX_DEMO_URL || "http://localhost:8186";

const run = (cmd, args) => {
  const r = spawnSync(cmd, args, { encoding: "utf8" });
  if (r.status !== 0) throw new Error(`${cmd} failed: ${r.stderr}`);
  return r.stdout;
};

// rough worst-case duration per step type, used to floor scene length
const EST = {
  wait: (s) => s.wait, waitSel: () => 600, type: (s) => s.type.text.length * 120 + 1500,
  clickText: () => 2000, click: () => 1500, tab: () => 1800, btn: () => 1600,
  fill: (s) => s.fill.text.length * 100 + 1500, mbtn: () => 1300, rowMenu: () => 2800,
  cm: (s) => s.cm.length * 100 + 1500, clickSel: () => 1400, yamlOpen: () => 1600,
  setLeaf: () => 3200, search: () => 1600, opt: () => 1600, typeIn: (s) => s.typeIn.text.length * 100 + 1500,
};
const estimate = (steps) =>
  steps.reduce((t, s) => t + ((EST[Object.keys(s)[0]] || (() => 500))(s) || 0), 0) + 3000;

// ---------- static scene html ----------
const CAPTION_CSS = `position:fixed;left:0;right:0;bottom:0;padding:10px 24px;pointer-events:none;
  background:rgba(0,0,0,.75);color:#fff;font:500 22px/1.35 -apple-system,sans-serif;z-index:9999`;
const CURSOR_CSS = `position:fixed;width:22px;height:22px;border-radius:50%;
  background:rgba(255,80,80,.85);border:2px solid #fff;transform:translate(-999px,-999px);
  z-index:9998;pointer-events:none;transition:transform .12s linear`;

function cardHtml(s) {
  return `<!doctype html><meta charset=utf-8><style>html,body{margin:0;height:100%;
  background:linear-gradient(135deg,#0b1220,#12304d);color:#fff;font-family:-apple-system,sans-serif;
  display:flex;align-items:center;justify-content:center}
  .t{text-align:center}.t h1{font-size:64px;margin:0 0 12px}.t p{font-size:26px;opacity:.75;margin:0}
  .cap{${CAPTION_CSS}}</style><div class=t><h1>${s.title}</h1><p>${s.sub}</p></div>
  <div class=cap>${s.caption}</div>`;
}

function termHtml(s) {
  return `<!doctype html><meta charset=utf-8><style>html,body{margin:0;height:100%;
  background:#0d1117;color:#e6edf3;font:16px/1.5 Menlo,monospace;padding:24px;box-sizing:border-box}
  .p{color:#7ee787}.cmd{white-space:pre-wrap}.out{white-space:pre-wrap;color:#9aa7b4;opacity:.9}
  .cap{${CAPTION_CSS};left:0;bottom:0;position:fixed}</style><div id=log></div>
  <div class=cap>${s.caption}</div><script>
  const L=${JSON.stringify(s.lines)};const log=document.getElementById('log');
  window.T=async()=>{for(const l of L){if(l.cmd!==undefined){
    const p=document.createElement('div');p.innerHTML='<span class=p>gssm@mbp:~ % </span><span class=cmd></span>';log.append(p);
    const c=p.querySelector('.cmd');for(const ch of l.cmd){c.textContent+=ch;await new Promise(r=>setTimeout(r,12));}
    await new Promise(r=>setTimeout(r,450));}
    else{const d=document.createElement('div');d.className='out';log.append(d);
      for(const ln of l.out.split('\\n')){d.append(ln+'\\n');await new Promise(r=>setTimeout(r,150));}}
    window.scrollTo(0,1e9);}};
  window.T();</script>`;
}

// ---------- live-app step interpreter ----------
// Every step is best-effort: a missing element logs and skips, so one
// drifted selector can never abort a recording.
async function runSteps(page, steps) {
  const moveCursor = (x, y) =>
    page.evaluate(([x, y]) => (document.getElementById("fxcur").style.transform =
      `translate(${x}px,${y}px)`), [x, y]);

  // center of the first visible element matching sel (+ optional text/re)
  const rectOf = (sel, text, rowRe) =>
    page.evaluate((s, t, rr) => {
      let pool = [...document.querySelectorAll(s)].filter((e) => e.offsetParent !== null);
      if (rr) pool = pool.filter((e) => new RegExp(rr, "i").test(
        (e.closest("tr") || e.closest('[data-testid^="yaml-kv-"]') || e.parentElement)?.innerText ?? ""));
      const el = t ? pool.find((e) => (e.textContent || "").toLowerCase().includes(String(t).toLowerCase())) : pool[0];
      if (!el) return null;
      el.scrollIntoView({ block: "center", inline: "nearest" });
      const q = el.getBoundingClientRect();
      return { x: q.x + q.width / 2, y: q.y + q.height / 2 };
    }, sel, text ?? "", rowRe ?? "");

  const realClick = async (p) => {
    if (!p) return false;
    await moveCursor(p.x, p.y);
    await page.mouse.move(p.x, p.y, { steps: 10 });
    await page.evaluate((ms) => new Promise((r) => setTimeout(r, ms)), 220);
    await page.mouse.click(p.x, p.y);
    return true;
  };
  const clickAt = async (sel, text, rowRe) => realClick(await rectOf(sel, text, rowRe));
  const sleep = (ms) => page.evaluate((m) => new Promise((r) => setTimeout(r, m)), ms);

  // visible modal (antd portals several .ant-modal nodes; only one is open)
  const modalRect = (sel) =>
    page.evaluate((s) => {
      const m = [...document.querySelectorAll(".ant-modal")].find((x) => x.offsetParent !== null);
      const el = m && ((s && m.querySelector(s)) || m.querySelector("input:not([disabled])"));
      if (!el) return null;
      el.scrollIntoView({ block: "center" }); // tall modals put OK below the fold
      const q = el.getBoundingClientRect();
      return { x: q.x + q.width / 2, y: q.y + q.height / 2 };
    }, sel);

  const openRowMenu = async (rowRe) => {
    // the ⋮ column sits at the right edge of a horizontally scrolled table,
    // and lists refresh asynchronously after a create - so retry like
    // walk.js does for virtualized selects
    for (let attempt = 0; attempt < 3; attempt++) {
      if (await clickAt('[data-testid="row-actions"]', "", rowRe)) {
        try {
          await page.waitForFunction(
            () => [...document.querySelectorAll(".ant-dropdown")].some((d) => d.offsetParent !== null),
            { timeout: 4000 },
          );
          await sleep(500); // rc-motion settle
          return true;
        } catch { /* dropdown never opened; retry */ }
      }
      await sleep(800);
    }
    return false;
  };

  for (const st of steps) {
    const op = Object.keys(st)[0];
    try {
      if (op === "wait") await sleep(st.wait);
      else if (op === "waitSel") await page.waitForSelector(st.waitSel, { timeout: 8000 });
      else if (op === "tab") { await clickAt('[role="tab"]', st.tab); await sleep(1300); }
      else if (op === "btn") {
        const label = st.btn.replace(/[$^]/g, "");
        if (!(await clickAt("button", label))) {
          await page.evaluate((r) => {
            [...document.querySelectorAll("button")].find((b) => b.offsetParent !== null &&
              new RegExp(r, "i").test(b.textContent.trim()))?.click();
          }, st.btn.startsWith("^") ? st.btn : "^" + label + "$");
        }
        await sleep(600);
      }
      else if (op === "clickSel") await clickAt(st.clickSel);
      else if (op === "clickText") await clickAt(st.clickText.sel, st.clickText.text);
      else if (op === "type") { await clickAt(st.type.sel); await page.type(st.type.sel, st.type.text, { delay: 90 }); }
      else if (op === "typeIn") {
        await page.evaluate((s) => document.querySelector(s)?.focus(), st.typeIn.sel);
        await page.keyboard.type(st.typeIn.text, { delay: 90 });
      }
      else if (op === "fill") {
        const p = await modalRect(st.fill.sel);
        if (!p) throw new Error("modal input not found: " + st.fill.sel);
        await realClick(p);
        await page.keyboard.type(st.fill.text, { delay: 80 });
      }
      else if (op === "mbtn") {
        // submit, and retry once if the modal survived the click (antd
        // swallows input that lands during the open animation)
        let clicked = false;
        for (let attempt = 0; attempt < 2 && !clicked; attempt++) {
          const p = await page.evaluate((t) => {
            const m = [...document.querySelectorAll(".ant-modal")].find((x) => x.offsetParent !== null);
            const b = m && [...m.querySelectorAll("button")].find((x) => x.offsetParent !== null &&
              x.textContent.toLowerCase().includes(String(t).toLowerCase()));
            if (!b) return null;
            b.scrollIntoView({ block: "center" }); // tall modals put OK below the fold
            const q = b.getBoundingClientRect();
            return { x: q.x + q.width / 2, y: q.y + q.height / 2 };
          }, st.mbtn);
          if (!p) throw new Error("modal button not found: " + st.mbtn);
          await realClick(p);
          await sleep(700);
          clicked = await page.evaluate(() =>
            ![...document.querySelectorAll(".ant-modal")].some((x) => x.offsetParent !== null));
        }
        if (!clicked) console.error("  note: modal still open after mbtn:", st.mbtn);
      }
      else if (op === "rowMenu") {
        if (await openRowMenu(st.rowMenu.row)) await clickAt(".ant-dropdown [role='menuitem']", st.rowMenu.item);
        else throw new Error("row not found: " + st.rowMenu.row);
        await sleep(500);
      }
      else if (op === "cm") {
        if (!(await clickAt(".cm-content", "", ""))) throw new Error("no CodeMirror");
        await page.keyboard.type(st.cm, { delay: 60 });
      }
      else if (op === "yamlOpen") {
        // the key cell is an <input>, so row text never contains the key
        // name; match key inputs by value and drill via the open button
        // that shares the same path suffix (yaml-key-d0.2 -> yaml-open-d0.2)
        const p = await page.evaluate((t) => {
          const key = [...document.querySelectorAll('[data-testid^="yaml-key-"]')]
            .find((e) => e.offsetParent !== null && (e.value || "").toLowerCase().includes(String(t).toLowerCase()));
          if (!key) return null;
          const suffix = key.getAttribute("data-testid").replace(/^yaml-key-/, "");
          const btn = document.querySelector(`[data-testid="yaml-open-${suffix}"]`) ||
            key.closest("tr")?.querySelector('[data-testid^="yaml-open-"]');
          if (!btn) return null;
          btn.scrollIntoView({ block: "center" });
          const q = btn.getBoundingClientRect();
          return { x: q.x + q.width / 2, y: q.y + q.height / 2 };
        }, st.yamlOpen);
        if (!p) throw new Error("yaml row not found: " + st.yamlOpen);
        await realClick(p);
      }
      else if (op === "setLeaf") {
        if (!(await clickAt('[data-testid^="yaml-valview-"]', st.setLeaf.match)))
          throw new Error("yaml value not found");
        await sleep(800);
        const p = await rectOf('[data-testid^="file-pane"] [data-testid^="yaml-val-"]');
        if (!p) throw new Error("yaml leaf editor not found");
        await page.mouse.click(p.x, p.y);
        // the antd input ignores synthetic click focus; select explicitly
        // so the typed value replaces the old one on screen
        await page.evaluate(() => {
          const el = document.querySelector('[data-testid^="file-pane"] [data-testid^="yaml-val-"]');
          el.focus();
          el.setSelectionRange(0, el.value.length);
        });
        await page.keyboard.type(st.setLeaf.text, { delay: 80 });
      }
      else if (op === "search") {
        await page.evaluate((s, v) => {
          const i = document.querySelector(s);
          if (!i) return;
          Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value").set.call(i, v);
          i.dispatchEvent(new Event("input", { bubbles: true }));
        }, st.search.sel, st.search.text);
        await sleep(900);
      }
      else if (op === "opt") await clickAt(".ant-select-item-option", st.opt);
      else throw new Error("unknown step: " + op);
    } catch (e) {
      console.error("  step skipped:", JSON.stringify(st), "|", String(e.message).split("\n")[0]);
    }
  }
}

// ---------- capture one scene ----------
async function captureScene(browser, scene, dir, audio) {
  fs.mkdirSync(dir, { recursive: true });
  const dur = Number(run("ffprobe", ["-v", "error", "-show_entries", "format=duration",
    "-of", "csv=p=0", audio]).trim());
  const minDur = scene.steps ? estimate(scene.steps) / 1000 : 0;
  const total = Math.max(dur + 0.4, minDur);
  const frames = Math.ceil(total * FPS);

  const page = await browser.newPage();
  await page.setViewport({ width: W, height: H });
  if (scene.type === "card" || scene.type === "term") {
    await page.setContent(scene.type === "card" ? cardHtml(scene) : termHtml(scene));
  } else {
    await page.goto(BASE + scene.url, { waitUntil: "networkidle2" });
    await page.addStyleTag({ content: `#fxcap{${CAPTION_CSS}}#fxcur{${CURSOR_CSS}}` });
    await page.evaluate((cap) => {
      const c = document.createElement("div"); c.id = "fxcap"; c.textContent = cap; document.body.append(c);
      const k = document.createElement("div"); k.id = "fxcur"; document.body.append(k);
    }, scene.caption);
  }

  let n = 0;
  const t0 = Date.now();
  const timer = setInterval(() => {
    const want = Math.min(frames, Math.floor(((Date.now() - t0) / 1000) * FPS) + 1);
    (async () => {
      while (n < want) await page.screenshot({ path: path.join(dir, String(n++).padStart(4, "0") + ".png") });
    })();
  }, 1000 / FPS);

  if (scene.type === "gui") await runSteps(page, scene.steps);
  const slept = Date.now() - t0;
  if ((total - 0.2) * 1000 > slept) await page.evaluate((ms) => new Promise((r) => setTimeout(r, ms)), (total - 0.2) * 1000 - slept);
  clearInterval(timer);
  while (n < frames) await page.screenshot({ path: path.join(dir, String(n++).padStart(4, "0") + ".png") });
  await page.close();
  return { frames, dur };
}

// ---------- main ----------
module.exports = { runSteps };
if (require.main === module) (async () => {
  const scen = process.argv[2];
  const S = SCENARIOS[scen];
  if (!S) { console.error("usage: record.js standalone|docker"); process.exit(2); }
  const tmp = `/tmp/fx-demo/${scen}`;
  fs.rmSync(tmp, { recursive: true, force: true });
  fs.mkdirSync(tmp, { recursive: true });

  // reuse the GUI test harness's puppeteer-core + local Chrome (as walk.js)
  const puppeteer = require(path.join(__dirname, "../gui/node_modules/puppeteer-core"));
  const CHROME = process.env.CHROME_PATH || "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
  if (!fs.existsSync(CHROME)) { console.error("Chrome not found at " + CHROME + " (set CHROME_PATH)"); process.exit(2); }

  // narration first (frame counts derive from its length); say's AIFF has
  // an unreliable header, so normalize to WAV and use that everywhere.
  const audios = [];
  S.scenes.forEach((s, i) => {
    const aiff = path.join(tmp, `n${i}.aiff`);
    const wav = path.join(tmp, `n${i}.wav`);
    run("say", ["-v", VOICE, "-r", String(RATE), "-o", aiff, s.say]);
    run("ffmpeg", ["-y", "-v", "error", "-i", aiff, "-ar", "44100", "-ac", "1", wav]);
    audios.push(wav);
  });

  const browser = await puppeteer.launch({ executablePath: CHROME, headless: "new", args: ["--no-sandbox", "--force-device-scale-factor=1"] });
  const parts = [];
  for (let i = 0; i < S.scenes.length; i++) {
    const dir = path.join(tmp, `s${i}`);
    const { frames, dur } = await captureScene(browser, S.scenes[i], dir, audios[i]);
    console.log(`scene ${i} (${S.scenes[i].type}): ${frames} frames, ${(frames / FPS).toFixed(1)}s (narration ${dur.toFixed(1)}s)`);
    const mp4 = path.join(tmp, `s${i}.mp4`);
    run("ffmpeg", ["-y", "-v", "error", "-framerate", String(FPS), "-i", path.join(dir, "%04d.png"),
      "-i", audios[i], "-af", "apad", "-c:v", "libx264", "-crf", "21", "-preset", "medium",
      "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "112k", "-ar", "44100", "-shortest", mp4]);
    parts.push(mp4);
  }
  await browser.close();

  fs.writeFileSync(path.join(tmp, "list.txt"), parts.map((p) => `file '${p}'`).join("\n"));
  fs.mkdirSync(path.dirname(S.out), { recursive: true });
  // +faststart: moov index up front → no buffering lag when the file is
  // streamed from GitHub Releases / raw.githubusercontent
  run("ffmpeg", ["-y", "-v", "error", "-f", "concat", "-safe", "0", "-i", path.join(tmp, "list.txt"),
    "-c", "copy", "-movflags", "+faststart", S.out]);
  console.log("wrote", S.out);
})().catch((e) => { console.error(e); process.exit(1); });
