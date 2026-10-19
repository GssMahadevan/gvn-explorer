// Screenshot the real app in light and dark mode for visual inspection.
// Needs a running server + fixture data; scripts/test_gui.sh (SHOTS=1)
// boots both. Env: BASE_URL, CHROME_PATH, OUT_DIR (same as walk.js).
const path = require('path');
const fs = require('fs');
const os = require('os');
const puppeteer = require('puppeteer-core');
const CHROME = process.env.CHROME_PATH || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const B = process.env.BASE_URL || 'http://localhost:8096';
const OUT = process.env.OUT_DIR || path.join(os.tmpdir(), 'fx-gui');
fs.mkdirSync(OUT, { recursive: true });

(async () => {
  const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new' });
  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 900 });
  await page.goto(B + '/', { waitUntil: 'networkidle2' });
  if (await page.$('input[autocomplete="organization"]')) {
    await page.type('input[autocomplete="organization"]', 'system');
    await page.type('input[autocomplete="username"]', 'admin');
    await page.type('input[autocomplete="current-password"]', 'secret');
    await page.evaluate(() =>
      [...document.querySelectorAll('button')].find((b) => /login/i.test(b.textContent)).click(),
    );
    await page.waitForSelector('.app-user', { timeout: 8000 });
  }
  await new Promise((r) => setTimeout(r, 1200));
  await page.screenshot({ path: path.join(OUT, 'light.png') });

  // toggle dark via the real Settings modal
  await page.click('.app-user');
  await page.waitForFunction(
    () => [...document.querySelectorAll('.ant-dropdown')].some((d) => d.offsetParent !== null),
    { timeout: 4000 },
  );
  await page.evaluate(() => {
    const it = [...document.querySelectorAll('.ant-dropdown [role="menuitem"]')].find((x) =>
      x.textContent.includes('Settings'),
    );
    it && it.click();
  });
  await page.waitForSelector('.ant-modal .ant-switch', { timeout: 4000 });
  await page.evaluate(() =>
    [...document.querySelectorAll('.ant-modal .ant-switch')].find((s) => s.offsetParent !== null)?.click(),
  );
  await new Promise((r) => setTimeout(r, 800));
  await page.keyboard.press('Escape');
  await new Promise((r) => setTimeout(r, 600));
  await page.screenshot({ path: path.join(OUT, 'dark.png') });
  await browser.close();
  console.log('wrote ' + path.join(OUT, 'light.png') + ' and dark.png');
})();
