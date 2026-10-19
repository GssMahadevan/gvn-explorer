// Demo-video recorder: builds docs/demo-<scenario>.mp4 from real footage.
//
//   node record.js standalone|docker   (expects the app at FX_DEMO_URL)
//
// Scenes are declared per scenario below; every scene gets a narration
// track generated with macOS `say`, and frames captured from headless
// Chrome at DEMO_FPS while the scripted actions run in real time:
//   card  - title/end card (local HTML)
//   term  - typing-animation terminal (local HTML; the commands shown
//           are the real ones for the scenario)
//   gui   - the live app: cursor moves, clicks, keystrokes — same
//           selectors the GUI walk uses.
// ffmpeg muxes each scene (video + padded narration) and concatenates.
// Everything intermediate lives in /tmp/fx-demo/<scenario>/.

const { spawnSync } = require("child_process");
const fs = require("fs");
const path = require("path");

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

// ---------- scenarios ----------
const SCENARIOS = {
  standalone: {
    out: "docs/demo-standalone.mp4",
    scenes: [
      { type: "card", title: "gvn-explorer", sub: "Standalone — one self-contained binary",
        caption: "A self-hosted web file explorer — no install, no Docker",
        say: "Try g-v-n explorer standalone: one self-contained binary. No install, and no Docker." },
      { type: "term", caption: "Download the archive → unpack → run. That's the whole install.",
        lines: [
          { cmd: "curl -LO https://github.com/GssMahadevan/gvn-explorer/releases/download/v0.1.0/gvn-explorer-v0.1.0-linux-amd64.tar.gz" },
          { cmd: "tar xzf gvn-explorer-v0.1.0-linux-amd64.tar.gz" },
          { cmd: "cd gvn-explorer-v0.1.0-linux-amd64" },
          { cmd: "./gvn-explorer-v0.1.0-linux-amd64" },
          { out: 'INFO  file root        path=/srv/files' },
          { out: 'INFO  web gui enabled  dir=(embedded)' },
          { out: 'INFO  server starting  addr=:8080' },
        ],
        say: "Grab the archive for your platform from the releases page. Unpack it, and run the binary. That is the whole install. The web console and the full user guide live inside the binary itself, and on first run it creates its data folder and database." },
      { type: "gui", url: "/", caption: "Open http://localhost:8080 — first-run admin: system / admin / admin",
        say: "Now open localhost colon 8080 in a browser. Sign in with the first-run admin: tenant system, user admin, password admin. In a real deployment you would change that password right away.",
        steps: [
          { waitSel: 'input[autocomplete="organization"]' },
          { type: { sel: 'input[autocomplete="organization"]', text: "system" } },
          { type: { sel: 'input[autocomplete="username"]', text: "admin" } },
          { type: { sel: 'input[autocomplete="current-password"]', text: "admin" } },
          { clickText: { sel: "button", text: "login" } },
          { waitSel: ".app-user" },
        ] },
      { type: "gui", url: "/", caption: "Real files on disk · markdown renders · the guide is built in",
        say: "Your files stay plain files on disk: browse, search, open — markdown renders exactly as you would expect. And the brand menu's Help opens the complete user guide — permissions, sharing, API keys — offline, inside the app.",
        steps: [
          { waitSel: "tbody tr" },
          { clickText: { sel: "tbody button", text: "projects" } },
          { wait: 700 },
          { clickText: { sel: "tbody button", text: "release-notes.md" } },
          { wait: 1600 },
          { click: ".app-brand" },
          { wait: 500 },
          { clickText: { sel: ".ant-dropdown li, .ant-menu-item", text: "Help" } },
          { wait: 2000 },
        ] },
      { type: "card", title: "data/ + perms.db", sub: "…that's the whole backup. Upgrade = swap the binary.",
        caption: "Releases: github.com/GssMahadevan/gvn-explorer",
        say: "Back up one folder and one file. And upgrading is just swapping the binary." },
    ],
  },
  docker: {
    out: "docs/demo-docker.mp4",
    scenes: [
      { type: "card", title: "gvn-explorer", sub: "Docker — one command, multi-arch",
        caption: "Self-hosted file explorer, containerized",
        say: "Try g-v-n explorer with Docker: one command, a multi-arch image." },
      { type: "term", caption: "Your files at /data/root · accounts+audit at /data (back this up!)",
        lines: [
          { cmd: 'mkdir -p files state' },
          { cmd: 'docker run -d --name gvn -p 8080:8080 \\\n  -v "$PWD/files:/data/root" \\\n  -v "$PWD/state:/data" \\\n  ghcr.io/gssmahadevan/gvn-explorer:latest' },
          { cmd: "docker logs -f gvn" },
          { out: 'INFO  file root        path=/data/root' },
          { out: 'INFO  web gui enabled  dir=/app/web' },
          { out: 'INFO  server starting  addr=:8080' },
        ],
        say: "Run the published multi-arch image with two volumes: your file tree at slash data slash root, and slash data for accounts, permissions, and the audit trail — that is the one to back up. The container starts with a health-check and structured JSON logs." },
      { type: "gui", url: "/", caption: "localhost:8080 — admin set by the ADMIN env (default system/admin/admin)",
        say: "Point a browser at localhost colon 8080 — the same full console. Sign in with the admin you set with the A D M I N variable; the default is system, admin, admin.",
        steps: [
          { waitSel: 'input[autocomplete="organization"]' },
          { type: { sel: 'input[autocomplete="organization"]', text: "system" } },
          { type: { sel: 'input[autocomplete="username"]', text: "admin" } },
          { type: { sel: 'input[autocomplete="current-password"]', text: "admin" } },
          { clickText: { sel: "button", text: "login" } },
          { waitSel: ".app-user" },
        ] },
      { type: "gui", url: "/", caption: "Same app: draft+publish editing, per-path permissions, built-in guide",
        say: "Everything is here: browsing, editing with draft and publish, per-path permissions with inheritance, tenants, API keys — and the built-in guide, one click away in the Help menu.",
        steps: [
          { waitSel: "tbody tr" },
          { clickText: { sel: "tbody button", text: "projects" } },
          { wait: 700 },
          { clickText: { sel: "tbody button", text: "release-notes.md" } },
          { wait: 1500 },
          { click: ".app-brand" },
          { wait: 500 },
          { clickText: { sel: ".ant-dropdown li, .ant-menu-item", text: "Help" } },
          { wait: 1800 },
        ] },
      { type: "card", title: "ghcr.io/gssmahadevan/gvn-explorer", sub: "amd64 + arm64 · compose + k8s notes in the repo",
        caption: "Upgrade = pull a new image; data stays in the volumes",
        say: "Upgrades mean pulling a new image — your data stays in the volumes." },
    ],
  },
};

// ---------- html ----------
const CAPTION_CSS = `position:fixed;left:0;right:0;bottom:0;padding:10px 24px;
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
  const esc = (t) => t.replace(/&/g, "&amp;").replace(/</g, "&lt;");
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

// ---------- per-scene capture ----------
async function captureScene(browser, scene, dir, audio) {
  fs.mkdirSync(dir, { recursive: true });
  const dur = Number(run("ffprobe", ["-v", "error", "-show_entries", "format=duration",
    "-of", "csv=p=0", audio]
  ).trim());
  const frames = Math.ceil((dur + 0.4) * FPS);

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
      while (n < want) {
        await page.screenshot({ path: path.join(dir, String(n++).padStart(4, "0") + ".png") });
      }
    })();
  }, 1000 / FPS);

  if (scene.type === "gui") await guiSteps(page, scene.steps);
  const slept = Date.now() - t0;
  if ((dur + 0.25) * 1000 > slept) await page.evaluate((ms) => new Promise((r) => setTimeout(r, ms)), (dur + 0.25) * 1000 - slept);
  clearInterval(timer);
  // final catch-up so the frame count is exact
  while (n < frames) await page.screenshot({ path: path.join(dir, String(n++).padStart(4, "0") + ".png") });
  await page.close();
  return { frames, audio, dur };
}

async function guiSteps(page, steps) {
  const moveCursor = async (x, y) =>
    page.evaluate(([x, y]) => document.getElementById("fxcur")
      .style.transform = `translate(${x}px,${y}px)`, [x, y]);
  const hover = async (sel, text) => {
    let bx = null;
    if (text) {
      for (const c of await page.$$(sel)) {
        const t = await c.evaluate((e) => e.textContent || "");
        if (t.trim().toLowerCase().includes(String(text).toLowerCase())) { bx = await c.boundingBox(); break; }
      }
    } else {
      const el = await page.$(sel);
      if (el) bx = await el.boundingBox();
    }
    if (!bx) return null;
    const x = bx.x + bx.width / 2, y = bx.y + bx.height / 2;
    await moveCursor(x, y);
    await page.mouse.move(x, y, { steps: 12 });
    await page.evaluate((ms) => new Promise((r) => setTimeout(r, ms)), 260);
    return { x, y };
  };
  for (const st of steps) {
    try {
      if (st.waitSel) await page.waitForSelector(st.waitSel, { timeout: 8000 });
      else if (st.wait) await page.evaluate((ms) => new Promise((r) => setTimeout(r, ms)), st.wait);
      else if (st.type) {
        const b = await hover(st.type.sel);
        if (b) await page.mouse.click(b.x, b.y);
        await page.type(st.type.sel, st.type.text, { delay: 90 });
      } else if (st.click) {
        const b = await hover(st.click);
        if (b) await page.mouse.click(b.x, b.y);
      } else if (st.clickText) {
        const b = await hover(st.clickText.sel, st.clickText.text);
        if (b) await page.mouse.click(b.x, b.y);
      }
    } catch (e) { console.error("step skipped:", JSON.stringify(st), e.message.split("\n")[0]); }
  }
}

// ---------- main ----------
(async () => {
  const scen = process.argv[2];
  const S = SCENARIOS[scen];
  if (!S) { console.error("usage: record.js standalone|docker"); process.exit(2); }
  const tmp = `/tmp/fx-demo/${scen}`;
  fs.rmSync(tmp, { recursive: true, force: true });
  fs.mkdirSync(tmp, { recursive: true });
  // reuse the GUI test harness's puppeteer-core + local Chrome (same as walk.js)
  const puppeteer = require(path.join(__dirname, "../gui/node_modules/puppeteer-core"));
  const CHROME = process.env.CHROME_PATH || "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
  if (!fs.existsSync(CHROME)) { console.error("Chrome not found at " + CHROME + " (set CHROME_PATH)"); process.exit(2); }

  // 1. narration first (frame counts derive from its length). say's AIFF
  // has an unreliable header (ffprobe vs ffmpeg disagree on length), so
  // normalize to WAV immediately and use that everywhere.
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
    console.log(`scene ${i} (${S.scenes[i].type}): ${frames} frames, ${dur.toFixed(1)}s`);
    const mp4 = path.join(tmp, `s${i}.mp4`);
    run("ffmpeg", ["-y", "-v", "error", "-framerate", String(FPS), "-i", path.join(dir, "%04d.png"),
      "-i", audios[i], "-af", "apad", "-c:v", "libx264", "-crf", "21", "-preset", "medium",
      "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "112k", "-ar", "44100", "-shortest", mp4]);
    parts.push(mp4);
  }
  await browser.close();

  fs.writeFileSync(path.join(tmp, "list.txt"), parts.map((p) => `file '${p}'`).join("\n"));
  fs.mkdirSync(path.dirname(S.out), { recursive: true });
  run("ffmpeg", ["-y", "-v", "error", "-f", "concat", "-safe", "0", "-i", path.join(tmp, "list.txt"),
    "-c", "copy", S.out]);
  console.log("wrote", S.out);
})().catch((e) => { console.error(e); process.exit(1); });
