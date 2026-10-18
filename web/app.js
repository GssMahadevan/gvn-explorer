const $ = (id) => document.getElementById(id);
let me = null, cur = "/", entries = [];

const api = (path, opts = {}) =>
  fetch(path, { headers: { 'Content-Type': 'application/json' }, ...opts }).then(async (r) => {
    if (r.status === 204) return null;
    const body = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(body?.error?.message || r.statusText);
    return body;
  });

function toast(msg, ok = true) {
  const t = $('toast');
  t.textContent = msg; t.className = ok ? 'ok' : 'bad'; t.hidden = false;
  clearTimeout(toast._h); toast._h = setTimeout(() => (t.hidden = true), 3500);
}
const esc = (s) => String(s ?? '').replace(/[&<>"']/g, c =>
  ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const fmtSize = (n) => n >= 1e6 ? (n / 1e6).toFixed(1) + ' MB' : n >= 1e3 ? (n / 1e3).toFixed(1) + ' kB' : n;
const bits = (p) => (p.read ? 'r' : '-') + (p.write ? 'w' : '-') + (p.delete ? 'd' : '-');

// ---- auth ----
async function boot() {
  try { me = await api('/api/v1/auth/me'); showApp(); }
  catch { $('login').hidden = false; }
}
$('login-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  try {
    me = await api('/api/v1/auth/login', { method: 'POST', body: JSON.stringify({ username: $('l-user').value, password: $('l-pass').value }) });
    $('login').hidden = true; showApp();
  } catch (err) { $('login-error').textContent = err.message; $('login-error').hidden = false; }
});
$('logout').addEventListener('click', async () => {
  await api('/api/v1/auth/logout', { method: 'POST' });
  location.reload();
});

function showApp() {
  $('app').hidden = false;
  $('who').textContent = me.username + (me.is_admin ? ' (admin)' : '');
  document.querySelectorAll('.admin-only').forEach(el => { el.hidden = !me.is_admin; });
  loadDir(cur);
}

// ---- tabs ----
$('tabs').addEventListener('click', (e) => {
  const b = e.target.closest('button'); if (!b) return;
  document.querySelectorAll('.tab').forEach(x => x.classList.toggle('active', x === b));
  for (const t of ['files', 'perms', 'admin']) $('tab-' + t).hidden = t !== b.dataset.tab;
  if (b.dataset.tab === 'perms') loadPerms();
  if (b.dataset.tab === 'admin') loadAdmin();
});

// ---- explorer ----
async function loadDir(path) {
  cur = path;
  try {
    const data = await api('/api/v1/fs?path=' + encodeURIComponent(path));
    entries = data.entries || [];
    renderCrumbs(); renderRows();
  } catch (err) { toast(err.message, false); }
}

function renderCrumbs() {
  const parts = cur === '/' ? [] : cur.split('/').filter(Boolean);
  let html = `<button class="crumb" data-p="/">/</button>`;
  let acc = '';
  for (const p of parts) { acc += '/' + p; html += ` <span class="sep">›</span> <button class="crumb" data-p="${esc(acc)}">${esc(p)}</button>`; }
  $('crumbs').innerHTML = html;
}
$('crumbs').addEventListener('click', (e) => {
  const b = e.target.closest('.crumb'); if (b) loadDir(b.dataset.p);
});

function renderRows() {
  $('empty').hidden = entries.length > 0;
  $('rows').innerHTML = entries.map((x) => {
    const e = x.entry, p = x.perms;
    const name = e.is_dir ? e.name + '/' : e.name;
    return `<tr data-p="${esc(e.path)}" data-dir="${e.is_dir ? 1 : 0}">
      <td><button class="openlink ${e.is_dir ? 'dir' : ''}">${esc(name)}</button></td>
      <td class="num">${e.is_dir ? '' : fmtSize(e.size)}</td>
      <td class="muted">${new Date(e.mod_time).toLocaleString()}</td>
      <td class="mono perms">${bits(p)}${p.source && p.source !== e.path ? ` <span class="muted">via ${esc(p.source)}</span>` : ''}</td>
      <td class="rowbtns">
        <button data-act="rename">rename</button>
        <button data-act="del" class="danger">delete</button>
      </td></tr>`;
  }).join('');
}

$('rows').addEventListener('click', async (e) => {
  const tr = e.target.closest('tr'); if (!tr) return;
  const path = tr.dataset.p, isDir = tr.dataset.dir === '1';
  if (e.target.classList.contains('openlink')) {
    if (isDir) loadDir(path); else openEditor(path);
    return;
  }
  const act = e.target.dataset?.act;
  try {
    if (act === 'del') {
      if (!confirm('delete ' + path + '?')) return;
      await api('/api/v1/fs?path=' + encodeURIComponent(path), { method: 'DELETE' });
      toast('deleted ' + path); loadDir(cur);
    } else if (act === 'rename') {
      const to = prompt('new path (full):', path);
      if (!to || to === path) return;
      await api('/api/v1/fs/rename', { method: 'POST', body: JSON.stringify({ from: path, to }) });
      toast('renamed'); loadDir(cur);
    }
  } catch (err) { toast(err.message, false); }
});

$('new-dir').addEventListener('click', async () => {
  const name = prompt('new folder name:'); if (!name) return;
  const p = (cur === '/' ? '' : cur) + '/' + name;
  try { await api('/api/v1/fs/mkdir', { method: 'POST', body: JSON.stringify({ path: p }) }); loadDir(cur); }
  catch (err) { toast(err.message, false); }
});
$('new-file').addEventListener('click', async () => {
  const name = prompt('new file name:'); if (!name) return;
  const p = (cur === '/' ? '' : cur) + '/' + name;
  try { await api('/api/v1/fs/write', { method: 'POST', body: JSON.stringify({ path: p, content: '' }) }); openEditor(p); }
  catch (err) { toast(err.message, false); }
});
$('upload').addEventListener('change', async (e) => {
  const f = e.target.files[0]; if (!f) return;
  const fd = new FormData(); fd.set('path', cur); fd.set('file', f);
  try {
    const r = await fetch('/api/v1/fs/upload', { method: 'POST', body: fd });
    const b = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(b?.error?.message || r.statusText);
    toast('uploaded ' + b.path); loadDir(cur);
  } catch (err) { toast(err.message, false); }
  e.target.value = '';
});

// ---- editor ----
let edPath = null;
async function openEditor(path) {
  edPath = path;
  try {
    const d = await api('/api/v1/fs/content?path=' + encodeURIComponent(path));
    $('editor').hidden = false; $('ed-title').textContent = path;
    $('ed-msg').hidden = true;
    if (d.is_binary) {
      $('ed-text').hidden = true; $('ed-binary').hidden = false;
      $('ed-dl').href = '/api/v1/fs/raw?path=' + encodeURIComponent(path);
      $('ed-save').hidden = true;
    } else {
      $('ed-text').hidden = false; $('ed-binary').hidden = true;
      $('ed-text').value = d.content; $('ed-save').hidden = false;
    }
  } catch (err) { toast(err.message, false); }
}
$('ed-save').addEventListener('click', async () => {
  try {
    await api('/api/v1/fs/write', { method: 'POST', body: JSON.stringify({ path: edPath, content: $('ed-text').value }) });
    toast('saved ' + edPath); $('ed-msg').hidden = true;
  } catch (err) { $('ed-msg').textContent = err.message; $('ed-msg').hidden = false; }
});
$('ed-close').addEventListener('click', () => { $('editor').hidden = true; loadDir(cur); });

// ---- perms ----
async function loadPerms() {
  $('pm-path').textContent = cur;
  try {
    const d = await api('/api/v1/perms?path=' + encodeURIComponent(cur));
    const eff = d.effective;
    $('pm-effective').textContent = `effective for you: ${bits(eff)}${eff.source ? ' (from ' + eff.source + ')' : eff.read === undefined ? '' : ' (none)'}`;
    $('pm-rows').innerHTML = (d.grants || []).map(g => `<tr data-id="${g.id}">
      <td>${g.principal_type}: ${esc(g.principal_name)}</td>
      <td>${g.read ? 'r' : '-'}</td><td>${g.write ? 'w' : '-'}</td><td>${g.delete ? 'd' : '-'}</td>
      <td><button data-act="del" class="danger">revoke</button></td></tr>`).join('') || '<tr><td colspan=5 class="muted">no grants on this exact path (inheritance may still apply)</td></tr>';
  } catch (err) { toast(err.message, false); }
}
$('pm-rows').addEventListener('click', async (e) => {
  if (e.target.dataset?.act !== 'del') return;
  const id = e.target.closest('tr').dataset.id;
  try { await api('/api/v1/perms/' + id, { method: 'DELETE' }); loadPerms(); }
  catch (err) { toast(err.message, false); }
});
$('pm-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  try {
    await api('/api/v1/perms', { method: 'PUT', body: JSON.stringify({
      path: cur, principal_type: $('pm-type').value, principal: $('pm-principal').value,
      read: $('pm-r').checked, write: $('pm-w').checked, delete: $('pm-d').checked,
    }) });
    toast('grant saved'); loadPerms();
  } catch (err) { toast(err.message, false); }
});

// ---- admin ----
let users = [], groups = [];
async function loadAdmin() {
  try {
    users = await api('/api/v1/users'); groups = await api('/api/v1/groups');
    $('u-rows').innerHTML = users.map(u => `<tr data-id="${u.id}">
      <td>${esc(u.username)}</td><td>${u.is_admin ? 'yes' : ''}</td>
      <td class="muted">${new Date(u.created_at).toLocaleDateString()}</td>
      <td class="rowbtns"><button data-act="pass">password</button><button data-act="del" class="danger">delete</button></td></tr>`).join('');
    $('g-rows').innerHTML = groups.map(g => `<tr data-id="${g.id}">
      <td>${esc(g.name)}</td>
      <td>${(g.members || []).map(m => `<span class="chip">${esc(m.username)}<button data-act="rm" data-uid="${m.id}" title="remove">✕</button></span>`).join(' ') || '<span class="muted">none</span>'}</td>
      <td><button data-act="del" class="danger">delete</button></td></tr>`).join('');
    $('gm-group').innerHTML = groups.map(g => `<option value="${g.id}">${esc(g.name)}</option>`).join('');
    $('gm-user').innerHTML = users.map(u => `<option value="${u.id}">${esc(u.username)}</option>`).join('');
  } catch (err) { toast(err.message, false); }
}
$('u-rows').addEventListener('click', async (e) => {
  const act = e.target.dataset?.act; if (!act) return;
  const id = e.target.closest('tr').dataset.id;
  try {
    if (act === 'del') {
      if (!confirm('delete user ' + id + '?')) return;
      await api('/api/v1/users/' + id, { method: 'DELETE' });
    } else if (act === 'pass') {
      const pw = prompt('new password:'); if (!pw) return;
      await api('/api/v1/users/' + id + '/password', { method: 'POST', body: JSON.stringify({ password: pw }) });
    }
    toast('ok'); loadAdmin();
  } catch (err) { toast(err.message, false); }
});
$('g-rows').addEventListener('click', async (e) => {
  if (e.target.dataset?.act !== 'del') return;
  if (!confirm('delete group?')) return;
  try { await api('/api/v1/groups/' + e.target.closest('tr').dataset.id, { method: 'DELETE' }); loadAdmin(); }
  catch (err) { toast(err.message, false); }
});
$('u-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  try { await api('/api/v1/users', { method: 'POST', body: JSON.stringify({ username: $('u-name').value, password: $('u-pass').value, is_admin: $('u-admin').checked }) }); toast('user created'); loadAdmin(); }
  catch (err) { toast(err.message, false); }
});
$('g-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  try { await api('/api/v1/groups', { method: 'POST', body: JSON.stringify({ name: $('g-name').value }) }); toast('group created'); loadAdmin(); }
  catch (err) { toast(err.message, false); }
});
$('gm-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const u = users.find(x => String(x.id) === $('gm-user').value);
  try { await api('/api/v1/groups/' + $('gm-group').value + '/members', { method: 'POST', body: JSON.stringify({ username: u.username }) }); toast('member added'); loadAdmin(); }
  catch (err) { toast(err.message, false); }
});

boot();
