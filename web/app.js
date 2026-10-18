const $ = (id) => document.getElementById(id);
let me = null, cur = "/";
let users = [], groups = []; // current page rows, for per-row actions

const api = (path, opts = {}) =>
  fetch(path, { headers: { 'Content-Type': 'application/json' }, ...opts }).then(async (r) => {
    if (r.status === 204) return null;
    const body = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(body?.error?.message || r.statusText);
    return body;
  });

// apiP: list fetch. X-Total-Count carries the filtered row total, so the
// pager knows the last page even though only one page of rows is sent.
async function apiP(path, opts = {}) {
  const r = await fetch(path, { headers: { 'Content-Type': 'application/json' }, ...opts });
  if (!r.ok) {
    const body = await r.json().catch(() => ({}));
    throw new Error(body?.error?.message || r.statusText);
  }
  const total = parseInt(r.headers.get('X-Total-Count') ?? '', 10);
  return { data: await r.json(), total: Number.isFinite(total) ? total : 0 };
}

function toast(msg, ok = true) {
  const t = $('toast');
  t.textContent = msg; t.className = ok ? 'ok' : 'bad'; t.hidden = false;
  clearTimeout(toast._h); toast._h = setTimeout(() => (t.hidden = true), 3500);
}
const esc = (s) => String(s ?? '').replace(/[&<>"']/g, c =>
  ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const fmtSize = (n) => n >= 1e6 ? (n / 1e6).toFixed(1) + ' MB' : n >= 1e3 ? (n / 1e3).toFixed(1) + ' kB' : n;
const bits = (p) => (p.read ? 'r' : '-') + (p.write ? 'w' : '-') + (p.delete ? 'd' : '-');
const expBadge = (x) => x.expired
  ? ' <span class="exp dead">&#x23f3; expired</span>'
  : x.expires_at ? ` <span class="exp">&#x23f3; till ${new Date(x.expires_at).toLocaleDateString()}</span>` : '';
const offBadge = (x) => x.active === false ? ' <span class="exp dead">&#x26d4; inactive</span>' : '';

// ---- list state: paging + search + sort, all server-side, per panel ----
const PAGE_SIZES = [10, 25, 50, 100];
const views = {
  files:   { limit: 25, offset: 0, total: 0, q: '', col: '', sort: 'name', dir: 'asc' },
  perms:   { limit: 25, offset: 0, total: 0, q: '', col: '', sort: 'principal', dir: 'asc' },
  users:   { limit: 25, offset: 0, total: 0, q: '', col: '', sort: 'username', dir: 'asc' },
  groups:  { limit: 25, offset: 0, total: 0, q: '', col: '', sort: 'name', dir: 'asc' },
  tenants: { limit: 25, offset: 0, total: 0, q: '', col: '', sort: 'name', dir: 'asc' },
};
function listQS(v) {
  let s = `limit=${v.limit}&offset=${v.offset}&sort=${v.sort}&dir=${v.dir}`;
  if (v.q) { s += '&q=' + encodeURIComponent(v.q); if (v.col) s += '&col=' + v.col; }
  return s;
}

function renderPager(view, reload) {
  const v = views[view], el = $('pg-' + view);
  const pages = Math.max(1, Math.ceil(v.total / v.limit));
  const page = Math.floor(v.offset / v.limit) + 1;
  const from = v.total === 0 ? 0 : v.offset + 1;
  const to = Math.min(v.total, v.offset + v.limit);
  el.innerHTML =
    `<button data-pg="first" ${page <= 1 ? 'disabled' : ''} title="first page">&#171;</button>` +
    `<button data-pg="prev" ${page <= 1 ? 'disabled' : ''} title="previous page">&#8249; prev</button>` +
    `<span class="pginfo">page ${page} / ${pages} &#8212; rows ${from}&#8211;${to} of ${v.total}</span>` +
    `<button data-pg="next" ${page >= pages ? 'disabled' : ''} title="next page">next &#8250;</button>` +
    `<button data-pg="last" ${page >= pages ? 'disabled' : ''} title="last page">&#187; last</button>` +
    `<label class="pgsize">rows <select data-pg="size">` +
    PAGE_SIZES.map(n => `<option value="${n}" ${n === v.limit ? 'selected' : ''}>${n}</option>`).join('') +
    `</select></label>`;
  el.onclick = (e) => {
    const act = e.target.dataset?.pg;
    if (!act || act === 'size') return;
    const pages = Math.max(1, Math.ceil(v.total / v.limit));
    if (act === 'first') v.offset = 0;
    else if (act === 'prev') v.offset = Math.max(0, v.offset - v.limit);
    else if (act === 'next') v.offset = Math.min((pages - 1) * v.limit, v.offset + v.limit);
    else if (act === 'last') v.offset = (pages - 1) * v.limit;
    reload();
  };
  el.onchange = (e) => {
    if (e.target.dataset?.pg !== 'size') return;
    v.limit = parseInt(e.target.value, 10);
    v.offset = 0;
    reload();
  };
}

function renderSortMarks(view) {
  const v = views[view];
  document.querySelectorAll(`th[data-view="${view}"]`).forEach(th => {
    th.querySelector('.sortmark').textContent =
      th.dataset.key === v.sort ? (v.dir === 'asc' ? '\u25b2' : '\u25bc') : '';
  });
}

function emptyMsg(view, count) {
  const v = views[view], p = $('empty-' + view);
  if (count > 0) { p.hidden = true; return; }
  p.hidden = false;
  p.textContent = v.q
    ? `nothing matches "${v.q}"`
    : { files: 'empty (or nothing readable here)',
        perms: 'no grants on this exact path (inheritance may still apply)',
        users: 'no users', groups: 'no groups', tenants: 'no tenants' }[view];
}

// after a delete the current page may have moved past the end: step back.
function clampPage(v, reload) {
  if (v.total > 0 && v.offset >= v.total) {
    v.offset = Math.floor((v.total - 1) / v.limit) * v.limit;
    reload();
    return true;
  }
  return false;
}

// ---- files panel ----
async function loadDir(keepPage) {
  const v = views.files;
  if (!keepPage) v.offset = 0;
  try {
    const { data, total } = await apiP(`/api/v1/fs?path=${encodeURIComponent(cur)}&${listQS(v)}`);
    v.total = total;
    const ents = data.entries || [];
    if (clampPage(v, () => loadDir(true))) return;
    renderCrumbs();
    $('rows').innerHTML = ents.map((x) => {
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
    emptyMsg('files', ents.length);
    renderSortMarks('files');
    renderPager('files', () => loadDir(true));
  } catch (err) { toast(err.message, false); }
}

function renderCrumbs() {
  const parts = cur.split('/').filter(Boolean);
  let html = `<button class="crumb ${parts.length ? '' : 'here'}" data-p="/">/</button>`;
  let acc = '';
  parts.forEach((seg, i) => {
    acc += '/' + seg;
    html += ` / <button class="crumb ${i === parts.length - 1 ? 'here' : ''}" data-p="${esc(acc)}">${esc(seg)}</button>`;
  });
  $('crumbs').innerHTML = html;
}

// ---- perms panel ----
async function loadPerms(keepPage) {
  const v = views.perms;
  if (!keepPage) v.offset = 0;
  $('pm-path').textContent = cur;
  try {
    const { data, total } = await apiP(`/api/v1/perms?path=${encodeURIComponent(cur)}&${listQS(v)}`);
    v.total = total;
    const eff = data.effective;
    $('pm-effective').textContent = me.is_super
      ? 'you are platform super admin \u2014 ACLs do not apply to you'
      : `effective for you: ${bits(eff)}${eff.source ? ' (inherited from ' + eff.source + ')' : ' (no access)'}`;
    const grants = data.grants || [];
    if (clampPage(v, () => loadPerms(true))) return;
    $('pm-rows').innerHTML = grants.map(g => `<tr data-id="${g.id}">
      <td>${g.principal_type}: ${esc(g.principal_tenant)}/${esc(g.principal_name)}${g.principal_active === false ? ' <span class="exp dead">&#x26d4; inactive principal</span>' : ''}${expBadge(g)}</td>
      <td>${g.read ? 'r' : '-'}</td><td>${g.write ? 'w' : '-'}</td><td>${g.delete ? 'd' : '-'}</td>
      <td><button data-act="del" class="danger">revoke</button></td></tr>`).join('');
    emptyMsg('perms', grants.length);
    renderSortMarks('perms');
    renderPager('perms', () => loadPerms(true));
  } catch (err) { toast(err.message, false); }
}

// ---- users panel ----
async function loadUsers(keepPage) {
  const v = views.users;
  if (!keepPage) v.offset = 0;
  try {
    const { data, total } = await apiP(`/api/v1/users?${listQS(v)}`);
    users = data; v.total = total;
    if (clampPage(v, () => loadUsers(true))) return;
    $('u-rows').innerHTML = users.map(u => `<tr data-id="${u.id}">
      <td>${esc(u.tenant)}</td>
      <td>${esc(u.username)}${offBadge(u)}${expBadge(u)}</td>
      <td>${u.is_super ? 'super' : u.is_admin ? 'yes' : ''}</td>
      <td class="muted">${new Date(u.created_at).toLocaleDateString()}</td>
      <td class="rowbtns">
        <button data-act="key" title="manage API keys for headless devices">apikey</button>
        <button data-act="pass" title="set a new password">password</button>
        <button data-act="active" title="suspend stops all access but keeps history">${u.active === false ? 'activate' : 'suspend'}</button>
        <button data-act="del" class="danger">delete</button>
      </td></tr>`).join('');
    emptyMsg('users', users.length);
    renderSortMarks('users');
    renderPager('users', () => loadUsers(true));
  } catch (err) { toast(err.message, false); }
}

// ---- groups panel ----
async function loadGroups(keepPage) {
  const v = views.groups;
  if (!keepPage) v.offset = 0;
  try {
    const { data, total } = await apiP(`/api/v1/groups?${listQS(v)}`);
    groups = data; v.total = total;
    if (clampPage(v, () => loadGroups(true))) return;
    $('g-rows').innerHTML = groups.map(g => `<tr data-id="${g.id}">
      <td>${esc(g.tenant)}</td>
      <td>${esc(g.name)}${offBadge(g)}</td>
      <td>${(g.members || []).map(m => `<span class="chip">${esc(m.username)}<button data-act="rm" data-uid="${m.id}" title="remove member">&#x2715;</button></span>`).join(' ') || '<span class="muted">none</span>'}</td>
      <td class="rowbtns">
        <button data-act="active" title="suspend stops the group granting access but keeps history">${g.active === false ? 'activate' : 'suspend'}</button>
        <button data-act="del" class="danger">delete</button>
      </td></tr>`).join('');
    $('gm-group').innerHTML = groups.map(g =>
      `<option value="${g.id}">${esc(g.tenant)}/${esc(g.name)}</option>`).join('');
    emptyMsg('groups', groups.length);
    renderSortMarks('groups');
    renderPager('groups', () => loadGroups(true));
  } catch (err) { toast(err.message, false); }
}

// ---- tenants panel ----
async function loadTenants(keepPage) {
  const v = views.tenants;
  if (!keepPage) v.offset = 0;
  try {
    const { data, total } = await apiP(`/api/v1/tenants?${listQS(v)}`);
    v.total = total;
    if (clampPage(v, () => loadTenants(true))) return;
    $('t-rows').innerHTML = data.map(t => `<tr data-id="${t.id}">
      <td>${esc(t.name)}</td>
      <td class="muted">${new Date(t.created_at).toLocaleString()}</td>
      <td><button data-act="del" class="danger">delete</button></td></tr>`).join('');
    emptyMsg('tenants', data.length);
    renderSortMarks('tenants');
    renderPager('tenants', () => loadTenants(true));
  } catch (err) { toast(err.message, false); }
}

// ---- tabs: one panel per view ----
const TABS = ['files', 'perms', 'users', 'groups', 'tenants'];
const reloadView = { files: () => loadDir(), perms: () => loadPerms(), users: () => loadUsers(),
  groups: () => loadGroups(), tenants: () => loadTenants() };
$('tabs').addEventListener('click', (e) => {
  const b = e.target.closest('button'); if (!b) return;
  document.querySelectorAll('.tab').forEach(x => x.classList.toggle('active', x === b));
  for (const t of TABS) $('tab-' + t).hidden = t !== b.dataset.tab;
  reloadView[b.dataset.tab]();
});

// ---- column-header sorting ----
document.querySelectorAll('th.sortable').forEach(th => th.addEventListener('click', () => {
  const view = th.dataset.view, v = views[view], key = th.dataset.key;
  if (v.sort === key) v.dir = v.dir === 'asc' ? 'desc' : 'asc';
  else { v.sort = key; v.dir = 'asc'; }
  v.offset = 0;
  ({ files: loadDir, perms: loadPerms, users: loadUsers, groups: loadGroups, tenants: loadTenants })[view](true);
}));

// ---- search boxes (debounced) + column selectors ----
const qTimers = {};
function wireSearch(view) {
  const inp = $('q-' + view), sel = $('scol-' + view);
  const go = () => {
    const v = views[view];
    v.q = inp.value.trim();
    if (sel) v.col = sel.value;
    v.offset = 0;
    ({ files: () => loadDir(true), perms: () => loadPerms(true), users: () => loadUsers(true),
      groups: () => loadGroups(true), tenants: () => loadTenants(true) })[view]();
  };
  inp.addEventListener('input', () => { clearTimeout(qTimers[view]); qTimers[view] = setTimeout(go, 350); });
  if (sel) sel.addEventListener('change', go);
}
TABS.forEach(wireSearch);

// ---- files panel actions ----
$('crumbs').addEventListener('click', (e) => {
  const b = e.target.closest('.crumb'); if (b) { cur = b.dataset.p; loadDir(); }
});
$('rows').addEventListener('click', async (e) => {
  const tr = e.target.closest('tr'); if (!tr) return;
  const path = tr.dataset.p, isDir = tr.dataset.dir === '1';
  if (e.target.classList.contains('openlink')) {
    if (isDir) { cur = path; loadDir(); } else openEditor(path);
    return;
  }
  const act = e.target.dataset?.act;
  try {
    if (act === 'del') {
      if (!confirm('delete ' + path + '?')) return;
      await api('/api/v1/fs?path=' + encodeURIComponent(path), { method: 'DELETE' });
      toast('deleted ' + path); loadDir(true);
    } else if (act === 'rename') {
      const to = prompt('new path (full):', path);
      if (!to || to === path) return;
      await api('/api/v1/fs/rename', { method: 'POST', body: JSON.stringify({ from: path, to }) });
      toast('renamed'); loadDir(true);
    }
  } catch (err) { toast(err.message, false); }
});
$('new-dir').addEventListener('click', async () => {
  const name = prompt('new folder name:'); if (!name) return;
  const p = (cur === '/' ? '' : cur) + '/' + name;
  try { await api('/api/v1/fs/mkdir', { method: 'POST', body: JSON.stringify({ path: p }) }); loadDir(true); }
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
    toast('uploaded ' + b.path); loadDir(true);
  } catch (err) { toast(err.message, false); }
  e.target.value = '';
});

// ---- editor overlay ----
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
$('ed-close').addEventListener('click', () => { $('editor').hidden = true; loadDir(true); });

// ---- perms panel actions ----
$('pm-rows').addEventListener('click', async (e) => {
  if (e.target.dataset?.act !== 'del') return;
  const id = e.target.closest('tr').dataset.id;
  try { await api('/api/v1/perms/' + id, { method: 'DELETE' }); toast('grant revoked'); loadPerms(true); }
  catch (err) { toast(err.message, false); }
});
$('pm-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const body = {
    path: cur, principal_type: $('pm-type').value, principal: $('pm-principal').value,
    tenant: $('pm-tenant').value || me.tenant,
    read: $('pm-r').checked, write: $('pm-w').checked, delete: $('pm-d').checked,
  };
  if ($('pm-exp').value) body.expires_days = parseFloat($('pm-exp').value);
  try {
    await api('/api/v1/perms', { method: 'PUT', body: JSON.stringify(body) });
    toast('grant saved'); loadPerms(true);
  } catch (err) { toast(err.message, false); }
});

// ---- users panel actions ----
$('u-rows').addEventListener('click', async (e) => {
  const act = e.target.dataset?.act; if (!act) return;
  const id = e.target.closest('tr').dataset.id;
  try {
    if (act === 'del') {
      if (!confirm('delete user ' + id + '?')) return;
      await api('/api/v1/users/' + id, { method: 'DELETE' });
    } else if (act === 'active') {
      const on = users.find(x => String(x.id) === id)?.active !== false;
      if (on && !confirm('suspend this user? live sessions and API keys die immediately, but the row and history stay.')) return;
      await api('/api/v1/users/' + id + '/active', { method: 'POST', body: JSON.stringify({ active: !on }) });
    } else if (act === 'pass') {
      const pw = prompt('new password:'); if (!pw) return;
      await api('/api/v1/users/' + id + '/password', { method: 'POST', body: JSON.stringify({ password: pw }) });
    } else if (act === 'key') {
      const r = await api('/api/v1/users/' + id + '/factors', { method: 'POST', body: JSON.stringify({ method: 'apikey' }) });
      prompt('API key (shown ONCE, store it in the device):', r.key);
      toast('API key provisioned'); return;
    }
    toast('ok'); loadUsers(true);
  } catch (err) { toast(err.message, false); }
});
$('u-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const body = { tenant: $('u-tenant').value, username: $('u-name').value, password: $('u-pass').value, is_admin: $('u-admin').checked };
  if ($('u-exp').value) body.expires_days = parseFloat($('u-exp').value);
  try { await api('/api/v1/users', { method: 'POST', body: JSON.stringify(body) }); toast('user created'); $('u-name').value = ''; $('u-pass').value = ''; loadUsers(true); }
  catch (err) { toast(err.message, false); }
});

// ---- groups panel actions ----
$('g-rows').addEventListener('click', async (e) => {
  const act = e.target.dataset?.act; if (!act) return;
  const gid = e.target.closest('tr').dataset.id;
  try {
    if (act === 'rm') {
      await api(`/api/v1/groups/${gid}/members/${e.target.dataset.uid}`, { method: 'DELETE' });
      toast('member removed');
    } else if (act === 'active') {
      const on = groups.find(x => String(x.id) === gid)?.active !== false;
      if (on && !confirm('suspend this group? grants through it become inert until reinstated.')) return;
      await api('/api/v1/groups/' + gid + '/active', { method: 'POST', body: JSON.stringify({ active: !on }) });
      toast(on ? 'group suspended' : 'group activated');
    } else if (act === 'del') {
      if (!confirm('delete group?')) return;
      await api('/api/v1/groups/' + gid, { method: 'DELETE' });
      toast('group deleted');
    }
    loadGroups(true);
  } catch (err) { toast(err.message, false); }
});
$('g-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  try { await api('/api/v1/groups', { method: 'POST', body: JSON.stringify({ tenant: $('g-tenant').value, name: $('g-name').value }) }); toast('group created'); $('g-name').value = ''; loadGroups(true); }
  catch (err) { toast(err.message, false); }
});
$('gm-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  try {
    await api('/api/v1/groups/' + $('gm-group').value + '/members', { method: 'POST', body: JSON.stringify({ username: $('gm-user').value.trim() }) });
    toast('member added'); $('gm-user').value = ''; loadGroups(true);
  } catch (err) { toast(err.message, false); }
});

// ---- tenants panel actions ----
$('t-rows').addEventListener('click', async (e) => {
  if (e.target.dataset?.act !== 'del') return;
  if (!confirm('delete tenant \u2014 removes its users, groups AND all their ACL grants?')) return;
  try { await api('/api/v1/tenants/' + e.target.closest('tr').dataset.id, { method: 'DELETE' }); toast('tenant deleted'); loadTenants(true); }
  catch (err) { toast(err.message, false); }
});
$('t-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  try { await api('/api/v1/tenants', { method: 'POST', body: JSON.stringify({ name: $('t-name').value }) }); toast('tenant created'); $('t-name').value = ''; loadTenants(true); }
  catch (err) { toast(err.message, false); }
});

// ---- session ----
async function boot() {
  try { me = await api('/api/v1/auth/me'); showApp(); }
  catch { $('login').hidden = false; }
}
$('login-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  try {
    me = await api('/api/v1/auth/login', { method: 'POST', body: JSON.stringify({ tenant: $('l-tenant').value, username: $('l-user').value, password: $('l-pass').value }) });
    $('login').hidden = true; showApp();
  } catch (err) { $('login-error').textContent = err.message; $('login-error').hidden = false; }
});
$('logout').addEventListener('click', async () => {
  await api('/api/v1/auth/logout', { method: 'POST' });
  location.reload();
});

function showApp() {
  $('app').hidden = false;
  const role = me.is_super ? 'super' : me.is_admin ? 'admin' : '';
  $('who').textContent = `${me.tenant}/${me.username}` + (role ? ` (${role})` : '');
  const isMgr = me.is_admin || me.is_super;
  document.querySelectorAll('.admin-only').forEach(el => { el.hidden = !isMgr; });
  document.querySelectorAll('.super-only').forEach(el => { el.hidden = !me.is_super; });
  if (me.tenant) $('pm-tenant').placeholder = me.tenant;
  loadDir();
}

boot();
